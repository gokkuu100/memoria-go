package capsules

import (
	"context"
	"log/slog"
	"time"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/pg"
)

const unlockReminderLead = 10 * time.Minute

// ScheduleUnlockReminder enqueues a push for each accepted member at unlock_at − 10m.
// Idempotent via capsules.unlock_reminder_sent_at.
func ScheduleUnlockReminder(ctx context.Context, q *dbgen.Queries, notify notifications.Sender, cap dbgen.Capsule) {
	if notify == nil || cap.UnlockReminderSentAt.Valid {
		return
	}
	unlockAt := pg.TimeValue(cap.UnlockAt)
	reminderAt := unlockAt.Add(-unlockReminderLead)
	if !reminderAt.After(time.Now().UTC()) {
		return
	}

	members, err := q.ListCapsuleMemberUserIDs(ctx, cap.ID)
	if err != nil {
		slog.ErrorContext(ctx, "capsules: listing members for unlock reminder", "error", err)
		return
	}
	for _, uid := range members {
		notify.CapsuleUnlockSoon(ctx, pg.UUIDValue(uid), pg.UUIDValue(cap.ID), cap.Name, reminderAt)
	}
	if _, err := q.MarkUnlockReminderSent(ctx, cap.ID); err != nil {
		slog.ErrorContext(ctx, "capsules: marking unlock reminder sent", "error", err)
	}
}

// RunUnlockReminder sends reminders for capsules unlocking within 10 minutes (backup
// for capsules created before scheduling existed).
func RunUnlockReminder(ctx context.Context, q *dbgen.Queries, notify notifications.Sender) error {
	rows, err := q.ListCapsulesDueUnlockReminder(ctx)
	if err != nil {
		return err
	}
	for _, cap := range rows {
		ScheduleUnlockReminder(ctx, q, notify, cap)
	}
	return nil
}
