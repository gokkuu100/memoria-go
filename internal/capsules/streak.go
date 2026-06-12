package capsules

import (
	"context"
	"time"

	"github.com/google/uuid"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/pg"
)

// RecordContribution updates streak state after a new memory is added.
func RecordContribution(ctx context.Context, q *dbgen.Queries, capsuleID uuid.UUID, now time.Time) error {
	cid := pg.UUID(capsuleID)
	today := dateUTC(now)
	yesterday := today.AddDate(0, 0, -1)

	if err := q.UpsertStreakDay(ctx, dbgen.UpsertStreakDayParams{
		CapsuleID: cid,
		Day:       pg.Date(today),
	}); err != nil {
		return err
	}

	capsule, err := q.GetCapsuleByID(ctx, cid)
	if err != nil {
		return err
	}

	streak := int32(1)
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

	return q.UpdateCapsuleLastContribution(ctx, dbgen.UpdateCapsuleLastContributionParams{
		ID:                 cid,
		LastContributionAt: pg.Time(now),
		StreakCurrent:      streak,
	})
}

func dateUTC(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
