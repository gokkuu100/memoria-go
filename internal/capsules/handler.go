package capsules

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"memoria-backend/internal/billing"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/friends"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/media"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/pg"
	"memoria-backend/internal/users"
)

type Handler struct {
	Pool   *pgxpool.Pool
	Q      *dbgen.Queries
	Media  *media.Store
	Notify notifications.Sender
}

func (h *Handler) Mount(r chi.Router) {
	r.Post("/capsules", h.create)
	r.Get("/capsules", h.list)
	r.Get("/capsules/{id}", h.get)
	r.Post("/capsules/{id}/accept", h.accept)
	r.Post("/capsules/{id}/decline", h.decline)
	r.Post("/capsules/{id}/members", h.inviteMember)
	r.Delete("/capsules/{id}/members/{userId}", h.removeMember)
	r.Post("/capsules/{id}/memories", h.addMemory)
	r.Get("/capsules/{id}/memories", h.listMemories)
	r.Get("/capsules/{id}/memories/{memoryId}/media", h.streamMemoryMedia)
	r.Get("/capsules/{id}/memories/{memoryId}/voice", h.streamMemoryVoice)
	r.Post("/capsules/{id}/unfreeze-vote", h.unfreezeVote)
	r.Post("/capsules/{id}/unblock/{userId}", h.unblockMember)
	r.Get("/capsules/{id}/stats", h.stats)
	r.Post("/capsules/{id}/unlock-reveal/seen", h.markUnlockRevealSeen)
	r.Post("/capsules/{id}/leave", h.leave)
	r.Delete("/capsules/{id}", h.deleteCapsule)
}

type memberDTO struct {
	ID                 string     `json:"id"`
	Username           string     `json:"username"`
	DisplayName        string     `json:"display_name"`
	AvatarURL          *string    `json:"avatar_url,omitempty"`
	Role               string     `json:"role"`
	InviteStatus       string     `json:"invite_status"`
	AcceptedAt         *time.Time `json:"accepted_at,omitempty"`
	ViewBlocked        bool       `json:"view_blocked"`
	UnlockRevealSeen   bool       `json:"unlock_reveal_seen"`
	MemoryCount        int64      `json:"memory_count"`
	LastContributionAt *time.Time `json:"last_contribution_at,omitempty"`
}

type capsuleDetailDTO struct {
	ID                   string      `json:"id"`
	Name                 string      `json:"name"`
	Description          *string     `json:"description,omitempty"`
	Type                 string      `json:"type"`
	State                string      `json:"state"`
	CreatorID            string      `json:"creator_id"`
	UnlockAt             time.Time   `json:"unlock_at"`
	InviteExpiresAt      *time.Time  `json:"invite_expires_at,omitempty"`
	FrozenAt             *time.Time  `json:"frozen_at,omitempty"`
	UnlockedAt           *time.Time  `json:"unlocked_at,omitempty"`
	ViewableUntil        *time.Time  `json:"viewable_until,omitempty"`
	UnlockSecondsRemaining *int64    `json:"unlock_seconds_remaining,omitempty"`
	MemoryCount          int64       `json:"memory_count"`
	StreakCurrent        int32       `json:"streak_current"`
	StreakPerfect        bool        `json:"streak_perfect"`
	LastContributionAt   *time.Time  `json:"last_contribution_at,omitempty"`
	Members              []memberDTO `json:"members"`
	Sealed               bool        `json:"sealed"`
}

type capsuleSummaryDTO struct {
	ID                     string     `json:"id"`
	Name                   string     `json:"name"`
	Description            *string    `json:"description,omitempty"`
	Type                   string     `json:"type"`
	State                  string     `json:"state"`
	CreatorID              string     `json:"creator_id"`
	UnlockAt               time.Time  `json:"unlock_at"`
	MemoryCount            int64      `json:"memory_count"`
	MemberCount            int64      `json:"member_count"`
	StreakCurrent          int32      `json:"streak_current"`
	StreakPerfect          bool       `json:"streak_perfect"`
	UnlockSecondsRemaining *int64     `json:"unlock_seconds_remaining,omitempty"`
	FrozenAt               *time.Time `json:"frozen_at,omitempty"`
	UnlockedAt             *time.Time `json:"unlocked_at,omitempty"`
}

type memoryDTO struct {
	ID            string            `json:"id"`
	Author        users.ProfileCard `json:"author"`
	MediaURL      string            `json:"media_url"`
	MediaKind     string            `json:"media_kind"`
	VoiceURL      *string           `json:"voice_url,omitempty"`
	Caption       *string           `json:"caption,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	ReactionCount int64             `json:"reaction_count"`
	ReactionEmojis []string         `json:"reaction_emojis,omitempty"`
	CommentCount  int64             `json:"comment_count"`
}

// POST /v1/capsules
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	var req struct {
		Name            string   `json:"name"`
		Description     *string  `json:"description"`
		Type            string   `json:"type"`
		UnlockAt        string   `json:"unlock_at"`
		InvitedUserIDs  []string `json:"invited_user_ids"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	if req.Name == "" || len(req.Name) > httpx.MaxNameLen {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "name must be 1-100 characters")
		return
	}
	if req.Description != nil && len(*req.Description) > 200 {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "description must be at most 200 characters")
		return
	}
	if req.Type != TypeSolo && req.Type != TypeGroup {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "type must be solo or group")
		return
	}
	unlockAt, err := time.Parse(time.RFC3339, req.UnlockAt)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "unlock_at must be RFC3339")
		return
	}
	unlockAt = unlockAt.UTC()
	if !unlockAt.After(time.Now().UTC()) {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "unlock_at must be in the future")
		return
	}

	creator, err := h.Q.GetUserByID(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	plan := billing.ForUser(creator.Plan)

	maxDist := time.Duration(plan.MaxUnlockDistanceDays) * 24 * time.Hour
	if unlockAt.Sub(time.Now().UTC()) > maxDist {
		httpx.Error(w, http.StatusForbidden, "unlock_date_too_far",
			fmt.Sprintf("unlock date must be within %d days on your plan", plan.MaxUnlockDistanceDays))
		return
	}

	activeCount, err := h.Q.CountActiveCapsulesForUser(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if !billing.IsUnlimited(plan.MaxActiveCapsules) && int(activeCount) >= plan.MaxActiveCapsules {
		httpx.Error(w, http.StatusForbidden, "capsule_limit_reached",
			fmt.Sprintf("your plan allows %d active capsule(s)", plan.MaxActiveCapsules))
		return
	}

	inviteeIDs := make([]uuid.UUID, 0, len(req.InvitedUserIDs))
	if req.Type == TypeSolo {
		if len(req.InvitedUserIDs) > 0 {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "solo capsules cannot have invites")
			return
		}
	} else {
		if len(req.InvitedUserIDs) == 0 {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "group capsules require at least one invite")
			return
		}
		totalMembers := 1 + len(req.InvitedUserIDs)
		if totalMembers > plan.MaxCapsuleMembers {
			httpx.Error(w, http.StatusForbidden, "capsule_member_limit",
				fmt.Sprintf("your plan allows %d members per capsule", plan.MaxCapsuleMembers))
			return
		}
		seen := map[uuid.UUID]bool{userID: true}
		for _, raw := range req.InvitedUserIDs {
			id, err := uuid.Parse(raw)
			if err != nil {
				httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "invited_user_ids must be valid UUIDs")
				return
			}
			if seen[id] {
				httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "duplicate invite")
				return
			}
			seen[id] = true
			if !friends.AssertNotBlocked(w, r.Context(), h.Q, userID, id) {
				return
			}
			ok, err := h.Q.AreFriends(r.Context(), dbgen.AreFriendsParams{
				UserA: pg.UUID(userID), UserB: pg.UUID(id),
			})
			if err != nil {
				httpx.InternalError(w, err)
				return
			}
			if !ok {
				httpx.Error(w, http.StatusBadRequest, "not_connected", "invited users must be accepted friends")
				return
			}
			inviteeIDs = append(inviteeIDs, id)
		}
	}

	state := StatePending
	var inviteExpires pgtype.Timestamptz
	if req.Type == TypeSolo {
		state = StateActive
	} else {
		inviteExpires = pg.Time(time.Now().UTC().Add(inviteWindow))
	}

	cap, err := h.Q.CreateCapsule(r.Context(), dbgen.CreateCapsuleParams{
		CreatorID:       pg.UUID(userID),
		Name:            req.Name,
		Description:     req.Description,
		Type:            req.Type,
		State:           state,
		UnlockAt:        pg.Time(unlockAt),
		InviteExpiresAt: inviteExpires,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	now := pg.Time(time.Now().UTC())
	if err := h.Q.AddCapsuleMember(r.Context(), dbgen.AddCapsuleMemberParams{
		CapsuleID: cap.ID, UserID: pg.UUID(userID), Role: RoleAdmin,
		InviteStatus: "accepted", AcceptedAt: now,
	}); err != nil {
		httpx.InternalError(w, err)
		return
	}

	for _, id := range inviteeIDs {
		if err := h.Q.AddCapsuleMember(r.Context(), dbgen.AddCapsuleMemberParams{
			CapsuleID: cap.ID, UserID: pg.UUID(id), Role: RoleMember, InviteStatus: "pending",
		}); err != nil {
			httpx.InternalError(w, err)
			return
		}
		if h.Notify != nil {
			h.Notify.CapsuleInviteReceived(r.Context(), id, userID, pg.UUIDValue(cap.ID), cap.Name)
		}
	}

	resp := map[string]any{
		"id":         pg.UUIDValue(cap.ID).String(),
		"name":       cap.Name,
		"type":       cap.Type,
		"state":      cap.State,
		"unlock_at":  pg.TimeValue(cap.UnlockAt),
	}
	if inviteExpires.Valid {
		resp["invite_expires_at"] = pg.TimeValue(inviteExpires)
	}
	if cap.State == StateActive {
		ScheduleUnlockReminder(r.Context(), h.Q, h.Notify, cap)
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

func (h *Handler) accept(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}
	if cap.State != StatePending {
		httpx.Error(w, http.StatusConflict, "capsule_not_pending", "capsule is not awaiting acceptance")
		return
	}
	if member.InviteStatus != "pending" {
		httpx.Error(w, http.StatusConflict, "invite_not_pending", "no pending invite for you")
		return
	}

	if _, err := h.Q.AcceptCapsuleInvite(r.Context(), dbgen.AcceptCapsuleInviteParams{
		CapsuleID: pg.UUID(capID), UserID: pg.UUID(userID),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusConflict, "invite_not_pending", "no pending invite for you")
			return
		}
		httpx.InternalError(w, err)
		return
	}

	pending, err := h.Q.CountPendingCapsuleInvites(r.Context(), pg.UUID(capID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if pending == 0 {
		if _, err := h.Q.ActivateCapsule(r.Context(), pg.UUID(capID)); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			httpx.InternalError(w, err)
			return
		}
		members, _ := h.Q.ListCapsuleMemberUserIDs(r.Context(), pg.UUID(capID))
		if h.Notify != nil {
			for _, uid := range members {
				h.Notify.CapsuleActivated(r.Context(), pg.UUIDValue(uid), capID, cap.Name)
			}
		}
		if updated, err := h.Q.GetCapsuleByID(r.Context(), pg.UUID(capID)); err == nil {
			ScheduleUnlockReminder(r.Context(), h.Q, h.Notify, updated)
		}
	}

	httpx.JSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (h *Handler) decline(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}
	if cap.State != StatePending {
		httpx.Error(w, http.StatusConflict, "capsule_not_pending", "capsule is not awaiting acceptance")
		return
	}
	if member.InviteStatus != "pending" {
		httpx.Error(w, http.StatusConflict, "invite_not_pending", "no pending invite for you")
		return
	}

	if _, err := h.Q.DeclineCapsuleInvite(r.Context(), dbgen.DeclineCapsuleInviteParams{
		CapsuleID: pg.UUID(capID), UserID: pg.UUID(userID),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusConflict, "invite_not_pending", "no pending invite for you")
			return
		}
		httpx.InternalError(w, err)
		return
	}

	if h.Notify != nil {
		h.Notify.CapsuleInviteDeclined(r.Context(), pg.UUIDValue(cap.CreatorID), userID, capID, cap.Name)
	}

	httpx.JSON(w, http.StatusOK, map[string]string{"status": "declined"})
}

func (h *Handler) inviteMember(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}
	if member.Role != RoleAdmin {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "admin only")
		return
	}
	if cap.State != StatePending {
		httpx.Error(w, http.StatusConflict, "capsule_not_pending", "can only reinvite while pending")
		return
	}
	if !cap.InviteExpiresAt.Valid || time.Now().UTC().After(pg.TimeValue(cap.InviteExpiresAt)) {
		httpx.Error(w, http.StatusConflict, "invite_window_closed", "invite window has expired")
		return
	}

	var req struct {
		UserID string `json:"user_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	inviteeID, err := uuid.Parse(req.UserID)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "user_id must be a valid UUID")
		return
	}
	if inviteeID == userID {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "cannot invite yourself")
		return
	}
	if !friends.AssertNotBlocked(w, r.Context(), h.Q, userID, inviteeID) {
		return
	}
	okFriends, err := h.Q.AreFriends(r.Context(), dbgen.AreFriendsParams{
		UserA: pg.UUID(userID), UserB: pg.UUID(inviteeID),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if !okFriends {
		httpx.Error(w, http.StatusBadRequest, "not_connected", "invited user must be an accepted friend")
		return
	}

	creator, err := h.Q.GetUserByID(r.Context(), cap.CreatorID)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	plan := billing.ForUser(creator.Plan)
	count, err := h.Q.CountAcceptedCapsuleMembers(r.Context(), pg.UUID(capID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	pending, err := h.Q.CountPendingCapsuleInvites(r.Context(), pg.UUID(capID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if int(count)+int(pending)+1 > plan.MaxCapsuleMembers {
		httpx.Error(w, http.StatusForbidden, "capsule_member_limit",
			fmt.Sprintf("your plan allows %d members per capsule", plan.MaxCapsuleMembers))
		return
	}

	memberRow, err := h.Q.GetCapsuleMember(r.Context(), dbgen.GetCapsuleMemberParams{
		CapsuleID: pg.UUID(capID), UserID: pg.UUID(inviteeID),
	})
	if err == nil {
		if memberRow.InviteStatus == "accepted" {
			httpx.Error(w, http.StatusConflict, "already_member", "user is already in this capsule")
			return
		}
		_, err := h.Pool.Exec(r.Context(), `UPDATE capsule_members SET invite_status = 'pending' WHERE capsule_id = $1 AND user_id = $2`, pg.UUID(capID), pg.UUID(inviteeID))
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		if h.Notify != nil {
			h.Notify.CapsuleInviteReceived(r.Context(), inviteeID, userID, capID, cap.Name)
		}
		httpx.JSON(w, http.StatusCreated, map[string]string{"status": "invited"})
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		httpx.InternalError(w, err)
		return
	}

	if err := h.Q.AddCapsuleMember(r.Context(), dbgen.AddCapsuleMemberParams{
		CapsuleID: pg.UUID(capID), UserID: pg.UUID(inviteeID), Role: RoleMember, InviteStatus: "pending",
	}); err != nil {
		httpx.InternalError(w, err)
		return
	}
	if h.Notify != nil {
		h.Notify.CapsuleInviteReceived(r.Context(), inviteeID, userID, capID, cap.Name)
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"status": "invited"})
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "member not found")
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}
	if member.Role != RoleAdmin {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "admin only")
		return
	}
	if targetID == userID {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "use leave to exit the capsule")
		return
	}

	target, err := h.Q.GetCapsuleMember(r.Context(), dbgen.GetCapsuleMemberParams{
		CapsuleID: pg.UUID(capID), UserID: pg.UUID(targetID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "member not found")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if cap.State == StatePending && target.InviteStatus != "declined" && target.InviteStatus != "pending" {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "can only remove declined or pending members while pending")
		return
	}

	if err := h.Q.RemoveCapsuleMember(r.Context(), dbgen.RemoveCapsuleMemberParams{
		CapsuleID: pg.UUID(capID), UserID: pg.UUID(targetID),
	}); err != nil {
		httpx.InternalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	dto, ok := h.buildCapsuleDetail(w, r, capID, userID)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, dto)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	filter := r.URL.Query().Get("filter")
	if filter != "ongoing" && filter != "frozen" && filter != "completed" && filter != "invites" {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "filter must be ongoing, frozen, completed, or invites")
		return
	}
	rows, err := h.Q.ListCapsulesForUser(r.Context(), dbgen.ListCapsulesForUserParams{
		UserID: pg.UUID(userID), Filter: filter,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	now := time.Now().UTC()
	out := make([]capsuleSummaryDTO, 0, len(rows))
	for _, c := range rows {
		cnt, err := h.Q.CountCapsuleMemories(r.Context(), c.ID)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		memberCnt, err := h.Q.CountCapsuleAcceptedMembers(r.Context(), c.ID)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		var unlockRemaining *int64
		if c.State != StateUnlocked && c.State != StateArchived && c.State != StateDisintegrated {
			if unlockAt := pg.TimeValue(c.UnlockAt); unlockAt.After(now) {
				sec := int64(unlockAt.Sub(now).Seconds())
				unlockRemaining = &sec
			}
		}
		var desc *string
		if c.Description != nil && *c.Description != "" {
			desc = c.Description
		}
		var frozenAt, unlockedAt *time.Time
		if c.FrozenAt.Valid {
			t := pg.TimeValue(c.FrozenAt)
			frozenAt = &t
		}
		if c.UnlockedAt.Valid {
			t := pg.TimeValue(c.UnlockedAt)
			unlockedAt = &t
		}
		out = append(out, capsuleSummaryDTO{
			ID:                     pg.UUIDValue(c.ID).String(),
			Name:                   c.Name,
			Description:            desc,
			Type:                   c.Type,
			State:                  c.State,
			CreatorID:              pg.UUIDValue(c.CreatorID).String(),
			UnlockAt:               pg.TimeValue(c.UnlockAt),
			MemoryCount:            cnt,
			MemberCount:            memberCnt,
			StreakCurrent:          c.StreakCurrent,
			StreakPerfect:          c.StreakPerfect,
			UnlockSecondsRemaining: unlockRemaining,
			FrozenAt:               frozenAt,
			UnlockedAt:             unlockedAt,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"capsules": out})
}

func (h *Handler) addMemory(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}
	if cap.State != StateActive {
		httpx.Error(w, http.StatusForbidden, "capsule_not_active", "capsule is not accepting contributions")
		return
	}
	if member.InviteStatus != "accepted" {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "not an active member")
		return
	}

	now := time.Now().UTC()
	unlockAt := pg.TimeValue(cap.UnlockAt)
	if !now.Before(unlockAt) {
		httpx.Error(w, http.StatusForbidden, "queue_sync_deadline_passed",
			"capsule unlock has passed; memories can no longer be added")
		return
	}

	var req struct {
		MediaID      string  `json:"media_id"`
		VoiceMediaID *string `json:"voice_media_id"`
		Caption      *string `json:"caption"`
		CapturedAt   *string `json:"captured_at"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	mediaID, err := uuid.Parse(req.MediaID)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "media_id must be a valid UUID")
		return
	}

	capturedAt := now
	if req.CapturedAt != nil && *req.CapturedAt != "" {
		parsed, err := parseRFC3339(*req.CapturedAt)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "captured_at must be RFC3339")
			return
		}
		capturedAt = parsed.UTC()
	}
	if !capturedAt.Before(unlockAt) {
		httpx.Error(w, http.StatusBadRequest, "queue_capture_outside_window",
			"memory was captured after the capsule contribution window closed")
		return
	}
	if capturedAt.After(now.Add(5 * time.Minute)) {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "captured_at cannot be in the future")
		return
	}
	if capturedAt.Before(pg.TimeValue(cap.CreatedAt)) {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "captured_at cannot be before the capsule was created")
		return
	}
	if member.AcceptedAt.Valid && capturedAt.Before(pg.TimeValue(member.AcceptedAt)) {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "captured_at cannot be before you joined the capsule")
		return
	}

	var idempotencyKey uuid.UUID
	if rawKey := r.Header.Get("Idempotency-Key"); rawKey != "" {
		idempotencyKey, err = uuid.Parse(rawKey)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "Idempotency-Key must be a valid UUID")
			return
		}
		existing, err := h.Q.GetMemoryIdempotency(r.Context(), dbgen.GetMemoryIdempotencyParams{
			UserID:         pg.UUID(userID),
			IdempotencyKey: pg.UUID(idempotencyKey),
		})
		if err == nil {
			if pg.UUIDValue(existing.CapsuleID) != capID {
				httpx.Error(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used for a different capsule")
				return
			}
			mem, err := h.Q.GetMemoryByID(r.Context(), existing.MemoryID)
			if err != nil {
				httpx.InternalError(w, err)
				return
			}
			h.writeMemoryCreated(w, mem, http.StatusOK)
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			httpx.InternalError(w, err)
			return
		}
	}

	creator, err := h.Q.GetUserByID(r.Context(), cap.CreatorID)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	plan := billing.ForUser(creator.Plan)

	count, err := h.Q.CountCapsuleMemories(r.Context(), pg.UUID(capID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if !billing.IsUnlimited(plan.MaxCapsuleMemories) && int(count) >= plan.MaxCapsuleMemories {
		httpx.Error(w, http.StatusForbidden, "capsule_memory_limit_reached",
			fmt.Sprintf("capsules are limited to %d memories on the creator's plan", plan.MaxCapsuleMemories))
		return
	}

	mediaRow, ok := h.validateReadyMedia(w, r, userID, mediaID, false)
	if !ok {
		return
	}
	if mediaRow.Kind != "photo" && mediaRow.Kind != "video" {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "memory media must be photo or video")
		return
	}

	var voiceID pgtype.UUID
	if req.VoiceMediaID != nil {
		vid, err := uuid.Parse(*req.VoiceMediaID)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "voice_media_id must be a valid UUID")
			return
		}
		if _, ok := h.validateReadyMedia(w, r, userID, vid, true); !ok {
			return
		}
		voiceID = pg.UUID(vid)
	}
	if req.Caption != nil && len(*req.Caption) > httpx.MaxCaptionLen {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "caption must be at most 500 characters")
		return
	}

	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	qtx := h.Q.WithTx(tx)

	mem, err := qtx.CreateMemory(r.Context(), dbgen.CreateMemoryParams{
		ContainerType: "capsule",
		ContainerID:   pg.UUID(capID),
		AuthorID:      pg.UUID(userID),
		MediaID:       pg.UUID(mediaID),
		VoiceMediaID:  voiceID,
		Caption:       req.Caption,
		CapturedAt:    pg.Time(capturedAt),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	if idempotencyKey != uuid.Nil {
		if _, err := qtx.CreateMemoryIdempotency(r.Context(), dbgen.CreateMemoryIdempotencyParams{
			UserID:         pg.UUID(userID),
			IdempotencyKey: pg.UUID(idempotencyKey),
			CapsuleID:      pg.UUID(capID),
			MemoryID:       mem.ID,
		}); err != nil {
			httpx.InternalError(w, err)
			return
		}
	}

	if err := RecordContribution(r.Context(), qtx, capID, capturedAt); err != nil {
		httpx.InternalError(w, err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		httpx.InternalError(w, err)
		return
	}

	members, err := h.Q.ListCapsuleMemberUserIDs(r.Context(), pg.UUID(capID))
	if err == nil && h.Notify != nil {
		for _, uid := range members {
			mid := pg.UUIDValue(uid)
			if mid == userID {
				continue
			}
			h.Notify.CapsuleMemoryAdded(r.Context(), mid, userID, capID, cap.Name)
		}
	}

	h.writeMemoryCreated(w, mem, http.StatusCreated)
}

func parseRFC3339(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time")
}

func (h *Handler) writeMemoryCreated(w http.ResponseWriter, mem dbgen.Memory, status int) {
	httpx.JSON(w, status, map[string]any{
		"id":          pg.UUIDValue(mem.ID).String(),
		"created_at":  pg.TimeValue(mem.CreatedAt),
		"captured_at": pg.TimeValue(mem.CapturedAt),
	})
}

func (h *Handler) listMemories(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}

	if IsSealed(cap) {
		httpx.Error(w, http.StatusForbidden, "capsule_sealed", "capsule contents are sealed until unlock")
		return
	}
	if !CanViewMemories(cap, member, time.Now().UTC()) {
		if member.ViewBlocked {
			httpx.Error(w, http.StatusForbidden, "view_blocked", "you are blocked from viewing this capsule")
			return
		}
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "capsule is not viewable")
		return
	}

	limit := int32(30)
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 100 {
			limit = int32(n)
		}
	}

	var cursorCreated pgtype.Timestamptz
	var cursorID pgtype.UUID
	if c := r.URL.Query().Get("cursor"); c != "" {
		if parts := splitCursor(c); len(parts) == 2 {
			if t, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
				cursorCreated = pg.Time(t)
			}
			if id, err := uuid.Parse(parts[1]); err == nil {
				cursorID = pg.UUID(id)
			}
		}
	}

	rows, err := h.Q.ListCapsuleMemories(r.Context(), dbgen.ListCapsuleMemoriesParams{
		ContainerID:     pg.UUID(capID),
		CursorCreatedAt: cursorCreated,
		CursorID:        cursorID,
		PageLimit:       limit + 1,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	hasMore := len(rows) > int(limit)
	if hasMore {
		rows = rows[:limit]
	}

	items := make([]memoryDTO, 0, len(rows))
	for _, row := range rows {
		dto, err := h.memoryToDTO(r.Context(), row)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		items = append(items, dto)
	}

	resp := map[string]any{"memories": items}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		resp["next_cursor"] = fmt.Sprintf("%s|%s",
			pg.TimeValue(last.CapturedAt).Format(time.RFC3339Nano),
			pg.UUIDValue(last.ID).String())
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (h *Handler) unfreezeVote(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}
	if cap.State != StateFrozen {
		httpx.Error(w, http.StatusConflict, "capsule_not_frozen", "capsule is not frozen")
		return
	}
	if member.InviteStatus != "accepted" {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "not an active member")
		return
	}

	if err := h.Q.AddUnfreezeVote(r.Context(), dbgen.AddUnfreezeVoteParams{
		CapsuleID: pg.UUID(capID), UserID: pg.UUID(userID),
	}); err != nil {
		httpx.InternalError(w, err)
		return
	}

	votes, err := h.Q.CountUnfreezeVotes(r.Context(), pg.UUID(capID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	members, err := h.Q.CountAcceptedCapsuleMembers(r.Context(), pg.UUID(capID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	if h.Notify != nil {
		others, _ := h.Q.ListCapsuleMemberUserIDs(r.Context(), pg.UUID(capID))
		for _, uid := range others {
			if pg.UUIDValue(uid) != userID {
				h.Notify.CapsuleUnfreezeVote(r.Context(), pg.UUIDValue(uid), userID, capID, cap.Name)
			}
		}
	}

	if votes >= members {
		if _, err := h.Q.UnfreezeCapsule(r.Context(), pg.UUID(capID)); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			httpx.InternalError(w, err)
			return
		}
		_ = h.Q.ClearUnfreezeVotes(r.Context(), pg.UUID(capID))
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"status": "voted", "votes": votes, "required": members})
}

func (h *Handler) unblockMember(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "member not found")
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}
	if member.Role != RoleAdmin {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "admin only")
		return
	}
	if cap.State != StateUnlocked {
		httpx.Error(w, http.StatusConflict, "capsule_not_unlocked", "capsule must be unlocked")
		return
	}

	admin, err := h.Q.GetUserByID(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	plan := billing.ForUser(admin.Plan)
	if plan.Name == billing.PlanSpark {
		httpx.Error(w, http.StatusForbidden, "plan_required", "Plus or Pro required to unblock members")
		return
	}

	if _, err := h.Q.UnblockCapsuleMember(r.Context(), dbgen.UnblockCapsuleMemberParams{
		CapsuleID: pg.UUID(capID), UserID: pg.UUID(targetID),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "member not found or not blocked")
			return
		}
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "unblocked"})
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}
	if cap.State != StateUnlocked || !CanViewMemories(cap, member, time.Now().UTC()) {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "stats available after unlock")
		return
	}

	totals, err := h.Q.GetCapsuleUnlockStats(r.Context(), pg.UUID(capID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	resp := map[string]any{
		"total_memories": totals.TotalMemories,
		"photos":         totals.PhotoCount,
		"videos":         totals.VideoCount,
		"voice_notes":    totals.VoiceCount,
		"streak_perfect": cap.StreakPerfect,
		"streak_current": cap.StreakCurrent,
	}

	if top, err := h.Q.GetTopCapsuleContributor(r.Context(), pg.UUID(capID)); err == nil {
		resp["top_contributor"] = map[string]any{
			"id":            pg.UUIDValue(top.AuthorID).String(),
			"username":      top.Username,
			"display_name":  top.DisplayName,
			"memory_count":  top.MemoryCount,
		}
	}
	if most, err := h.Q.GetMostReactedCapsuleMemory(r.Context(), pg.UUID(capID)); err == nil {
		resp["most_reacted_memory_id"] = pg.UUIDValue(most.ID).String()
		resp["most_reacted_count"] = most.ReactionCount
	}

	httpx.JSON(w, http.StatusOK, resp)
}

// POST /v1/capsules/{id}/unlock-reveal/seen
func (h *Handler) markUnlockRevealSeen(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}
	if member.InviteStatus != "accepted" {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "not an active member")
		return
	}
	if member.ViewBlocked {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "view blocked")
		return
	}
	if cap.State != StateUnlocked && cap.State != StateArchived {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "capsule not unlocked")
		return
	}

	updated, err := h.Q.MarkCapsuleUnlockRevealSeen(r.Context(), dbgen.MarkCapsuleUnlockRevealSeenParams{
		CapsuleID: pg.UUID(capID),
		UserID:    pg.UUID(userID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "not an active member")
			return
		}
		httpx.InternalError(w, err)
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"status":             "ok",
		"unlock_reveal_seen": updated.UnlockRevealSeenAt.Valid,
	})
}

func (h *Handler) leave(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}
	if member.InviteStatus != "accepted" {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "not an active member")
		return
	}
	if cap.State == StateDisintegrated || cap.State == StateArchived {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "capsule not found")
		return
	}

	if member.Role == RoleAdmin {
		next, err := h.Q.GetNextCapsuleAdminCandidate(r.Context(), dbgen.GetNextCapsuleAdminCandidateParams{
			CapsuleID: pg.UUID(capID), UserID: pg.UUID(userID),
		})
		if err == nil {
			_ = h.Q.TransferCapsuleAdmin(r.Context(), dbgen.TransferCapsuleAdminParams{
				CapsuleID: pg.UUID(capID), UserID: next.UserID,
			})
			_ = h.Q.UpdateCapsuleCreator(r.Context(), dbgen.UpdateCapsuleCreatorParams{
				ID: pg.UUID(capID), CreatorID: next.UserID,
			})
		}
	}

	if err := h.Q.RemoveCapsuleMember(r.Context(), dbgen.RemoveCapsuleMemberParams{
		CapsuleID: pg.UUID(capID), UserID: pg.UUID(userID),
	}); err != nil {
		httpx.InternalError(w, err)
		return
	}

	if h.Notify != nil {
		others, _ := h.Q.ListCapsuleMemberUserIDs(r.Context(), pg.UUID(capID))
		for _, uid := range others {
			if pg.UUIDValue(uid) != userID {
				h.Notify.CapsuleMemberLeft(r.Context(), pg.UUIDValue(uid), userID, capID, cap.Name)
			}
		}
	}

	httpx.JSON(w, http.StatusOK, map[string]string{"status": "left"})
}

func (h *Handler) deleteCapsule(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return
	}
	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return
	}
	if member.Role != RoleAdmin {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "admin only")
		return
	}
	// Purge all memory media from object storage before marking disintegrated.
	if err := purgeCapsuleMemories(r.Context(), h.Q, h.Media, capID); err != nil {
		httpx.InternalError(w, err)
		return
	}

	var members []pgtype.UUID
	if h.Notify != nil {
		members, _ = h.Q.ListCapsuleMemberUserIDs(r.Context(), pg.UUID(capID))
	}

	if _, err := h.Q.MarkCapsuleDisintegrated(r.Context(), pg.UUID(capID)); err != nil {
		httpx.InternalError(w, err)
		return
	}

	if h.Notify != nil {
		for _, uid := range members {
			if pg.UUIDValue(uid) != userID {
				h.Notify.CapsuleDeleted(r.Context(), pg.UUIDValue(uid), userID, capID, cap.Name)
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func (h *Handler) parseCapsuleID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "capsule not found")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) loadCapsuleMember(w http.ResponseWriter, r *http.Request, capID, userID uuid.UUID) (dbgen.Capsule, dbgen.CapsuleMember, bool) {
	cap, err := h.Q.GetCapsuleByID(r.Context(), pg.UUID(capID))
	if errors.Is(err, pgx.ErrNoRows) || cap.State == StateDisintegrated {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "capsule not found")
		return dbgen.Capsule{}, dbgen.CapsuleMember{}, false
	}
	if err != nil {
		httpx.InternalError(w, err)
		return dbgen.Capsule{}, dbgen.CapsuleMember{}, false
	}
	member, err := h.Q.GetCapsuleMember(r.Context(), dbgen.GetCapsuleMemberParams{
		CapsuleID: pg.UUID(capID), UserID: pg.UUID(userID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "capsule not found")
		return dbgen.Capsule{}, dbgen.CapsuleMember{}, false
	}
	if err != nil {
		httpx.InternalError(w, err)
		return dbgen.Capsule{}, dbgen.CapsuleMember{}, false
	}
	return cap, member, true
}

func (h *Handler) buildCapsuleDetail(w http.ResponseWriter, r *http.Request, capID, userID uuid.UUID) (capsuleDetailDTO, bool) {
	cap, _, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return capsuleDetailDTO{}, false
	}

	memCount, err := h.Q.CountCapsuleMemories(r.Context(), pg.UUID(capID))
	if err != nil {
		httpx.InternalError(w, err)
		return capsuleDetailDTO{}, false
	}

	members, err := h.Q.ListCapsuleMembers(r.Context(), pg.UUID(capID))
	if err != nil {
		httpx.InternalError(w, err)
		return capsuleDetailDTO{}, false
	}

	memberDTOs := make([]memberDTO, 0, len(members))
	for _, m := range members {
		cnt, _ := h.Q.CountMemoriesByAuthorInCapsule(r.Context(), dbgen.CountMemoriesByAuthorInCapsuleParams{
			ContainerID: pg.UUID(capID), AuthorID: m.UserID,
		})
		var accepted *time.Time
		if m.AcceptedAt.Valid {
			t := pg.TimeValue(m.AcceptedAt)
			accepted = &t
		}
		var lastContrib *time.Time
		if lc, err := h.Q.GetMemberLastCapsuleContribution(r.Context(), dbgen.GetMemberLastCapsuleContributionParams{
			ContainerID: pg.UUID(capID), AuthorID: m.UserID,
		}); err == nil {
			t := pg.TimeValue(lc)
			lastContrib = &t
		}
		u := dbgen.User{ID: m.UserID, Username: m.Username, DisplayName: m.DisplayName, AvatarMediaID: m.AvatarMediaID}
		card := users.ToProfileCard(u)
		card.AvatarURL = h.signAvatar(r.Context(), u)
		memberDTOs = append(memberDTOs, memberDTO{
			ID: pg.UUIDValue(m.UserID).String(), Username: m.Username, DisplayName: m.DisplayName,
			AvatarURL: card.AvatarURL, Role: m.Role, InviteStatus: m.InviteStatus,
			AcceptedAt: accepted, ViewBlocked: m.ViewBlocked,
			UnlockRevealSeen: m.UnlockRevealSeenAt.Valid, MemoryCount: cnt,
			LastContributionAt: lastContrib,
		})
	}

	var inviteExp, frozenAt, unlockedAt, viewableUntil, lastContrib *time.Time
	if cap.InviteExpiresAt.Valid {
		t := pg.TimeValue(cap.InviteExpiresAt)
		inviteExp = &t
	}
	if cap.FrozenAt.Valid {
		t := pg.TimeValue(cap.FrozenAt)
		frozenAt = &t
	}
	if cap.UnlockedAt.Valid {
		t := pg.TimeValue(cap.UnlockedAt)
		unlockedAt = &t
	}
	if cap.ViewableUntil.Valid {
		t := pg.TimeValue(cap.ViewableUntil)
		viewableUntil = &t
	}
	if cap.LastContributionAt.Valid {
		t := pg.TimeValue(cap.LastContributionAt)
		lastContrib = &t
	}

	var unlockRemaining *int64
	if IsSealed(cap) {
		sec := int64(time.Until(pg.TimeValue(cap.UnlockAt)).Seconds())
		if sec < 0 {
			sec = 0
		}
		unlockRemaining = &sec
	}

	return capsuleDetailDTO{
		ID: pg.UUIDValue(cap.ID).String(), Name: cap.Name, Description: cap.Description,
		Type: cap.Type, State: cap.State, CreatorID: pg.UUIDValue(cap.CreatorID).String(),
		UnlockAt: pg.TimeValue(cap.UnlockAt), InviteExpiresAt: inviteExp,
		FrozenAt: frozenAt, UnlockedAt: unlockedAt, ViewableUntil: viewableUntil,
		UnlockSecondsRemaining: unlockRemaining, MemoryCount: memCount,
		StreakCurrent: cap.StreakCurrent, StreakPerfect: cap.StreakPerfect,
		LastContributionAt: lastContrib, Members: memberDTOs, Sealed: IsSealed(cap),
	}, true
}

func (h *Handler) validateReadyMedia(w http.ResponseWriter, r *http.Request, userID, mediaID uuid.UUID, voice bool) (dbgen.Medium, bool) {
	row, err := h.Q.GetMediaByID(r.Context(), pg.UUID(mediaID))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && pg.UUIDValue(row.OwnerID) != userID) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "media not found")
		return dbgen.Medium{}, false
	}
	if err != nil {
		httpx.InternalError(w, err)
		return dbgen.Medium{}, false
	}
	if row.Status != "ready" {
		httpx.Error(w, http.StatusConflict, "media_not_ready", "media has not been confirmed yet")
		return dbgen.Medium{}, false
	}
	if voice && row.Kind != "voice" {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "voice_media_id must reference a voice note")
		return dbgen.Medium{}, false
	}
	return row, true
}

func (h *Handler) memoryToDTO(ctx context.Context, row dbgen.ListCapsuleMemoriesRow) (memoryDTO, error) {
	author, err := h.Q.GetUserByID(ctx, row.AuthorID)
	if err != nil {
		return memoryDTO{}, err
	}
	mediaRow, err := h.Q.GetMediaByID(ctx, row.MediaID)
	if err != nil {
		return memoryDTO{}, err
	}
	url, err := h.Media.SignedURL(ctx, mediaRow.BucketKey, media.SignedURLTTL)
	if err != nil {
		return memoryDTO{}, err
	}
	var voiceURL *string
	if row.VoiceMediaID.Valid {
		voice, err := h.Q.GetMediaByID(ctx, row.VoiceMediaID)
		if err != nil {
			return memoryDTO{}, err
		}
		v, err := h.Media.SignedURL(ctx, voice.BucketKey, media.SignedURLTTL)
		if err != nil {
			return memoryDTO{}, err
		}
		voiceURL = &v
	}
	card := users.ToProfileCard(author)
	card.AvatarURL = h.signAvatar(ctx, author)
	return memoryDTO{
		ID: pg.UUIDValue(row.ID).String(), Author: card, MediaURL: url, MediaKind: mediaRow.Kind,
		VoiceURL: voiceURL, Caption: row.Caption, CreatedAt: pg.TimeValue(row.CapturedAt),
		ReactionCount: row.ReactionCount, ReactionEmojis: pg.StringSliceFromPG(row.ReactionEmojis),
		CommentCount: row.CommentCount,
	}, nil
}

func (h *Handler) signAvatar(ctx context.Context, u dbgen.User) *string {
	if !u.AvatarMediaID.Valid || h.Media == nil {
		return nil
	}
	row, err := h.Q.GetMediaByID(ctx, u.AvatarMediaID)
	if err != nil || row.Status != "ready" {
		return nil
	}
	url, err := h.Media.SignedURL(ctx, row.BucketKey, media.SignedURLTTL)
	if err != nil {
		return nil
	}
	return &url
}

func splitCursor(c string) []string {
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == '|' {
			return []string{c[:i], c[i+1:]}
		}
	}
	return nil
}
