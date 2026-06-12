package capsules

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"memoria-backend/internal/billing"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/pg"
)

// ExpireInvites disintegrates pending capsules past invite_expires_at.
func ExpireInvites(ctx context.Context, q *dbgen.Queries, notify notifications.Sender) error {
	rows, err := q.ListPendingExpiredCapsules(ctx)
	if err != nil {
		return err
	}
	for _, cap := range rows {
		if _, err := q.MarkCapsuleDisintegrated(ctx, cap.ID); err != nil {
			slog.Error("capsules job: disintegrating expired invite", "capsule", pg.UUIDValue(cap.ID), "error", err)
			continue
		}
		if notify != nil {
			notify.CapsuleInviteExpired(ctx, pg.UUIDValue(cap.CreatorID), pg.UUIDValue(cap.ID), cap.Name)
		}
	}
	return nil
}

// RunFreezeWarning sends day-5 inactivity warnings.
func RunFreezeWarning(ctx context.Context, q *dbgen.Queries, notify notifications.Sender) error {
	rows, err := q.ListCapsulesNeedingFreezeWarning(ctx)
	if err != nil {
		return err
	}
	for _, cap := range rows {
		members, err := q.ListCapsuleMemberUserIDs(ctx, cap.ID)
		if err != nil {
			continue
		}
		for _, uid := range members {
			if notify != nil {
				notify.CapsuleFreezeWarning(ctx, pg.UUIDValue(uid), pg.UUIDValue(cap.ID), cap.Name)
			}
		}
		if err := q.MarkFreezeWarningSent(ctx, cap.ID); err != nil {
			slog.Error("capsules job: marking freeze warning sent", "capsule", pg.UUIDValue(cap.ID), "error", err)
		}
	}
	return nil
}

// RunFreeze freezes active capsules after 7 days without contribution.
func RunFreeze(ctx context.Context, q *dbgen.Queries, notify notifications.Sender) error {
	rows, err := q.ListCapsulesToFreeze(ctx)
	if err != nil {
		return err
	}
	for _, cap := range rows {
		if _, err := q.FreezeCapsule(ctx, cap.ID); err != nil {
			slog.Error("capsules job: freezing capsule", "capsule", pg.UUIDValue(cap.ID), "error", err)
			continue
		}
		members, err := q.ListCapsuleMemberUserIDs(ctx, cap.ID)
		if err != nil {
			continue
		}
		for _, uid := range members {
			if notify != nil {
				notify.CapsuleFrozen(ctx, pg.UUIDValue(uid), pg.UUIDValue(cap.ID), cap.Name)
			}
		}
	}
	return nil
}

// RunUnlock unlocks capsules whose unlock_at has passed.
func RunUnlock(ctx context.Context, q *dbgen.Queries, notify notifications.Sender) error {
	rows, err := q.ListCapsulesDueUnlock(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, row := range rows {
		plan := billing.ForUser(row.CreatorPlan)
		var viewableUntil pgtype.Timestamptz
		if vt := ViewableUntil(now, plan); vt != nil {
			viewableUntil = pg.Time(*vt)
		}

		wasFrozen := row.State == StateFrozen
		unlocked, err := q.UnlockCapsule(ctx, dbgen.UnlockCapsuleParams{
			ID:             row.ID,
			ViewableUntil:  viewableUntil,
		})
		if err != nil {
			slog.Error("capsules job: unlocking capsule", "capsule", pg.UUIDValue(row.ID), "error", err)
			continue
		}

		if wasFrozen {
			if err := q.SetAllNonAdminViewBlocked(ctx, row.ID); err != nil {
				slog.Error("capsules job: setting view_blocked", "capsule", pg.UUIDValue(row.ID), "error", err)
			}
		}

		members, err := q.ListCapsuleMembers(ctx, row.ID)
		if err != nil {
			continue
		}
		for _, m := range members {
			if m.InviteStatus != "accepted" {
				continue
			}
			u, err := q.GetUserByID(ctx, m.UserID)
			if err != nil {
				continue
			}
			deliverAfter := unlockDeliverAfter(now, u.Timezone)
			if notify != nil {
				notify.CapsuleUnlocked(ctx, pg.UUIDValue(m.UserID), pg.UUIDValue(row.ID), row.Name, deliverAfter)
			}
		}
		if notify != nil {
			widgetUsers := make([]uuid.UUID, 0, len(members))
			for _, m := range members {
				if m.InviteStatus == "accepted" {
					widgetUsers = append(widgetUsers, pg.UUIDValue(m.UserID))
				}
			}
			notify.RefreshWidgetForUsers(ctx, widgetUsers)
		}
		_ = unlocked
	}
	return nil
}

// unlockDeliverAfter returns 9am user-local on the unlock day (or next morning if past 9am).
func unlockDeliverAfter(now time.Time, timezone string) time.Time {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	local := now.In(loc)
	target := time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, loc)
	if local.After(target) {
		target = target.Add(24 * time.Hour)
	}
	return target.UTC()
}

// RunArchiveSweep archives unlocked capsules past viewable_until.
func RunArchiveSweep(ctx context.Context, q *dbgen.Queries) error {
	rows, err := q.ListArchivedCapsulesPastViewable(ctx)
	if err != nil {
		return err
	}
	for _, cap := range rows {
		if _, err := q.ArchiveCapsule(ctx, cap.ID); err != nil {
			slog.Error("capsules job: archiving capsule", "capsule", pg.UUIDValue(cap.ID), "error", err)
		}
	}
	return nil
}

// RestampViewableForCreator re-stamps viewable_until when creator upgrades plan (B8 hook).
func RestampViewableForCreator(ctx context.Context, q *dbgen.Queries, creatorID uuid.UUID, plan billing.Plan) error {
	caps, err := q.ListCapsulesOwnedByUser(ctx, pg.UUID(creatorID))
	if err != nil {
		return err
	}
	for _, cap := range caps {
		if cap.State != StateUnlocked {
			continue
		}
		if cap.ViewableUntil.Valid && time.Now().UTC().After(pg.TimeValue(cap.ViewableUntil)) {
			continue
		}
		unlockedAt := time.Now().UTC()
		if cap.UnlockedAt.Valid {
			unlockedAt = pg.TimeValue(cap.UnlockedAt)
		}
		var vt pgtype.Timestamptz
		if t := ViewableUntil(unlockedAt, plan); t != nil {
			vt = pg.Time(*t)
		}
		if err := q.RestampCapsuleViewableUntil(ctx, dbgen.RestampCapsuleViewableUntilParams{
			ID:            cap.ID,
			ViewableUntil: vt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// TransferAdminOnAccountDelete transfers admin for capsules owned by deleted user.
func TransferAdminOnAccountDelete(ctx context.Context, q *dbgen.Queries, userID uuid.UUID) error {
	caps, err := q.ListCapsulesOwnedByUser(ctx, pg.UUID(userID))
	if err != nil {
		return err
	}
	for _, cap := range caps {
		next, err := q.GetNextCapsuleAdminCandidate(ctx, dbgen.GetNextCapsuleAdminCandidateParams{
			CapsuleID: cap.ID,
			UserID:    pg.UUID(userID),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err := q.DemoteCapsuleAdmin(ctx, dbgen.DemoteCapsuleAdminParams{
			CapsuleID: cap.ID,
			UserID:    pg.UUID(userID),
		}); err != nil {
			return err
		}
		if err := q.TransferCapsuleAdmin(ctx, dbgen.TransferCapsuleAdminParams{
			CapsuleID: cap.ID,
			UserID:    next.UserID,
		}); err != nil {
			return err
		}
		if err := q.UpdateCapsuleCreator(ctx, dbgen.UpdateCapsuleCreatorParams{
			ID:        cap.ID,
			CreatorID: next.UserID,
		}); err != nil {
			return err
		}
	}
	return nil
}
