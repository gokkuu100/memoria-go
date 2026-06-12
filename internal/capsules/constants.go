package capsules

import (
	"time"
)

const inviteWindow = 48 * time.Hour

const (
	StatePending       = "pending"
	StateActive        = "active"
	StateFrozen        = "frozen"
	StateUnlocked      = "unlocked"
	StateArchived      = "archived"
	StateDisintegrated = "disintegrated"
)

const (
	TypeSolo  = "solo"
	TypeGroup = "group"
)

const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)
