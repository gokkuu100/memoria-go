// Package analytics is Memoria's first-party product analytics.
// Server Track() is non-blocking; client events are batched via POST /v1/analytics/events.
package analytics

// Canonical event names. Keep in sync with docs/ANALYTICS.md and the mobile allowlist.
const (
	EventSignupComplete          = "signup_complete"
	EventCapsuleCreated          = "capsule_created"
	EventCapsuleInviteSent       = "capsule_invite_sent"
	EventCapsuleInviteAccepted   = "capsule_invite_accepted"
	EventCapsuleMemoryCreated    = "capsule_memory_created"
	EventCapsuleFrozen           = "capsule_frozen"
	EventCapsuleUnlocked         = "capsule_unlocked"
	EventCapsuleDisintegrated    = "capsule_disintegrated"
	EventUnlockRevealCompleted   = "unlock_reveal_completed"
	EventMemoryReacted           = "memory_reacted"
	EventMemoryCommented         = "memory_commented"

	EventAppOpened               = "app_opened"
	EventCapsuleOpened           = "capsule_opened"
	EventMemoryViewed            = "memory_viewed"
	EventUnlockStorySlide        = "unlock_story_slide"
	EventUnlockStoryCompleted    = "unlock_story_completed"
	EventSubscriptionViewed      = "subscription_viewed"
)

const (
	SourceServer = "server"
	SourceClient = "client"
)

// Allowed client event names (server events are only emitted by the API).
var clientAllowed = map[string]struct{}{
	EventAppOpened:            {},
	EventCapsuleOpened:        {},
	EventMemoryViewed:         {},
	EventUnlockStorySlide:     {},
	EventUnlockStoryCompleted: {},
	EventSubscriptionViewed:   {},
	// Also accept a few server names if a client echoes them — harmless, filtered props.
	EventSignupComplete: {},
	EventCapsuleCreated: {},
}

// Allowed property keys — privacy: no captions, media URLs, emails, usernames.
var allowedProps = map[string]struct{}{
	"capsule_id":    {},
	"capsule_type":  {},
	"capsule_state": {},
	"slide":         {},
	"beat":          {},
	"is_own":        {},
	"plan":          {},
	"source":        {},
}

func IsClientEventAllowed(name string) bool {
	_, ok := clientAllowed[name]
	return ok
}

// SanitizeProps keeps only allowlisted keys with scalar values.
func SanitizeProps(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if _, ok := allowedProps[k]; !ok {
			continue
		}
		switch t := v.(type) {
		case string:
			if len(t) > 128 {
				t = t[:128]
			}
			out[k] = t
		case float64:
			out[k] = t
		case int:
			out[k] = t
		case int64:
			out[k] = t
		case bool:
			out[k] = t
		case nil:
			// skip
		default:
			// skip nested objects / arrays
		}
	}
	return out
}
