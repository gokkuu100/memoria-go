package notifications

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/pg"
)

// RefreshWidgetForUsers sends throttled silent (content-available) pushes so
// clients reload GET /v1/widget/feed in the background.
func (s *OutboxService) RefreshWidgetForUsers(ctx context.Context, userIDs []uuid.UUID) {
	for _, uid := range userIDs {
		s.refreshWidgetForUser(ctx, uid)
	}
}

func (s *OutboxService) refreshWidgetForUser(ctx context.Context, userID uuid.UUID) {
	tokens, err := s.Q.ListWidgetPushEligibleTokens(ctx, pg.UUID(userID))
	if err != nil {
		slog.ErrorContext(ctx, "notifications: listing widget push tokens", "error", err, "user", userID)
		return
	}
	if len(tokens) == 0 {
		return
	}

	contentAvailable := true
	msgs := make([]pushMessage, 0, len(tokens))
	for _, expoToken := range tokens {
		msgs = append(msgs, pushMessage{
			To:               expoToken,
			ContentAvailable: &contentAvailable,
			Priority:         "high",
			Data:             map[string]any{"type": "widget_refresh"},
		})
	}

	if _, err := s.Expo.Send(ctx, msgs); err != nil {
		slog.ErrorContext(ctx, "notifications: widget silent push", "error", err, "user", userID)
		return
	}

	if err := s.Q.TouchWidgetPushAt(ctx, dbgen.TouchWidgetPushAtParams{
		UserID:  pg.UUID(userID),
		Column2: tokens,
	}); err != nil {
		slog.ErrorContext(ctx, "notifications: recording widget push time", "error", err, "user", userID)
	}
}
