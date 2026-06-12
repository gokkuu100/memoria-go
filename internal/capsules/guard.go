package capsules

import (
	"time"

	"memoria-backend/internal/billing"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/pg"
)

// ViewableUntil computes post-unlock viewability from the creator's plan.
func ViewableUntil(unlockedAt time.Time, plan billing.Plan) *time.Time {
	if billing.IsUnlimited(plan.ViewabilityDays) {
		return nil
	}
	t := unlockedAt.Add(time.Duration(plan.ViewabilityDays) * 24 * time.Hour)
	return &t
}

// CanViewMemories reports whether a member may receive signed media URLs.
func CanViewMemories(c dbgen.Capsule, member dbgen.CapsuleMember, now time.Time) bool {
	if c.State != StateUnlocked {
		return false
	}
	if member.InviteStatus != "accepted" {
		return false
	}
	if member.ViewBlocked {
		return false
	}
	if c.ViewableUntil.Valid && now.After(pg.TimeValue(c.ViewableUntil)) {
		return false
	}
	return true
}

// IsSealed reports whether capsule memory contents must remain hidden.
func IsSealed(c dbgen.Capsule) bool {
	switch c.State {
	case StatePending, StateActive, StateFrozen:
		return true
	default:
		return false
	}
}
