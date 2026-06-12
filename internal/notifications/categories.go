package notifications

// Category constants match project.md notification settings (8 toggles).
const (
	CategoryCapsuleInvites    = "capsule_invites"
	CategoryAlbumInvites      = "album_invites"
	CategoryAlbumMemory       = "album_memory"
	CategoryCapsuleMemory     = "capsule_memory"
	CategoryReactionsComments = "reactions_comments"
	CategoryCapsuleUnlock     = "capsule_unlock"
	CategoryFriendRequests    = "friend_requests"
	CategorySystemAlerts      = "system_alerts"
)

// AllCategories is the canonical list returned by GET /v1/me/notification-prefs.
var AllCategories = []string{
	CategoryCapsuleInvites,
	CategoryAlbumInvites,
	CategoryAlbumMemory,
	CategoryCapsuleMemory,
	CategoryReactionsComments,
	CategoryCapsuleUnlock,
	CategoryFriendRequests,
	CategorySystemAlerts,
}

// IsInstant reports whether a category flushes immediately (no batch_key).
func IsInstant(category string) bool {
	switch category {
	case CategoryCapsuleMemory:
		return false
	default:
		return true
	}
}

func ValidCategory(category string) bool {
	for _, c := range AllCategories {
		if c == category {
			return true
		}
	}
	return false
}
