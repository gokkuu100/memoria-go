package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/pg"
)

const flushBatchSize = 100

// OutboxService enqueues notification rows and flushes instant categories to Expo.
type OutboxService struct {
	Pool  *pgxpool.Pool
	Q     *dbgen.Queries
	Expo  *ExpoClient
	flush chan struct{}
	stop  chan struct{}
	wg    sync.WaitGroup
}

func NewOutboxService(pool *pgxpool.Pool, q *dbgen.Queries, expo *ExpoClient) *OutboxService {
	return &OutboxService{
		Pool:  pool,
		Q:     q,
		Expo:  expo,
		flush: make(chan struct{}, 1),
		stop:  make(chan struct{}),
	}
}

// Start runs the periodic instant flush loop (every 30s).
func (s *OutboxService) Start(ctx context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-s.flush:
				s.FlushInstant(context.Background())
			case <-ticker.C:
				s.FlushInstant(context.Background())
			}
		}
	}()
}

func (s *OutboxService) Stop() {
	close(s.stop)
	s.wg.Wait()
}

func (s *OutboxService) triggerFlush() {
	select {
	case s.flush <- struct{}{}:
	default:
	}
}

// Enqueue writes an outbox row. batchKey is nil for instant categories.
func (s *OutboxService) Enqueue(ctx context.Context, userID uuid.UUID, category, title, body string, data map[string]any, batchKey *string) error {
	return s.EnqueueAt(ctx, userID, category, title, body, data, batchKey, time.Now().UTC())
}

// EnqueueAt writes an outbox row with an explicit deliver_after timestamp.
func (s *OutboxService) EnqueueAt(ctx context.Context, userID uuid.UUID, category, title, body string, data map[string]any, batchKey *string, deliverAfter time.Time) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if raw == nil {
		raw = json.RawMessage("{}")
	}
	_, err = s.Q.CreateOutbox(ctx, dbgen.CreateOutboxParams{
		UserID:       pg.UUID(userID),
		Category:     category,
		Title:        title,
		Body:         body,
		Data:         raw,
		BatchKey:     batchKey,
		DeliverAfter: pg.Time(deliverAfter),
	})
	return err
}

func (s *OutboxService) FriendRequestReceived(ctx context.Context, toUserID, fromUserID uuid.UUID) {
	s.enqueueFriendEvent(ctx, toUserID, fromUserID, "Friend request",
		" sent you a friend request", "friend_request_received")
}

func (s *OutboxService) FriendRequestAccepted(ctx context.Context, toUserID, fromUserID uuid.UUID) {
	s.enqueueFriendEvent(ctx, toUserID, fromUserID, "New friend",
		" accepted your friend request", "friend_request_accepted")
}

func (s *OutboxService) AlbumInviteReceived(ctx context.Context, toUserID, fromUserID, albumID uuid.UUID, albumName string) {
	s.enqueueAlbumEvent(ctx, toUserID, fromUserID, albumID, albumName,
		CategoryAlbumInvites, "Album invite", " invited you to ", "album_invite_received")
}

func (s *OutboxService) AlbumInviteExpired(ctx context.Context, toUserID, albumID uuid.UUID, albumName string) {
	title := "Album invite expired"
	body := "Your album \"" + albumName + "\" was not accepted in time"
	data := map[string]any{
		"type":     "album_invite_expired",
		"album_id": albumID.String(),
	}
	if err := s.Enqueue(ctx, toUserID, CategorySystemAlerts, title, body, data, nil); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue album invite expired", "error", err)
		return
	}
	s.triggerFlush()
}

func (s *OutboxService) AlbumMemoryAdded(ctx context.Context, toUserID, fromUserID, albumID uuid.UUID, albumName string) {
	s.enqueueAlbumEvent(ctx, toUserID, fromUserID, albumID, albumName,
		CategoryAlbumMemory, "New memory", " added a photo to ", "album_memory_added")
}

func (s *OutboxService) CapsuleMemoryAdded(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string) {
	from, err := s.Q.GetUserByID(ctx, pg.UUID(fromUserID))
	if err != nil {
		slog.ErrorContext(ctx, "notifications: loading sender profile", "error", err)
		return
	}
	batchKey := capsuleBatchKey(capsuleID, toUserID)
	deliverAfter := s.computeBatchDeliverAfter(ctx, batchKey)
	title := "New memory"
	body := from.DisplayName + " added a memory to " + capsuleName
	data := map[string]any{
		"type":         "capsule_memory_added",
		"capsule_id":   capsuleID.String(),
		"from_user_id": fromUserID.String(),
		"capsule_name": capsuleName,
	}
	if err := s.EnqueueAt(ctx, toUserID, CategoryCapsuleMemory, title, body, data, &batchKey, deliverAfter); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue capsule memory", "error", err)
	}
}

func (s *OutboxService) CommentOnMemory(ctx context.Context, toUserID, fromUserID, memoryID uuid.UUID) {
	s.enqueueMemorySocialEvent(ctx, toUserID, fromUserID, memoryID,
		CategoryReactionsComments, "New comment", " commented on a photo", "comment_on_memory")
}

func (s *OutboxService) ReactionOnMemory(ctx context.Context, toUserID, fromUserID, memoryID uuid.UUID) {
	from, err := s.Q.GetUserByID(ctx, pg.UUID(fromUserID))
	if err != nil {
		slog.ErrorContext(ctx, "notifications: loading sender profile", "error", err)
		return
	}
	batchKey := reactionBatchKey(memoryID, toUserID)
	deliverAfter := s.computeBatchDeliverAfter(ctx, batchKey)
	title := "New reaction"
	body := from.DisplayName + " reacted to your photo"
	data := map[string]any{
		"type":         "reaction_on_memory",
		"memory_id":    memoryID.String(),
		"from_user_id": fromUserID.String(),
	}
	if err := s.EnqueueAt(ctx, toUserID, CategoryReactionsComments, title, body, data, &batchKey, deliverAfter); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue reaction", "error", err)
	}
}

func (s *OutboxService) CapsuleInviteReceived(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string) {
	s.enqueueCapsuleEvent(ctx, toUserID, fromUserID, capsuleID, capsuleName,
		CategoryCapsuleInvites, "Capsule invite", " invited you to ", "capsule_invite_received")
}

func (s *OutboxService) CapsuleInviteDeclined(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string) {
	s.enqueueCapsuleEvent(ctx, toUserID, fromUserID, capsuleID, capsuleName,
		CategoryCapsuleInvites, "Invite declined", " declined your invite to ", "capsule_invite_declined")
}

func (s *OutboxService) CapsuleInviteExpired(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string) {
	title := "Capsule invite expired"
	body := "Your capsule \"" + capsuleName + "\" was not accepted in time"
	data := map[string]any{
		"type":        "capsule_invite_expired",
		"capsule_id":  capsuleID.String(),
	}
	if err := s.Enqueue(ctx, toUserID, CategorySystemAlerts, title, body, data, nil); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue capsule invite expired", "error", err)
		return
	}
	s.triggerFlush()
}

func (s *OutboxService) CapsuleActivated(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string) {
	title := "Capsule is live"
	body := "\"" + capsuleName + "\" is now active — start contributing!"
	data := map[string]any{
		"type":       "capsule_activated",
		"capsule_id": capsuleID.String(),
	}
	if err := s.Enqueue(ctx, toUserID, CategoryCapsuleInvites, title, body, data, nil); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue capsule activated", "error", err)
		return
	}
	s.triggerFlush()
}

func (s *OutboxService) CapsuleFreezeWarning(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string) {
	title := "Capsule needs a memory"
	body := "Your capsule \"" + capsuleName + "\" hasn't had a memory in 5 days"
	data := map[string]any{
		"type":       "capsule_freeze_warning",
		"capsule_id": capsuleID.String(),
	}
	if err := s.Enqueue(ctx, toUserID, CategorySystemAlerts, title, body, data, nil); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue freeze warning", "error", err)
		return
	}
	s.triggerFlush()
}

func (s *OutboxService) CapsuleFrozen(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string) {
	title := "Capsule frozen"
	body := "\"" + capsuleName + "\" has been frozen due to inactivity"
	data := map[string]any{
		"type":       "capsule_frozen",
		"capsule_id": capsuleID.String(),
	}
	if err := s.Enqueue(ctx, toUserID, CategorySystemAlerts, title, body, data, nil); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue capsule frozen", "error", err)
		return
	}
	s.triggerFlush()
}

func (s *OutboxService) CapsuleUnfreezeVote(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string) {
	s.enqueueCapsuleEvent(ctx, toUserID, fromUserID, capsuleID, capsuleName,
		CategorySystemAlerts, "Unfreeze vote", " voted to unfreeze ", "capsule_unfreeze_vote")
}

func (s *OutboxService) CapsuleUnlocked(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string, deliverAfter time.Time) {
	title := "Capsule unlocked!"
	body := "\"" + capsuleName + "\" is ready to open"
	data := map[string]any{
		"type":       "capsule_unlocked",
		"capsule_id": capsuleID.String(),
	}
	if err := s.EnqueueAt(ctx, toUserID, CategoryCapsuleUnlock, title, body, data, nil, deliverAfter); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue capsule unlocked", "error", err)
	}
}

func (s *OutboxService) enqueueCapsuleEvent(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName, category, titlePrefix, bodySuffix, eventType string) {
	from, err := s.Q.GetUserByID(ctx, pg.UUID(fromUserID))
	if err != nil {
		slog.ErrorContext(ctx, "notifications: loading sender profile", "error", err)
		return
	}
	title := titlePrefix
	body := from.DisplayName + bodySuffix + capsuleName
	data := map[string]any{
		"type":         eventType,
		"capsule_id":   capsuleID.String(),
		"from_user_id": fromUserID.String(),
	}
	if err := s.Enqueue(ctx, toUserID, category, title, body, data, nil); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue capsule event", "error", err)
		return
	}
	s.triggerFlush()
}

func (s *OutboxService) enqueueAlbumEvent(ctx context.Context, toUserID, fromUserID, albumID uuid.UUID, albumName, category, titlePrefix, bodySuffix, eventType string) {
	from, err := s.Q.GetUserByID(ctx, pg.UUID(fromUserID))
	if err != nil {
		slog.ErrorContext(ctx, "notifications: loading sender profile", "error", err)
		return
	}
	title := titlePrefix
	body := from.DisplayName + bodySuffix + albumName
	data := map[string]any{
		"type":       eventType,
		"album_id":   albumID.String(),
		"from_user_id": fromUserID.String(),
	}
	if err := s.Enqueue(ctx, toUserID, category, title, body, data, nil); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue album event", "error", err)
		return
	}
	s.triggerFlush()
}

func (s *OutboxService) enqueueMemorySocialEvent(ctx context.Context, toUserID, fromUserID, memoryID uuid.UUID, category, titlePrefix, bodySuffix, eventType string) {
	from, err := s.Q.GetUserByID(ctx, pg.UUID(fromUserID))
	if err != nil {
		slog.ErrorContext(ctx, "notifications: loading sender profile", "error", err)
		return
	}
	title := titlePrefix
	body := from.DisplayName + bodySuffix
	data := map[string]any{
		"type":       eventType,
		"memory_id":  memoryID.String(),
		"from_user_id": fromUserID.String(),
	}
	if err := s.Enqueue(ctx, toUserID, category, title, body, data, nil); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue social event", "error", err)
		return
	}
	s.triggerFlush()
}

func (s *OutboxService) enqueueFriendEvent(ctx context.Context, toUserID, fromUserID uuid.UUID, titlePrefix, bodySuffix, eventType string) {
	from, err := s.Q.GetUserByID(ctx, pg.UUID(fromUserID))
	if err != nil {
		slog.ErrorContext(ctx, "notifications: loading sender profile", "error", err)
		return
	}
	title := titlePrefix
	body := from.DisplayName + bodySuffix
	data := map[string]any{
		"type":         eventType,
		"from_user_id": fromUserID.String(),
	}
	if err := s.Enqueue(ctx, toUserID, CategoryFriendRequests, title, body, data, nil); err != nil {
		slog.ErrorContext(ctx, "notifications: enqueue friend event", "error", err)
		return
	}
	s.triggerFlush()
}

func (s *OutboxService) prefEnabled(ctx context.Context, userID uuid.UUID, category string) (bool, error) {
	rows, err := s.Q.GetNotificationPrefs(ctx, pg.UUID(userID))
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.Category == category {
			return row.Enabled, nil
		}
	}
	return true, nil
}

// FlushInstant delivers due instant outbox rows (batch_key IS NULL).
func (s *OutboxService) FlushInstant(ctx context.Context) {
	rows, err := s.Q.ListDueInstantOutbox(ctx, flushBatchSize)
	if err != nil {
		slog.ErrorContext(ctx, "notifications: listing due outbox", "error", err)
		return
	}
	for _, row := range rows {
		s.flushOne(ctx, row)
	}
}

// FlushOnAppOpen advances pending unlock notifications and flushes due rows.
func (s *OutboxService) FlushOnAppOpen(ctx context.Context, userID uuid.UUID) {
	if err := s.Q.AdvanceUnlockNotificationsForUser(ctx, pg.UUID(userID)); err != nil {
		slog.ErrorContext(ctx, "notifications: advancing unlock notifications", "error", err)
	}
	s.FlushDueBatchesForUser(ctx, userID)
	s.FlushInstant(ctx)
}

func (s *OutboxService) flushOne(ctx context.Context, row dbgen.NotificationOutbox) {
	userID := pg.UUIDValue(row.UserID)
	enabled, err := s.prefEnabled(ctx, userID, row.Category)
	if err != nil {
		slog.ErrorContext(ctx, "notifications: loading prefs", "error", err, "user", userID)
		return
	}
	if !enabled {
		if err := s.Q.MarkOutboxSent(ctx, row.ID); err != nil {
			slog.ErrorContext(ctx, "notifications: marking skipped outbox sent", "error", err)
		}
		return
	}

	tokens, err := s.Q.ListPushTokensForUser(ctx, row.UserID)
	if err != nil {
		slog.ErrorContext(ctx, "notifications: loading push tokens", "error", err)
		return
	}

	var data map[string]any
	if len(row.Data) > 0 {
		_ = json.Unmarshal(row.Data, &data)
	}

	if len(tokens) == 0 {
		if err := s.Q.MarkOutboxSent(ctx, row.ID); err != nil {
			slog.ErrorContext(ctx, "notifications: marking outbox sent (no tokens)", "error", err)
		}
		return
	}

	msgs := make([]pushMessage, 0, len(tokens))
	for _, tok := range tokens {
		msgs = append(msgs, pushMessage{
			To:        tok.ExpoToken,
			Title:     row.Title,
			Body:      row.Body,
			Sound:     "default",
			ChannelID: "default",
			Priority:  "high",
			Data:      data,
		})
	}

	sendResult, err := s.Expo.Send(ctx, msgs)
	if err != nil {
		slog.ErrorContext(ctx, "notifications: expo send", "error", err)
		return
	}

	if err := s.Q.MarkOutboxSent(ctx, row.ID); err != nil {
		slog.ErrorContext(ctx, "notifications: marking outbox sent", "error", err)
		return
	}

	s.checkReceiptsAndPrune(ctx, userID, tokens, sendResult)
}

func (s *OutboxService) checkReceiptsAndPrune(ctx context.Context, userID uuid.UUID, tokens []dbgen.ListPushTokensForUserRow, sendResult SendResult) {
	ticketToToken := make(map[string]string, len(sendResult.TicketByToken))
	var ticketIDs []string
	for token, ticketID := range sendResult.TicketByToken {
		ticketToToken[ticketID] = token
		ticketIDs = append(ticketIDs, ticketID)
	}
	if len(ticketIDs) == 0 {
		return
	}

	deadTickets, err := s.Expo.CheckReceipts(ctx, ticketIDs)
	if err != nil {
		slog.ErrorContext(ctx, "notifications: checking receipts", "error", err)
		return
	}
	for _, ticketID := range deadTickets {
		expoToken, ok := ticketToToken[ticketID]
		if !ok {
			continue
		}
		if err := s.Q.DeletePushToken(ctx, dbgen.DeletePushTokenParams{
			UserID:    pg.UUID(userID),
			ExpoToken: expoToken,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			slog.ErrorContext(ctx, "notifications: pruning dead token", "error", err)
		}
	}
}
