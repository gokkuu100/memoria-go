package social

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/pg"
	"memoria-backend/internal/users"
)

type Handler struct {
	Q      *dbgen.Queries
	Notify notifications.Sender
}

func (h *Handler) Mount(r chi.Router) {
	r.Put("/memories/{id}/reactions", h.putReaction)
	r.Delete("/memories/{id}/reactions", h.deleteReaction)
	r.Get("/memories/{id}/comments", h.listComments)
	r.Post("/memories/{id}/comments", h.createComment)
	r.Delete("/memories/{id}/comments/{commentId}", h.deleteComment)
}

type commentDTO struct {
	ID        string            `json:"id"`
	Author    users.ProfileCard `json:"author"`
	Body      string            `json:"body"`
	CreatedAt time.Time         `json:"created_at"`
}

func (h *Handler) putReaction(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	mem, ok := h.loadAccessibleMemory(w, r, userID)
	if !ok {
		return
	}
	var req struct {
		Emoji string `json:"emoji"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	if req.Emoji == "" || len(req.Emoji) > 32 {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "emoji is required (max 32 chars)")
		return
	}

	reaction, err := h.Q.UpsertReaction(r.Context(), dbgen.UpsertReactionParams{
		MemoryID: mem.ID,
		UserID:   pg.UUID(userID),
		Emoji:    req.Emoji,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	authorID := pg.UUIDValue(mem.AuthorID)
	if authorID != userID && h.Notify != nil {
		h.Notify.ReactionOnMemory(r.Context(), authorID, userID, pg.UUIDValue(mem.ID))
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"emoji":      reaction.Emoji,
		"created_at": pg.TimeValue(reaction.CreatedAt),
	})
}

func (h *Handler) deleteReaction(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	mem, ok := h.loadAccessibleMemory(w, r, userID)
	if !ok {
		return
	}
	n, err := h.Q.DeleteReaction(r.Context(), dbgen.DeleteReactionParams{
		MemoryID: mem.ID,
		UserID:   pg.UUID(userID),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if n == 0 {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "reaction not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listComments(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	mem, ok := h.loadAccessibleMemory(w, r, userID)
	if !ok {
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

	rows, err := h.Q.ListComments(r.Context(), dbgen.ListCommentsParams{
		MemoryID:        mem.ID,
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

	items := make([]commentDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, commentDTO{
			ID: pg.UUIDValue(row.ID).String(),
			Author: users.ProfileCard{
				ID:          pg.UUIDValue(row.AuthorID).String(),
				Username:    row.Username,
				DisplayName: row.DisplayName,
			},
			Body:      row.Body,
			CreatedAt: pg.TimeValue(row.CreatedAt),
		})
	}

	resp := map[string]any{"comments": items}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		resp["next_cursor"] = formatCursor(pg.TimeValue(last.CreatedAt), pg.UUIDValue(last.ID))
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (h *Handler) createComment(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	mem, ok := h.loadAccessibleMemory(w, r, userID)
	if !ok {
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	if req.Body == "" || len(req.Body) > httpx.MaxCommentLen {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "body must be 1-1000 characters")
		return
	}

	c, err := h.Q.CreateComment(r.Context(), dbgen.CreateCommentParams{
		MemoryID: mem.ID,
		AuthorID: pg.UUID(userID),
		Body:     req.Body,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	if h.Notify != nil {
		authorID := pg.UUIDValue(mem.AuthorID)
		if authorID != userID {
			h.Notify.CommentOnMemory(r.Context(), authorID, userID, pg.UUIDValue(mem.ID))
		}
		prior, err := h.Q.ListCommentAuthorsExcept(r.Context(), dbgen.ListCommentAuthorsExceptParams{
			MemoryID: mem.ID,
			AuthorID: pg.UUID(userID),
		})
		if err == nil {
			for _, pid := range prior {
				pidVal := pg.UUIDValue(pid)
				if pidVal != userID && pidVal != authorID {
					h.Notify.CommentOnMemory(r.Context(), pidVal, userID, pg.UUIDValue(mem.ID))
				}
			}
		}
	}

	author, _ := h.Q.GetUserByID(r.Context(), pg.UUID(userID))
	httpx.JSON(w, http.StatusCreated, commentDTO{
		ID:        pg.UUIDValue(c.ID).String(),
		Author:    users.ToProfileCard(author),
		Body:      c.Body,
		CreatedAt: pg.TimeValue(c.CreatedAt),
	})
}

func (h *Handler) deleteComment(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	mem, ok := h.loadAccessibleMemory(w, r, userID)
	if !ok {
		return
	}
	commentID, err := uuid.Parse(chi.URLParam(r, "commentId"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "comment not found")
		return
	}
	n, err := h.Q.DeleteComment(r.Context(), dbgen.DeleteCommentParams{
		ID:       pg.UUID(commentID),
		AuthorID: pg.UUID(userID),
		MemoryID: mem.ID,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if n == 0 {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "comment not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) loadAccessibleMemory(w http.ResponseWriter, r *http.Request, userID uuid.UUID) (dbgen.Memory, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return dbgen.Memory{}, false
	}
	mem, err := h.Q.GetMemoryByID(r.Context(), pg.UUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return dbgen.Memory{}, false
	}
	if err != nil {
		httpx.InternalError(w, err)
		return dbgen.Memory{}, false
	}

	switch mem.ContainerType {
	case "album":
		if !h.canAccessAlbumMemory(w, r, userID, pg.UUIDValue(mem.ContainerID)) {
			return dbgen.Memory{}, false
		}
	case "capsule":
		if !h.canAccessCapsuleMemory(w, r, userID, pg.UUIDValue(mem.ContainerID)) {
			return dbgen.Memory{}, false
		}
	default:
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return dbgen.Memory{}, false
	}
	return mem, true
}

func (h *Handler) canAccessAlbumMemory(w http.ResponseWriter, r *http.Request, userID, albumID uuid.UUID) bool {
	album, err := h.Q.GetAlbumByID(r.Context(), pg.UUID(albumID))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return false
	}
	if err != nil {
		httpx.InternalError(w, err)
		return false
	}
	if album.State == "deleted" || album.State == "pending" {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return false
	}
	member, err := h.Q.GetAlbumMember(r.Context(), dbgen.GetAlbumMemberParams{
		AlbumID: pg.UUID(albumID), UserID: pg.UUID(userID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return false
	}
	if err != nil {
		httpx.InternalError(w, err)
		return false
	}
	if member.InviteStatus != "accepted" || member.LeftAt.Valid {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return false
	}
	return true
}

func (h *Handler) canAccessCapsuleMemory(w http.ResponseWriter, r *http.Request, userID, capsuleID uuid.UUID) bool {
	cap, err := h.Q.GetCapsuleByID(r.Context(), pg.UUID(capsuleID))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return false
	}
	if err != nil {
		httpx.InternalError(w, err)
		return false
	}
	if cap.State != "unlocked" {
		httpx.Error(w, http.StatusForbidden, "capsule_sealed", "reactions and comments require an unlocked capsule")
		return false
	}
	member, err := h.Q.GetCapsuleMember(r.Context(), dbgen.GetCapsuleMemberParams{
		CapsuleID: pg.UUID(capsuleID), UserID: pg.UUID(userID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return false
	}
	if err != nil {
		httpx.InternalError(w, err)
		return false
	}
	if member.InviteStatus != "accepted" || member.ViewBlocked {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return false
	}
	if cap.ViewableUntil.Valid && time.Now().UTC().After(pg.TimeValue(cap.ViewableUntil)) {
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "capsule viewability expired")
		return false
	}
	return true
}

func splitCursor(c string) []string {
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == '|' {
			return []string{c[:i], c[i+1:]}
		}
	}
	return nil
}

func formatCursor(t time.Time, id uuid.UUID) string {
	return t.Format(time.RFC3339Nano) + "|" + id.String()
}
