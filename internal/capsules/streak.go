package capsules

import (
	"context"
	"time"

	"github.com/google/uuid"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/pg"
)

// RecordContribution updates streak state after a new memory is added.
// contributedAt is when the memory was captured (not necessarily when it synced).
func RecordContribution(ctx context.Context, q *dbgen.Queries, capsuleID uuid.UUID, contributedAt time.Time) error {
	cid := pg.UUID(capsuleID)
	contribDay := dateUTC(contributedAt.UTC())

	if err := q.UpsertStreakDay(ctx, dbgen.UpsertStreakDayParams{
		CapsuleID: cid,
		Day:       pg.Date(contribDay),
	}); err != nil {
		return err
	}

	capsule, err := q.GetCapsuleByID(ctx, cid)
	if err != nil {
		return err
	}

	newLastContrib := contributedAt.UTC()
	if capsule.LastContributionAt.Valid && !contributedAt.After(capsule.LastContributionAt.Time) {
		newLastContrib = capsule.LastContributionAt.Time
	}

	streak := capsule.StreakCurrent
	if !capsule.LastContributionAt.Valid || !contributedAt.Before(capsule.LastContributionAt.Time) {
		streak = int32(1)
		yesterday := contribDay.AddDate(0, 0, -1)
		hadYesterday, err := q.HasStreakDay(ctx, dbgen.HasStreakDayParams{
			CapsuleID: cid,
			Day:       pg.Date(yesterday),
		})
		if err != nil {
			return err
		}
		if hadYesterday {
			streak = capsule.StreakCurrent + 1
		}
	}

	return q.UpdateCapsuleLastContribution(ctx, dbgen.UpdateCapsuleLastContributionParams{
		ID:                 cid,
		LastContributionAt: pg.Time(newLastContrib),
		StreakCurrent:      streak,
	})
}

func dateUTC(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
