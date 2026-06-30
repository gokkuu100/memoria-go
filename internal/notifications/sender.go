// Package notifications defines the push-notification sender interface and
// outbox-based delivery via Expo Push.
package notifications

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Sender emits instant notification events. Implementations must be safe for
// concurrent use.
type Sender interface {
	FriendRequestReceived(ctx context.Context, toUserID, fromUserID uuid.UUID)
	FriendRequestAccepted(ctx context.Context, toUserID, fromUserID uuid.UUID)
	AlbumInviteReceived(ctx context.Context, toUserID, fromUserID, albumID uuid.UUID, albumName string)
	AlbumInviteExpired(ctx context.Context, toUserID, albumID uuid.UUID, albumName string)
	AlbumMemoryAdded(ctx context.Context, toUserID, fromUserID, albumID uuid.UUID, albumName string)
	AlbumMemberLeft(ctx context.Context, toUserID, fromUserID, albumID uuid.UUID, albumName string)
	AlbumDeleted(ctx context.Context, toUserID, fromUserID, albumID uuid.UUID, albumName string)
	RefreshWidgetForUsers(ctx context.Context, userIDs []uuid.UUID)
	CapsuleMemoryAdded(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string)
	CommentOnMemory(ctx context.Context, toUserID, fromUserID, memoryID uuid.UUID)
	ReactionOnMemory(ctx context.Context, toUserID, fromUserID, memoryID uuid.UUID)
	CapsuleInviteReceived(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string)
	CapsuleInviteDeclined(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string)
	CapsuleInviteExpired(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string)
	CapsuleActivated(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string)
	CapsuleFreezeWarning(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string)
	CapsuleFrozen(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string)
	CapsuleUnfreezeVote(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string)
	CapsuleUnlocked(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string, deliverAfter time.Time)
	CapsuleMemberLeft(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string)
	CapsuleDeleted(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string)
}

// LogSender is the dev no-op: events are logged instead of enqueued.
type LogSender struct{}

func (LogSender) FriendRequestReceived(ctx context.Context, toUserID, fromUserID uuid.UUID) {
	slog.InfoContext(ctx, "notification: friend_request_received",
		"to", toUserID, "from", fromUserID)
}

func (LogSender) FriendRequestAccepted(ctx context.Context, toUserID, fromUserID uuid.UUID) {
	slog.InfoContext(ctx, "notification: friend_request_accepted",
		"to", toUserID, "from", fromUserID)
}

func (LogSender) AlbumInviteReceived(ctx context.Context, toUserID, fromUserID, albumID uuid.UUID, albumName string) {
	slog.InfoContext(ctx, "notification: album_invite_received",
		"to", toUserID, "from", fromUserID, "album", albumID, "name", albumName)
}

func (LogSender) AlbumInviteExpired(ctx context.Context, toUserID, albumID uuid.UUID, albumName string) {
	slog.InfoContext(ctx, "notification: album_invite_expired",
		"to", toUserID, "album", albumID, "name", albumName)
}

func (LogSender) AlbumMemoryAdded(ctx context.Context, toUserID, fromUserID, albumID uuid.UUID, albumName string) {
	slog.InfoContext(ctx, "notification: album_memory_added",
		"to", toUserID, "from", fromUserID, "album", albumID, "name", albumName)
}

func (LogSender) AlbumMemberLeft(ctx context.Context, toUserID, fromUserID, albumID uuid.UUID, albumName string) {
	slog.InfoContext(ctx, "notification: album_member_left",
		"to", toUserID, "from", fromUserID, "album", albumID, "name", albumName)
}

func (LogSender) AlbumDeleted(ctx context.Context, toUserID, fromUserID, albumID uuid.UUID, albumName string) {
	slog.InfoContext(ctx, "notification: album_deleted",
		"to", toUserID, "from", fromUserID, "album", albumID, "name", albumName)
}

func (LogSender) RefreshWidgetForUsers(ctx context.Context, userIDs []uuid.UUID) {
	slog.InfoContext(ctx, "notification: widget_refresh", "users", userIDs)
}

func (LogSender) CapsuleMemoryAdded(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string) {
	slog.InfoContext(ctx, "notification: capsule_memory_added",
		"to", toUserID, "from", fromUserID, "capsule", capsuleID, "name", capsuleName)
}

func (LogSender) CommentOnMemory(ctx context.Context, toUserID, fromUserID, memoryID uuid.UUID) {
	slog.InfoContext(ctx, "notification: comment_on_memory",
		"to", toUserID, "from", fromUserID, "memory", memoryID)
}

func (LogSender) ReactionOnMemory(ctx context.Context, toUserID, fromUserID, memoryID uuid.UUID) {
	slog.InfoContext(ctx, "notification: reaction_on_memory",
		"to", toUserID, "from", fromUserID, "memory", memoryID)
}

func (LogSender) CapsuleInviteReceived(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string) {
	slog.InfoContext(ctx, "notification: capsule_invite_received",
		"to", toUserID, "from", fromUserID, "capsule", capsuleID, "name", capsuleName)
}

func (LogSender) CapsuleInviteDeclined(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string) {
	slog.InfoContext(ctx, "notification: capsule_invite_declined",
		"to", toUserID, "from", fromUserID, "capsule", capsuleID, "name", capsuleName)
}

func (LogSender) CapsuleInviteExpired(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string) {
	slog.InfoContext(ctx, "notification: capsule_invite_expired",
		"to", toUserID, "capsule", capsuleID, "name", capsuleName)
}

func (LogSender) CapsuleActivated(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string) {
	slog.InfoContext(ctx, "notification: capsule_activated",
		"to", toUserID, "capsule", capsuleID, "name", capsuleName)
}

func (LogSender) CapsuleFreezeWarning(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string) {
	slog.InfoContext(ctx, "notification: capsule_freeze_warning",
		"to", toUserID, "capsule", capsuleID, "name", capsuleName)
}

func (LogSender) CapsuleFrozen(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string) {
	slog.InfoContext(ctx, "notification: capsule_frozen",
		"to", toUserID, "capsule", capsuleID, "name", capsuleName)
}

func (LogSender) CapsuleUnfreezeVote(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string) {
	slog.InfoContext(ctx, "notification: capsule_unfreeze_vote",
		"to", toUserID, "from", fromUserID, "capsule", capsuleID, "name", capsuleName)
}

func (LogSender) CapsuleUnlocked(ctx context.Context, toUserID, capsuleID uuid.UUID, capsuleName string, deliverAfter time.Time) {
	slog.InfoContext(ctx, "notification: capsule_unlocked",
		"to", toUserID, "capsule", capsuleID, "name", capsuleName, "deliver_after", deliverAfter)
}

func (LogSender) CapsuleMemberLeft(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string) {
	slog.InfoContext(ctx, "notification: capsule_member_left",
		"to", toUserID, "from", fromUserID, "capsule", capsuleID, "name", capsuleName)
}

func (LogSender) CapsuleDeleted(ctx context.Context, toUserID, fromUserID, capsuleID uuid.UUID, capsuleName string) {
	slog.InfoContext(ctx, "notification: capsule_deleted",
		"to", toUserID, "from", fromUserID, "capsule", capsuleID, "name", capsuleName)
}
