// Package billing is the single source of truth for plan limits
// (SYSTEM_DESIGN §6). Every limit check anywhere in the API reads from this
// table — never scatter limit constants elsewhere.
package billing

// Plan holds every per-plan cap. Unlimited values are represented by the
// Unlimited sentinel; check with IsUnlimited before comparing counts.
type Plan struct {
	Name string

	// Capsules
	MaxActiveCapsules    int
	MaxCapsuleMembers    int
	MaxUnlockDistanceDays int
	MaxCapsuleMemories   int
	ViewabilityDays      int // post-unlock window; Unlimited = forever

	// Albums
	MaxActiveAlbums  int
	MaxAlbumMembers  int
	MaxAlbumPhotos   int
	AlbumLifespanDays int
	AlbumArchiveDays  int // retention after archive; Unlimited = forever

	// Media capture
	MaxVideoSeconds int
	VideoSound      bool
	MaxVoiceSeconds int

	// Upload byte caps (B2 decision: 10 MB photo / 60 MB video / 2 MB voice —
	// generous for in-app capture at the longest Pro durations).
	MaxPhotoBytes int64
	MaxVideoBytes int64
	MaxVoiceBytes int64

	// Profile stats heatmap history window (days); Unlimited = all time.
	HeatmapHistoryDays int
}

// Unlimited marks a count/day limit with no cap.
const Unlimited = -1

func IsUnlimited(v int) bool { return v == Unlimited }

const (
	mb = int64(1) << 20

	PlanSpark = "spark"
	PlanPlus  = "plus"
	PlanPro   = "pro"
)

var Plans = map[string]Plan{
	PlanSpark: {
		Name:                  PlanSpark,
		MaxActiveCapsules:     2,
		MaxCapsuleMembers:     5,
		MaxUnlockDistanceDays: 30,
		MaxCapsuleMemories:    50,
		ViewabilityDays:       30,
		MaxActiveAlbums:       1,
		MaxAlbumMembers:       2,
		MaxAlbumPhotos:        30,
		AlbumLifespanDays:     30,
		AlbumArchiveDays:      90,
		MaxVideoSeconds:       3,
		VideoSound:            true,
		MaxVoiceSeconds:       10,
		MaxPhotoBytes:         10 * mb,
		MaxVideoBytes:         60 * mb,
		MaxVoiceBytes:         2 * mb,
		HeatmapHistoryDays:    90,
	},
	PlanPlus: {
		Name:                  PlanPlus,
		MaxActiveCapsules:     5,
		MaxCapsuleMembers:     10,
		MaxUnlockDistanceDays: 60,
		MaxCapsuleMemories:    100,
		ViewabilityDays:       90,
		MaxActiveAlbums:       3,
		MaxAlbumMembers:       3,
		MaxAlbumPhotos:        75,
		AlbumLifespanDays:     90,
		AlbumArchiveDays:      365,
		MaxVideoSeconds:       20,
		VideoSound:            true,
		MaxVoiceSeconds:       20,
		MaxPhotoBytes:         10 * mb,
		MaxVideoBytes:         60 * mb,
		MaxVoiceBytes:         2 * mb,
		HeatmapHistoryDays:    Unlimited,
	},
	PlanPro: {
		Name:                  PlanPro,
		MaxActiveCapsules:     Unlimited,
		MaxCapsuleMembers:     20,
		MaxUnlockDistanceDays: 90,
		MaxCapsuleMemories:    200,
		ViewabilityDays:       Unlimited,
		MaxActiveAlbums:       Unlimited,
		MaxAlbumMembers:       5,
		MaxAlbumPhotos:        150,
		AlbumLifespanDays:     Unlimited,
		AlbumArchiveDays:      Unlimited,
		MaxVideoSeconds:       30,
		VideoSound:            true,
		MaxVoiceSeconds:       30,
		MaxPhotoBytes:         10 * mb,
		MaxVideoBytes:         60 * mb,
		MaxVoiceBytes:         2 * mb,
		HeatmapHistoryDays:    Unlimited,
	},
}

// ForUser returns the plan config for a users.plan value, falling back to
// Spark for anything unrecognized (defensive: plan strings only come from the
// RevenueCat webhook, but a bad value must never grant Pro limits).
func ForUser(plan string) Plan {
	if p, ok := Plans[plan]; ok {
		return p
	}
	return Plans[PlanSpark]
}
