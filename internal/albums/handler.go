package albums

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
	r.Post("/albums", h.create)
	r.Get("/albums", h.list)
	r.Get("/albums/{id}", h.get)
	r.Post("/albums/{id}/accept", h.accept)
	r.Post("/albums/{id}/decline", h.decline)
	r.Post("/albums/{id}/memories", h.addMemory)
	r.Get("/albums/{id}/memories", h.listMemories)
	r.Get("/albums/{id}/memories/{memoryId}/media", h.streamMemoryMedia)
	r.Get("/albums/{id}/memories/{memoryId}/voice", h.streamMemoryVoice)
	r.Post("/albums/{id}/leave", h.leave)
	r.Get("/timeline", h.timeline)
}

type memberDTO struct {
	ID            string     `json:"id"`
	Username      string     `json:"username"`
	DisplayName   string     `json:"display_name"`
	AvatarURL     *string    `json:"avatar_url,omitempty"`
	InviteStatus  string     `json:"invite_status"`
	AcceptedAt    *time.Time `json:"accepted_at,omitempty"`
	LeftAt        *time.Time `json:"left_at,omitempty"`
	MemoryCount   int64      `json:"memory_count"`
}

type albumDetailDTO struct {
	ID                 string      `json:"id"`
	Name               string      `json:"name"`
	CoverStyle         string      `json:"cover_style"`
	State              string      `json:"state"`
	CreatorID          string      `json:"creator_id"`
	ActivatedAt        *time.Time  `json:"activated_at,omitempty"`
	ArchiveUntil       *time.Time  `json:"archive_until,omitempty"`
	InviteExpiresAt    time.Time   `json:"invite_expires_at"`
	LifespanSeconds    *int64      `json:"lifespan_seconds_remaining,omitempty"`
	MemoryCount        int64       `json:"memory_count"`
	Members            []memberDTO `json:"members"`
	ContributionBalance map[string]int64 `json:"contribution_balance"`
}

type albumSummaryDTO struct {
	ID                     string     `json:"id"`
	Name                   string     `json:"name"`
	CoverStyle             string     `json:"cover_style"`
	State                  string     `json:"state"`
	ActivatedAt            *time.Time `json:"activated_at,omitempty"`
	MemoryCount            int64      `json:"memory_count"`
	MemberCount            int64      `json:"member_count"`
	LifespanSecondsRemaining *int64   `json:"lifespan_seconds_remaining,omitempty"`
}

type memoryDTO struct {
	ID             string            `json:"id"`
	Author         users.ProfileCard `json:"author"`
	MediaURL       string            `json:"media_url"`
	MediaKind      string            `json:"media_kind"`
	VoiceURL       *string           `json:"voice_url,omitempty"`
	Caption        *string           `json:"caption,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	ReactionCount  int64             `json:"reaction_count"`
	ReactionEmojis []string          `json:"reaction_emojis,omitempty"`
	CommentCount   int64             `json:"comment_count"`
}

// POST /v1/albums
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	var req struct {
		Name           string `json:"name"`
		CoverStyle     string `json:"cover_style"`
		InvitedUserID  string `json:"invited_user_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	if req.Name == "" || len(req.Name) > httpx.MaxNameLen {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "name must be 1-100 characters")
		return
	}
	if !validCoverStyle(req.CoverStyle) {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid cover_style")
		return
	}
	inviteeID, err := uuid.Parse(req.InvitedUserID)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "invited_user_id must be a valid UUID")
		return
	}
	if inviteeID == userID {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "cannot invite yourself")
		return
	}
	if !friends.AssertNotBlocked(w, r.Context(), h.Q, userID, inviteeID) {
		return
	}

	friendsOK, err := h.Q.AreFriends(r.Context(), dbgen.AreFriendsParams{
		UserA: pg.UUID(userID),
		UserB: pg.UUID(inviteeID),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if !friendsOK {
		httpx.Error(w, http.StatusBadRequest, "not_connected", "invited user must be an accepted friend")
		return
	}

	creator, err := h.Q.GetUserByID(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	plan := billing.ForUser(creator.Plan)

	activeCount, err := h.Q.CountActiveAlbumsForUser(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if !billing.IsUnlimited(plan.MaxActiveAlbums) && int(activeCount) >= plan.MaxActiveAlbums {
		httpx.Error(w, http.StatusForbidden, "album_limit_reached",
			fmt.Sprintf("your plan allows %d active album(s)", plan.MaxActiveAlbums))
		return
	}

	// 2 members: creator + one invitee.
	if plan.MaxAlbumMembers < 2 {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "plan does not allow album creation")
		return
	}

	expires := time.Now().UTC().Add(inviteWindow)
	album, err := h.Q.CreateAlbum(r.Context(), dbgen.CreateAlbumParams{
		CreatorID:       pg.UUID(userID),
		Name:            req.Name,
		CoverStyle:      req.CoverStyle,
		InviteExpiresAt: pg.Time(expires),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	now := pg.Time(time.Now().UTC())
	if err := h.Q.AddAlbumMember(r.Context(), dbgen.AddAlbumMemberParams{
		AlbumID: album.ID, UserID: pg.UUID(userID), InviteStatus: "accepted", AcceptedAt: now,
	}); err != nil {
		httpx.InternalError(w, err)
		return
	}
	if err := h.Q.AddAlbumMember(r.Context(), dbgen.AddAlbumMemberParams{
		AlbumID: album.ID, UserID: pg.UUID(inviteeID), InviteStatus: "pending",
	}); err != nil {
		httpx.InternalError(w, err)
		return
	}

	if h.Notify != nil {
		h.Notify.AlbumInviteReceived(r.Context(), inviteeID, userID, pg.UUIDValue(album.ID), album.Name)
	}

	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id":                pg.UUIDValue(album.ID).String(),
		"name":              album.Name,
		"cover_style":       album.CoverStyle,
		"state":             album.State,
		"invite_expires_at": pg.TimeValue(album.InviteExpiresAt),
	})
}

func (h *Handler) accept(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	albumID, ok := h.parseAlbumID(w, r)
	if !ok {
		return
	}
	album, member, ok := h.loadAlbumMember(w, r, albumID, userID)
	if !ok {
		return
	}
	if album.State != "pending" {
		httpx.Error(w, http.StatusConflict, "album_not_pending", "album is not awaiting acceptance")
		return
	}
	if member.InviteStatus != "pending" {
		httpx.Error(w, http.StatusConflict, "invite_not_pending", "no pending invite for you")
		return
	}

	if _, err := h.Q.AcceptAlbumInvite(r.Context(), dbgen.AcceptAlbumInviteParams{
		AlbumID: pg.UUID(albumID), UserID: pg.UUID(userID),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusConflict, "invite_not_pending", "no pending invite for you")
			return
		}
		httpx.InternalError(w, err)
		return
	}

	// Activate when all members accepted (both for 2-person album).
	members, err := h.Q.ListAlbumMembers(r.Context(), pg.UUID(albumID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	allAccepted := true
	for _, m := range members {
		if m.InviteStatus != "accepted" {
			allAccepted = false
			break
		}
	}
	if allAccepted {
		if _, err := h.Q.ActivateAlbum(r.Context(), pg.UUID(albumID)); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			httpx.InternalError(w, err)
			return
		}
	}

	httpx.JSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (h *Handler) decline(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	albumID, ok := h.parseAlbumID(w, r)
	if !ok {
		return
	}
	album, member, ok := h.loadAlbumMember(w, r, albumID, userID)
	if !ok {
		return
	}
	if album.State != "pending" {
		httpx.Error(w, http.StatusConflict, "album_not_pending", "album is not awaiting acceptance")
		return
	}
	if member.InviteStatus != "pending" {
		httpx.Error(w, http.StatusConflict, "invite_not_pending", "no pending invite for you")
		return
	}

	if _, err := h.Q.DeclineAlbumInvite(r.Context(), dbgen.DeclineAlbumInviteParams{
		AlbumID: pg.UUID(albumID), UserID: pg.UUID(userID),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusConflict, "invite_not_pending", "no pending invite for you")
			return
		}
		httpx.InternalError(w, err)
		return
	}
	if _, err := h.Q.MarkAlbumDeleted(r.Context(), pg.UUID(albumID)); err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "declined"})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	albumID, ok := h.parseAlbumID(w, r)
	if !ok {
		return
	}
	dto, ok := h.buildAlbumDetail(w, r, albumID, userID)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, dto)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	state := r.URL.Query().Get("state")
	if state != "active" && state != "archived" && state != "invites" {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "state must be active, archived, or invites")
		return
	}

	var rows []dbgen.Album
	var err error
	if state == "invites" {
		rows, err = h.Q.ListAlbumInvitesForUser(r.Context(), pg.UUID(userID))
	} else {
		rows, err = h.Q.ListAlbumsForUser(r.Context(), dbgen.ListAlbumsForUserParams{
			UserID: pg.UUID(userID),
			State:  state,
		})
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	out := make([]albumSummaryDTO, 0, len(rows))
	for _, a := range rows {
		count, err := h.Q.CountAlbumMemories(r.Context(), a.ID)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		memberCnt, err := h.Q.CountActiveMembers(r.Context(), a.ID)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		var activated *time.Time
		if a.ActivatedAt.Valid {
			t := pg.TimeValue(a.ActivatedAt)
			activated = &t
		}
		var lifespanRemaining *int64
		if a.State == "active" && a.ActivatedAt.Valid {
			creator, err := h.Q.GetUserByID(r.Context(), a.CreatorID)
			if err != nil {
				httpx.InternalError(w, err)
				return
			}
			plan := billing.ForUser(creator.Plan)
			if !billing.IsUnlimited(plan.AlbumLifespanDays) {
				end := pg.TimeValue(a.ActivatedAt).Add(time.Duration(plan.AlbumLifespanDays) * 24 * time.Hour)
				sec := int64(time.Until(end).Seconds())
				if sec < 0 {
					sec = 0
				}
				lifespanRemaining = &sec
			}
		}
		out = append(out, albumSummaryDTO{
			ID:                       pg.UUIDValue(a.ID).String(),
			Name:                     a.Name,
			CoverStyle:               a.CoverStyle,
			State:                    a.State,
			ActivatedAt:              activated,
			MemoryCount:              count,
			MemberCount:              memberCnt,
			LifespanSecondsRemaining: lifespanRemaining,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"albums": out})
}

func (h *Handler) addMemory(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	albumID, ok := h.parseAlbumID(w, r)
	if !ok {
		return
	}
	album, member, ok := h.loadActiveMember(w, r, albumID, userID)
	if !ok {
		return
	}
	if album.State != "active" {
		httpx.Error(w, http.StatusForbidden, "album_not_active", "album is not active")
		return
	}
	if member.LeftAt.Valid {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "you have left this album")
		return
	}

	var req struct {
		MediaID      string  `json:"media_id"`
		VoiceMediaID *string `json:"voice_media_id"`
		Caption      *string `json:"caption"`
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

	creator, err := h.Q.GetUserByID(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	plan := billing.ForUser(creator.Plan)

	count, err := h.Q.CountAlbumMemories(r.Context(), pg.UUID(albumID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if !billing.IsUnlimited(plan.MaxAlbumPhotos) && int(count) >= plan.MaxAlbumPhotos {
		httpx.Error(w, http.StatusForbidden, "album_photo_limit_reached",
			fmt.Sprintf("albums are limited to %d photos on your plan", plan.MaxAlbumPhotos))
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

	mem, err := h.Q.CreateMemory(r.Context(), dbgen.CreateMemoryParams{
		ContainerType: "album",
		ContainerID:   pg.UUID(albumID),
		AuthorID:      pg.UUID(userID),
		MediaID:       pg.UUID(mediaID),
		VoiceMediaID:  voiceID,
		Caption:       req.Caption,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	// Notify other active members.
	members, err := h.Q.ListAlbumMembers(r.Context(), pg.UUID(albumID))
	if err == nil && h.Notify != nil {
		widgetUsers := make([]uuid.UUID, 0, len(members))
		for _, m := range members {
			mid := pg.UUIDValue(m.UserID)
			if m.InviteStatus != "accepted" || m.LeftAt.Valid {
				continue
			}
			widgetUsers = append(widgetUsers, mid)
			if mid == userID {
				continue
			}
			h.Notify.AlbumMemoryAdded(r.Context(), mid, userID, albumID, album.Name)
		}
		h.Notify.RefreshWidgetForUsers(r.Context(), widgetUsers)
	}

	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id":         pg.UUIDValue(mem.ID).String(),
		"created_at": pg.TimeValue(mem.CreatedAt),
	})
}

func (h *Handler) listMemories(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	albumID, ok := h.parseAlbumID(w, r)
	if !ok {
		return
	}
	album, _, ok := h.loadAlbumMember(w, r, albumID, userID)
	if !ok {
		return
	}
	if album.State == "deleted" || album.State == "pending" {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "album not found")
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
		// cursor format: created_at RFC3339Nano|uuid
		parts := splitCursor(c)
		if len(parts) == 2 {
			if t, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
				cursorCreated = pg.Time(t)
			}
			if id, err := uuid.Parse(parts[1]); err == nil {
				cursorID = pg.UUID(id)
			}
		}
	}

	rows, err := h.Q.ListAlbumMemories(r.Context(), dbgen.ListAlbumMemoriesParams{
		ContainerID:     pg.UUID(albumID),
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
			pg.TimeValue(last.CreatedAt).Format(time.RFC3339Nano),
			pg.UUIDValue(last.ID).String())
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (h *Handler) leave(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	albumID, ok := h.parseAlbumID(w, r)
	if !ok {
		return
	}
	album, member, ok := h.loadAlbumMember(w, r, albumID, userID)
	if !ok {
		return
	}
	if album.State == "deleted" || album.State == "pending" {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "album not found")
		return
	}
	if member.InviteStatus != "accepted" {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "not an album member")
		return
	}
	if member.LeftAt.Valid {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "already_left"})
		return
	}

	if err := deleteAuthorMemoriesInAlbum(r.Context(), h.Q, h.Media, albumID, userID); err != nil {
		httpx.InternalError(w, err)
		return
	}
	if _, err := h.Q.MarkMemberLeft(r.Context(), dbgen.MarkMemberLeftParams{
		AlbumID: pg.UUID(albumID), UserID: pg.UUID(userID),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusConflict, httpx.CodeBadRequest, "already left")
			return
		}
		httpx.InternalError(w, err)
		return
	}

	remaining, err := h.Q.CountActiveMembers(r.Context(), pg.UUID(albumID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if remaining == 0 {
		if err := purgeAlbumMemories(r.Context(), h.Q, h.Media, albumID); err != nil {
			httpx.InternalError(w, err)
			return
		}
		if _, err := h.Q.MarkAlbumDeleted(r.Context(), pg.UUID(albumID)); err != nil {
			httpx.InternalError(w, err)
			return
		}
	}

	httpx.JSON(w, http.StatusOK, map[string]string{"status": "left"})
}

type timelineDayDTO struct {
	Date  string              `json:"date"`
	Items []timelineItemDTO   `json:"items"`
}

type timelineItemDTO struct {
	Type          string  `json:"type"`
	MemoryID      string  `json:"memory_id,omitempty"`
	AlbumID       string  `json:"album_id,omitempty"`
	AlbumName     string  `json:"album_name,omitempty"`
	CapsuleID     string  `json:"capsule_id,omitempty"`
	CapsuleName   string  `json:"capsule_name,omitempty"`
	ThumbURL      string  `json:"thumb_url,omitempty"`
	UnlockAt      *string `json:"unlock_at,omitempty"`
	SealedMarker  bool    `json:"sealed_marker,omitempty"`
}

func (h *Handler) timeline(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	month := r.URL.Query().Get("month")
	if month == "" {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "month query param required (YYYY-MM)")
		return
	}
	t, err := time.Parse("2006-01", month)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "month must be YYYY-MM")
		return
	}
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	rows, err := h.Q.ListTimelineAlbumMemories(r.Context(), dbgen.ListTimelineAlbumMemoriesParams{
		UserID:      pg.UUID(userID),
		CreatedAt:   pg.Time(start),
		CreatedAt_2: pg.Time(end),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	byDay := map[string][]timelineItemDTO{}
	for _, row := range rows {
		day := pg.TimeValue(row.CreatedAt).UTC().Format("2006-01-02")
		url, err := h.Media.SignedURL(r.Context(), row.MediaKey, media.SignedURLTTL)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		byDay[day] = append(byDay[day], timelineItemDTO{
			Type:      "album_memory",
			MemoryID:  pg.UUIDValue(row.ID).String(),
			AlbumID:   pg.UUIDValue(row.AlbumID).String(),
			AlbumName: row.AlbumName,
			ThumbURL:  url,
		})
	}

	// Capsule timeline v2: sealed markers, countdowns, unlocked thumbnails.
	sealed, err := h.Q.ListTimelineCapsuleSealedMarkers(r.Context(), dbgen.ListTimelineCapsuleSealedMarkersParams{
		UserID: pg.UUID(userID), StartDay: pg.Date(start), EndDay: pg.Date(end),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	for _, row := range sealed {
		day := pg.DateValue(row.ContributionDay).Format("2006-01-02")
		byDay[day] = append(byDay[day], timelineItemDTO{
			Type:         "capsule_sealed",
			CapsuleID:    pg.UUIDValue(row.CapsuleID).String(),
			CapsuleName:  row.CapsuleName,
			SealedMarker: true,
		})
	}

	countdowns, err := h.Q.ListTimelineCapsuleCountdowns(r.Context(), dbgen.ListTimelineCapsuleCountdownsParams{
		UserID: pg.UUID(userID), RangeStart: pg.Time(start), RangeEnd: pg.Time(end),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	for _, row := range countdowns {
		day := pg.TimeValue(row.UnlockAt).UTC().Format("2006-01-02")
		unlock := pg.TimeValue(row.UnlockAt).UTC().Format(time.RFC3339)
		byDay[day] = append(byDay[day], timelineItemDTO{
			Type:        "capsule_countdown",
			CapsuleID:   pg.UUIDValue(row.CapsuleID).String(),
			CapsuleName: row.CapsuleName,
			UnlockAt:    &unlock,
		})
	}

	unlocked, err := h.Q.ListTimelineUnlockedCapsuleMemories(r.Context(), dbgen.ListTimelineUnlockedCapsuleMemoriesParams{
		UserID: pg.UUID(userID), CreatedAt: pg.Time(start), CreatedAt_2: pg.Time(end),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	for _, row := range unlocked {
		day := pg.TimeValue(row.CreatedAt).UTC().Format("2006-01-02")
		url, err := h.Media.SignedURL(r.Context(), row.MediaKey, media.SignedURLTTL)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		byDay[day] = append(byDay[day], timelineItemDTO{
			Type:        "capsule_memory",
			MemoryID:    pg.UUIDValue(row.ID).String(),
			CapsuleID:   pg.UUIDValue(row.CapsuleID).String(),
			CapsuleName: row.CapsuleName,
			ThumbURL:    url,
		})
	}

	days := make([]timelineDayDTO, 0, len(byDay))
	for day, items := range byDay {
		days = append(days, timelineDayDTO{Date: day, Items: items})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"month": month, "days": days})
}

// --- helpers ---

func (h *Handler) parseAlbumID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "album not found")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) loadAlbumMember(w http.ResponseWriter, r *http.Request, albumID, userID uuid.UUID) (dbgen.Album, dbgen.AlbumMember, bool) {
	album, err := h.Q.GetAlbumByID(r.Context(), pg.UUID(albumID))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "album not found")
		return dbgen.Album{}, dbgen.AlbumMember{}, false
	}
	if err != nil {
		httpx.InternalError(w, err)
		return dbgen.Album{}, dbgen.AlbumMember{}, false
	}
	member, err := h.Q.GetAlbumMember(r.Context(), dbgen.GetAlbumMemberParams{
		AlbumID: pg.UUID(albumID), UserID: pg.UUID(userID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "album not found")
		return dbgen.Album{}, dbgen.AlbumMember{}, false
	}
	if err != nil {
		httpx.InternalError(w, err)
		return dbgen.Album{}, dbgen.AlbumMember{}, false
	}
	return album, member, true
}

func (h *Handler) loadActiveMember(w http.ResponseWriter, r *http.Request, albumID, userID uuid.UUID) (dbgen.Album, dbgen.AlbumMember, bool) {
	album, member, ok := h.loadAlbumMember(w, r, albumID, userID)
	if !ok {
		return album, member, false
	}
	if member.InviteStatus != "accepted" {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "not an album member")
		return dbgen.Album{}, dbgen.AlbumMember{}, false
	}
	return album, member, true
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

func (h *Handler) buildAlbumDetail(w http.ResponseWriter, r *http.Request, albumID, userID uuid.UUID) (albumDetailDTO, bool) {
	album, member, ok := h.loadAlbumMember(w, r, albumID, userID)
	if !ok {
		return albumDetailDTO{}, false
	}
	if album.State == "deleted" {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "album not found")
		return albumDetailDTO{}, false
	}
	_ = member

	creator, err := h.Q.GetUserByID(r.Context(), album.CreatorID)
	if err != nil {
		httpx.InternalError(w, err)
		return albumDetailDTO{}, false
	}
	plan := billing.ForUser(creator.Plan)

	memCount, err := h.Q.CountAlbumMemories(r.Context(), pg.UUID(albumID))
	if err != nil {
		httpx.InternalError(w, err)
		return albumDetailDTO{}, false
	}

	members, err := h.Q.ListAlbumMembers(r.Context(), pg.UUID(albumID))
	if err != nil {
		httpx.InternalError(w, err)
		return albumDetailDTO{}, false
	}

	balance := map[string]int64{}
	memberDTOs := make([]memberDTO, 0, len(members))
	for _, m := range members {
		cnt, err := h.Q.CountMemoriesByAuthorInAlbum(r.Context(), dbgen.CountMemoriesByAuthorInAlbumParams{
			ContainerID: pg.UUID(albumID),
			AuthorID:    m.UserID,
		})
		if err != nil {
			httpx.InternalError(w, err)
			return albumDetailDTO{}, false
		}
		label := m.DisplayName
		if pg.UUIDValue(m.UserID) == userID {
			label = "You"
		}
		balance[label] = cnt

		var accepted, left *time.Time
		if m.AcceptedAt.Valid {
			t := pg.TimeValue(m.AcceptedAt)
			accepted = &t
		}
		if m.LeftAt.Valid {
			t := pg.TimeValue(m.LeftAt)
			left = &t
		}
		u := dbgen.User{
			ID: m.UserID, Username: m.Username, DisplayName: m.DisplayName, AvatarMediaID: m.AvatarMediaID,
		}
		card := users.ToProfileCard(u)
		card.AvatarURL = h.signAvatar(r.Context(), u)

		memberDTOs = append(memberDTOs, memberDTO{
			ID:           pg.UUIDValue(m.UserID).String(),
			Username:     m.Username,
			DisplayName:  m.DisplayName,
			AvatarURL:    card.AvatarURL,
			InviteStatus: m.InviteStatus,
			AcceptedAt:   accepted,
			LeftAt:       left,
			MemoryCount:  cnt,
		})
	}

	var activated, archiveUntil *time.Time
	if album.ActivatedAt.Valid {
		t := pg.TimeValue(album.ActivatedAt)
		activated = &t
	}
	if album.ArchiveUntil.Valid {
		t := pg.TimeValue(album.ArchiveUntil)
		archiveUntil = &t
	}

	var lifespanRemaining *int64
	if album.State == "active" && album.ActivatedAt.Valid && !billing.IsUnlimited(plan.AlbumLifespanDays) {
		end := pg.TimeValue(album.ActivatedAt).Add(time.Duration(plan.AlbumLifespanDays) * 24 * time.Hour)
		sec := int64(time.Until(end).Seconds())
		if sec < 0 {
			sec = 0
		}
		lifespanRemaining = &sec
	}

	return albumDetailDTO{
		ID:                  pg.UUIDValue(album.ID).String(),
		Name:                album.Name,
		CoverStyle:          album.CoverStyle,
		State:               album.State,
		CreatorID:           pg.UUIDValue(album.CreatorID).String(),
		ActivatedAt:         activated,
		ArchiveUntil:        archiveUntil,
		InviteExpiresAt:     pg.TimeValue(album.InviteExpiresAt),
		LifespanSeconds:     lifespanRemaining,
		MemoryCount:         memCount,
		Members:             memberDTOs,
		ContributionBalance: balance,
	}, true
}

func (h *Handler) memoryToDTO(ctx context.Context, row dbgen.ListAlbumMemoriesRow) (memoryDTO, error) {
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
		ID:            pg.UUIDValue(row.ID).String(),
		Author:        card,
		MediaURL:      url,
		MediaKind:     mediaRow.Kind,
		VoiceURL:      voiceURL,
		Caption:       row.Caption,
		CreatedAt:     pg.TimeValue(row.CreatedAt),
		ReactionCount: row.ReactionCount,
		ReactionEmojis: pg.StringSliceFromPG(row.ReactionEmojis),
		CommentCount:  row.CommentCount,
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
