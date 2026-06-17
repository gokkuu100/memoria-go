package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/pg"
)

type Handler struct {
	Q       *dbgen.Queries
	Flusher interface {
		FlushOnAppOpen(ctx context.Context, userID uuid.UUID)
	}
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/me/notification-prefs", h.getPrefs)
	r.Put("/me/notification-prefs", h.putPrefs)
	r.Get("/notifications/unread-count", h.unreadCount)
	r.Get("/notifications", h.listNotifications)
	r.Post("/notifications/test-push", h.sendTestPush)
	r.Post("/notifications/read-all", h.markAllRead)
	r.Post("/notifications/{id}/read", h.markRead)
	r.Post("/notifications/flush", h.flushOnOpen)
}

type prefItem struct {
	Category string `json:"category"`
	Enabled  bool   `json:"enabled"`
}

type notificationItem struct {
	ID       string          `json:"id"`
	Category string          `json:"category"`
	Title    string          `json:"title"`
	Body     string          `json:"body"`
	Data     json.RawMessage `json:"data"`
	SentAt   string          `json:"sent_at"`
	ReadAt   *string         `json:"read_at"`
}

// GET /v1/me/notification-prefs
func (h *Handler) getPrefs(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	stored, err := h.Q.GetNotificationPrefs(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	enabledByCat := make(map[string]bool, len(stored))
	for _, row := range stored {
		enabledByCat[row.Category] = row.Enabled
	}
	prefs := make([]prefItem, 0, len(AllCategories))
	for _, cat := range AllCategories {
		enabled := true
		if v, ok := enabledByCat[cat]; ok {
			enabled = v
		}
		prefs = append(prefs, prefItem{Category: cat, Enabled: enabled})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"prefs": prefs})
}

// PUT /v1/me/notification-prefs
func (h *Handler) putPrefs(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	var req struct {
		Prefs []prefItem `json:"prefs"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	for _, p := range req.Prefs {
		if !ValidCategory(p.Category) {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "unknown category: "+p.Category)
			return
		}
		if err := h.Q.UpsertNotificationPref(r.Context(), dbgen.UpsertNotificationPrefParams{
			UserID:   pg.UUID(userID),
			Category: p.Category,
			Enabled:  p.Enabled,
		}); err != nil {
			httpx.InternalError(w, err)
			return
		}
	}
	h.getPrefs(w, r)
}

// GET /v1/notifications
func (h *Handler) listNotifications(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	limit := int32(20)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 50 {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "limit must be 1-50")
			return
		}
		limit = int32(n)
	}

	params := dbgen.ListNotificationsForUserParams{
		UserID:    pg.UUID(userID),
		PageLimit: limit,
	}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		sentAt, id, err := decodeCursor(cursor)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid cursor")
			return
		}
		params.CursorSentAt = pg.Time(sentAt)
		params.CursorID = pg.UUID(id)
	}

	rows, err := h.Q.ListNotificationsForUser(r.Context(), params)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	items := make([]notificationItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, toNotificationItem(row))
	}
	resp := map[string]any{"notifications": items}
	if len(rows) == int(limit) {
		last := rows[len(rows)-1]
		resp["next_cursor"] = encodeCursor(pg.TimeValue(last.SentAt), pg.UUIDValue(last.ID))
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// GET /v1/notifications/unread-count
func (h *Handler) unreadCount(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	count, err := h.Q.CountUnreadNotifications(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]int64{"count": count})
}

// POST /v1/notifications/{id}/read
func (h *Handler) markRead(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "id must be a valid UUID")
		return
	}
	rows, err := h.Q.MarkNotificationRead(r.Context(), dbgen.MarkNotificationReadParams{
		ID:     pg.UUID(id),
		UserID: pg.UUID(userID),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if rows == 0 {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "notification not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "read"})
}

// POST /v1/notifications/flush — deliver due notifications on app foreground.
func (h *Handler) flushOnOpen(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	if h.Flusher != nil {
		h.Flusher.FlushOnAppOpen(r.Context(), userID)
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "flushed"})
}

func (h *Handler) markAllRead(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	if err := h.Q.MarkAllNotificationsRead(r.Context(), pg.UUID(userID)); err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "read"})
}

// POST /v1/notifications/test-push — send a test push to your own devices.
func (h *Handler) sendTestPush(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	title := strings.TrimSpace(req.Title)
	body := strings.TrimSpace(req.Body)
	if title == "" {
		title = "Memoria test notification"
	}
	if body == "" {
		body = "Push is working: banner + sound should appear."
	}

	payload, err := json.Marshal(map[string]any{
		"type": "test_push",
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	if _, err := h.Q.CreateOutbox(r.Context(), dbgen.CreateOutboxParams{
		UserID:   pg.UUID(userID),
		Category: CategorySystemAlerts,
		Title:    title,
		Body:     body,
		Data:     payload,
	}); err != nil {
		httpx.InternalError(w, err)
		return
	}
	if h.Flusher != nil {
		// Flush immediately so user can validate phone banner/sound in real time.
		h.Flusher.FlushOnAppOpen(r.Context(), userID)
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func toNotificationItem(row dbgen.NotificationOutbox) notificationItem {
	item := notificationItem{
		ID:       pg.UUIDValue(row.ID).String(),
		Category: row.Category,
		Title:    row.Title,
		Body:     row.Body,
		Data:     row.Data,
		SentAt:   pg.TimeValue(row.SentAt).UTC().Format(time.RFC3339Nano),
	}
	if row.ReadAt.Valid {
		s := pg.TimeValue(row.ReadAt).UTC().Format(time.RFC3339Nano)
		item.ReadAt = &s
	}
	if len(item.Data) == 0 {
		item.Data = json.RawMessage("{}")
	}
	return item
}

func encodeCursor(sentAt time.Time, id uuid.UUID) string {
	return sentAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()
}

func decodeCursor(raw string) (time.Time, uuid.UUID, error) {
	parts := strings.SplitN(raw, "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, errors.New("invalid cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return t, id, nil
}
