package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/pg"
)

const batchWindow = 4 * time.Hour

func capsuleBatchKey(capsuleID, userID uuid.UUID) string {
	return fmt.Sprintf("capsule:%s:%s", capsuleID, userID)
}

func reactionBatchKey(memoryID, userID uuid.UUID) string {
	return fmt.Sprintf("reaction:%s:%s", memoryID, userID)
}

func (s *OutboxService) computeBatchDeliverAfter(ctx context.Context, batchKey string) time.Time {
	now := time.Now().UTC()

	pending, err := s.Q.GetPendingBatchMinDeliverAfter(ctx, &batchKey)
	if err == nil && pending.Valid {
		return pg.TimeValue(pending).UTC()
	}

	lastSent, err := s.Q.GetLastBatchSentAt(ctx, &batchKey)
	if err != nil || !lastSent.Valid {
		return now.Add(batchWindow)
	}
	next := pg.TimeValue(lastSent).UTC().Add(batchWindow)
	if next.After(now) {
		return next
	}
	return now.Add(batchWindow)
}

// FlushBatches collapses due batched outbox rows into one push per batch_key.
func (s *OutboxService) FlushBatches(ctx context.Context) {
	keys, err := s.Q.ListDueBatchKeys(ctx, flushBatchSize)
	if err != nil {
		slog.ErrorContext(ctx, "notifications: listing due batch keys", "error", err)
		return
	}
	for _, key := range keys {
		if key == nil || *key == "" {
			continue
		}
		s.flushBatch(ctx, *key)
	}
}

// FlushDueBatchesForUser collapses due batches for a single user (app foreground).
func (s *OutboxService) FlushDueBatchesForUser(ctx context.Context, userID uuid.UUID) {
	keys, err := s.Q.ListDueBatchKeysForUser(ctx, pg.UUID(userID))
	if err != nil {
		slog.ErrorContext(ctx, "notifications: listing user batch keys", "error", err)
		return
	}
	for _, key := range keys {
		if key == nil || *key == "" {
			continue
		}
		s.flushBatch(ctx, *key)
	}
}

func (s *OutboxService) finalizeBatch(ctx context.Context, rows []dbgen.NotificationOutbox, title, body string, data map[string]any) error {
	rawData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if len(rows) > 1 {
		if err := s.Q.UpdateOutboxContent(ctx, dbgen.UpdateOutboxContentParams{
			ID:    rows[0].ID,
			Title: title,
			Body:  body,
			Data:  rawData,
		}); err != nil {
			return err
		}
		extra := make([]pgtype.UUID, len(rows)-1)
		for i, row := range rows[1:] {
			extra[i] = row.ID
		}
		if err := s.Q.DeleteOutboxRows(ctx, extra); err != nil {
			return err
		}
	}
	return s.Q.MarkOutboxSent(ctx, rows[0].ID)
}

func (s *OutboxService) flushBatch(ctx context.Context, batchKey string) {
	rows, err := s.Q.ListPendingBatchRows(ctx, &batchKey)
	if err != nil {
		slog.ErrorContext(ctx, "notifications: listing batch rows", "batch_key", batchKey, "error", err)
		return
	}
	if len(rows) == 0 {
		return
	}

	userID := pg.UUIDValue(rows[0].UserID)
	category := rows[0].Category
	ids := make([]pgtype.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}

	title, body, data := collapseBatchRows(rows)
	if data == nil {
		data = map[string]any{}
	}

	enabled, err := s.prefEnabled(ctx, userID, category)
	if err != nil {
		slog.ErrorContext(ctx, "notifications: loading prefs for batch", "error", err, "user", userID)
		return
	}
	if !enabled {
		if err := s.finalizeBatch(ctx, rows, title, body, data); err != nil {
			slog.ErrorContext(ctx, "notifications: marking skipped batch sent", "error", err)
		}
		return
	}

	tokens, err := s.Q.ListPushTokensForUser(ctx, pg.UUID(userID))
	if err != nil {
		slog.ErrorContext(ctx, "notifications: loading push tokens for batch", "error", err)
		return
	}

	if len(tokens) == 0 {
		if err := s.finalizeBatch(ctx, rows, title, body, data); err != nil {
			slog.ErrorContext(ctx, "notifications: marking batch sent (no tokens)", "error", err)
		}
		return
	}

	msgs := make([]pushMessage, 0, len(tokens))
	for _, tok := range tokens {
		msgs = append(msgs, pushMessage{
			To:    tok.ExpoToken,
			Title: title,
			Body:  body,
			Data:  data,
		})
	}

	sendResult, err := s.Expo.Send(ctx, msgs)
	if err != nil {
		slog.ErrorContext(ctx, "notifications: expo send batch", "error", err, "batch_key", batchKey)
		return
	}

	if err := s.finalizeBatch(ctx, rows, title, body, data); err != nil {
		slog.ErrorContext(ctx, "notifications: marking batch sent", "error", err)
		return
	}

	s.checkReceiptsAndPrune(ctx, userID, tokens, sendResult)
}

func collapseBatchRows(rows []dbgen.NotificationOutbox) (title, body string, data map[string]any) {
	n := len(rows)
	category := rows[0].Category

	_ = json.Unmarshal(rows[0].Data, &data)
	if data == nil {
		data = map[string]any{}
	}

	switch category {
	case CategoryCapsuleMemory:
		capsuleName, _ := data["capsule_name"].(string)
		if n == 1 {
			return rows[0].Title, rows[0].Body, data
		}
		title = "New memories"
		if capsuleName != "" {
			body = fmt.Sprintf("%d new memories in %s", n, capsuleName)
		} else {
			body = fmt.Sprintf("%d new memories in your capsule", n)
		}
		data["count"] = n
		data["type"] = "capsule_memory_batch"
		return title, body, data

	case CategoryReactionsComments:
		if n == 1 {
			return rows[0].Title, rows[0].Body, data
		}
		title = "New reactions"
		body = fmt.Sprintf("%d new reactions on your photo", n)
		data["count"] = n
		data["type"] = "reaction_batch"
		return title, body, data

	default:
		if n == 1 {
			return rows[0].Title, rows[0].Body, data
		}
		title = rows[0].Title
		body = fmt.Sprintf("%d notifications", n)
		data["count"] = n
		return title, body, data
	}
}
