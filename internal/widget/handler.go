package widget

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/media"
	"memoria-backend/internal/pg"
)

// Handler serves widget-eligible feed payloads for home-screen extensions.
type Handler struct {
	Q     *dbgen.Queries
	Store *media.Store
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/widget/feed", h.feed)
}

type feedItemDTO struct {
	Type        string  `json:"type"`
	AlbumID     string  `json:"album_id,omitempty"`
	AlbumName   string  `json:"album_name,omitempty"`
	CapsuleID   string  `json:"capsule_id,omitempty"`
	CapsuleName string  `json:"capsule_name,omitempty"`
	MemoryID    string  `json:"memory_id,omitempty"`
	ThumbURL    string  `json:"thumb_url,omitempty"`
	UnlockAt    *string `json:"unlock_at,omitempty"`
}

func (h *Handler) feed(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	ctx := r.Context()

	items := make([]feedItemDTO, 0)

	albums, err := h.Q.ListWidgetAlbumPhotos(ctx, pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	for _, row := range albums {
		key := media.WidgetThumbKey(row.ThumbBucketKey, row.MediaKey, row.MediaKind)
		if key == "" {
			continue
		}
		url, err := h.Store.SignedURL(ctx, key, media.SignedURLTTL)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		items = append(items, feedItemDTO{
			Type:      "album_photo",
			AlbumID:   pg.UUIDValue(row.AlbumID).String(),
			AlbumName: row.AlbumName,
			MemoryID:  pg.UUIDValue(row.MemoryID).String(),
			ThumbURL:  url,
		})
	}

	countdowns, err := h.Q.ListWidgetCapsuleCountdowns(ctx, pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	for _, row := range countdowns {
		unlock := pg.TimeValue(row.UnlockAt).UTC().Format(time.RFC3339)
		items = append(items, feedItemDTO{
			Type:        "capsule_countdown",
			CapsuleID:   pg.UUIDValue(row.CapsuleID).String(),
			CapsuleName: row.CapsuleName,
			UnlockAt:    &unlock,
		})
	}

	unlocked, err := h.Q.ListWidgetUnlockedTodayMemories(ctx, pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	for _, row := range unlocked {
		key := media.WidgetThumbKey(row.ThumbBucketKey, row.MediaKey, row.MediaKind)
		if key == "" {
			continue
		}
		url, err := h.Store.SignedURL(ctx, key, media.SignedURLTTL)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		items = append(items, feedItemDTO{
			Type:        "capsule_unlocked",
			CapsuleID:   pg.UUIDValue(row.CapsuleID).String(),
			CapsuleName: row.CapsuleName,
			MemoryID:    pg.UUIDValue(row.MemoryID).String(),
			ThumbURL:    url,
		})
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}
