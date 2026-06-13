package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "golang.org/x/image/webp"

	"memoria-backend/internal/billing"
	"memoria-backend/internal/config"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/pg"
)

// allowedTypes is the per-kind content-type allowlist. In-app capture only
// produces these formats.
var allowedTypes = map[string]map[string]bool{
	"photo": {"image/jpeg": true, "image/png": true, "image/webp": true},
	"video": {"video/mp4": true, "video/quicktime": true},
	"voice": {"audio/m4a": true, "audio/mp4": true, "audio/aac": true, "audio/x-caf": true, "audio/3gpp": true, "audio/webm": true},
}

type Handler struct {
	Q     *dbgen.Queries
	Store *Store
	Env   config.Env
}

func (h *Handler) Mount(r chi.Router) {
	r.Post("/media/presign", h.presign)
	r.Post("/media/{id}/confirm", h.confirm)
	r.Get("/media/{id}", h.get)
}

type DTO struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	ContentType string  `json:"content_type"`
	Status      string  `json:"status"`
	ByteSize    *int64  `json:"byte_size"`
	Width       *int32  `json:"width"`
	Height      *int32  `json:"height"`
	DurationMs  *int32  `json:"duration_ms"`
	URL         *string `json:"url,omitempty"`
}

func toDTO(m dbgen.Medium) DTO {
	return DTO{
		ID:          pg.UUIDValue(m.ID).String(),
		Kind:        m.Kind,
		ContentType: m.ContentType,
		Status:      m.Status,
		ByteSize:    m.ByteSize,
		Width:       m.Width,
		Height:      m.Height,
		DurationMs:  m.DurationMs,
	}
}

// byteCap and durationCap read the caller's plan limits — the single source
// in internal/billing.
func byteCap(p billing.Plan, kind string) int64 {
	switch kind {
	case "video":
		return p.MaxVideoBytes
	case "voice":
		return p.MaxVoiceBytes
	default:
		return p.MaxPhotoBytes
	}
}

func durationCapMs(p billing.Plan, kind string) int32 {
	switch kind {
	case "video":
		return int32(p.MaxVideoSeconds) * 1000
	case "voice":
		return int32(p.MaxVoiceSeconds) * 1000
	default:
		return 0
	}
}

func (h *Handler) presign(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	var req struct {
		Kind           string  `json:"kind"`
		ContentType    string  `json:"content_type"`
		ByteSize       int64   `json:"byte_size"`
		DurationMs     *int32  `json:"duration_ms"`
		UploadEndpoint *string `json:"upload_endpoint"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}

	allowed, knownKind := allowedTypes[req.Kind]
	if !knownKind {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "kind must be photo, video or voice")
		return
	}
	if !allowed[req.ContentType] {
		httpx.Error(w, http.StatusUnsupportedMediaType, "unsupported_content_type",
			fmt.Sprintf("content type %q is not allowed for kind %s", req.ContentType, req.Kind))
		return
	}
	if req.ByteSize <= 0 {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "byte_size is required")
		return
	}

	user, err := h.Q.GetUserByID(r.Context(), pg.UUID(userID))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "account no longer exists")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	plan := billing.ForUser(user.Plan)

	if cap := byteCap(plan, req.Kind); req.ByteSize > cap {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "media_too_large",
			fmt.Sprintf("%s uploads are limited to %d bytes on your plan", req.Kind, cap))
		return
	}
	if req.Kind == "video" || req.Kind == "voice" {
		if req.DurationMs == nil || *req.DurationMs <= 0 {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "duration_ms is required for video and voice")
			return
		}
		if cap := durationCapMs(plan, req.Kind); *req.DurationMs > cap {
			httpx.Error(w, http.StatusUnprocessableEntity, "duration_exceeds_plan",
				fmt.Sprintf("%s length is limited to %ds on your plan", req.Kind, int(cap/1000)))
			return
		}
	} else if req.DurationMs != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "duration_ms only applies to video and voice")
		return
	}

	key := fmt.Sprintf("media/%s/%s", userID, uuid.New())
	row, err := h.Q.CreateMedia(r.Context(), dbgen.CreateMediaParams{
		OwnerID:     pg.UUID(userID),
		Kind:        req.Kind,
		BucketKey:   key,
		ContentType: req.ContentType,
		DurationMs:  req.DurationMs,
		ByteSize:    &req.ByteSize,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	publicEndpoint := h.Store.publicEndpoint
	if h.Env == config.EnvDevelopment {
		if fromCtx := publicEndpointFromContext(r.Context(), ""); fromCtx != "" {
			publicEndpoint = fromCtx
		} else if req.UploadEndpoint != nil && *req.UploadEndpoint != "" && AllowedDevMediaEndpoint(*req.UploadEndpoint) {
			publicEndpoint = *req.UploadEndpoint
		}
	}

	uploadURL, err := h.Store.PresignPutForPublicEndpoint(r.Context(), publicEndpoint, key, req.ContentType)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"media_id":   pg.UUIDValue(row.ID).String(),
		"upload_url": uploadURL,
		"headers":    map[string]string{"Content-Type": req.ContentType},
		"expires_in": int(SignedURLTTL.Seconds()),
	})
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	row, ok := h.ownedMedia(w, r, userID)
	if !ok {
		return
	}
	if row.Status == "ready" {
		// Idempotent for mobile retries.
		httpx.JSON(w, http.StatusOK, toDTO(row))
		return
	}

	info, err := h.Store.Head(r.Context(), row.BucketKey)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if info == nil {
		httpx.Error(w, http.StatusBadRequest, "media_not_uploaded", "no object found for this media; upload first")
		return
	}

	user, err := h.Q.GetUserByID(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	plan := billing.ForUser(user.Plan)

	// Re-check the *actual* stored object against the declaration and plan
	// caps; a violation destroys both the object and the row.
	if info.ContentType != row.ContentType {
		h.destroy(w, r, row, http.StatusUnsupportedMediaType, "unsupported_content_type",
			"uploaded content type does not match the declared type")
		return
	}
	if row.ByteSize != nil && info.ByteSize != *row.ByteSize {
		h.destroy(w, r, row, http.StatusBadRequest, "media_size_mismatch",
			fmt.Sprintf("uploaded %d bytes but %d were declared at presign", info.ByteSize, *row.ByteSize))
		return
	}
	if cap := byteCap(plan, row.Kind); info.ByteSize > cap {
		h.destroy(w, r, row, http.StatusRequestEntityTooLarge, "media_too_large",
			fmt.Sprintf("uploaded object exceeds the %d byte limit for your plan", cap))
		return
	}

	var width, height *int32
	var thumbKey *string
	if row.Kind == "photo" {
		body, err := h.Store.Open(r.Context(), row.BucketKey)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		defer func() { _ = body.Close() }()
		img, _, err := image.Decode(body)
		if err != nil {
			h.destroy(w, r, row, http.StatusBadRequest, "invalid_image", "uploaded object is not a decodable image")
			return
		}
		b := img.Bounds()
		wpx, hpx := int32(b.Dx()), int32(b.Dy())
		width, height = &wpx, &hpx

		thumbBytes, err := GenerateJPEGThumb(img, ThumbMaxPx)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		key := ThumbKey(userID, pg.UUIDValue(row.ID))
		if err := h.Store.Put(r.Context(), key, "image/jpeg", bytes.NewReader(thumbBytes), int64(len(thumbBytes))); err != nil {
			httpx.InternalError(w, err)
			return
		}
		thumbKey = &key
	}

	ready, err := h.Q.MarkMediaReady(r.Context(), dbgen.MarkMediaReadyParams{
		ID:             row.ID,
		ByteSize:       &info.ByteSize,
		Width:          width,
		Height:         height,
		ThumbBucketKey: thumbKey,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toDTO(ready))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	row, ok := h.ownedMedia(w, r, userID)
	if !ok {
		return
	}
	if row.Status != "ready" {
		httpx.Error(w, http.StatusConflict, "media_not_ready", "media has not been confirmed yet")
		return
	}
	url, err := h.Store.SignedURL(r.Context(), row.BucketKey, SignedURLTTL)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	dto := toDTO(row)
	dto.URL = &url
	httpx.JSON(w, http.StatusOK, dto)
}

// ownedMedia loads the {id} media row and enforces ownership. Non-owners get
// the same 404 as a missing row so media IDs never leak existence.
func (h *Handler) ownedMedia(w http.ResponseWriter, r *http.Request, userID uuid.UUID) (dbgen.Medium, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "media not found")
		return dbgen.Medium{}, false
	}
	row, err := h.Q.GetMediaByID(r.Context(), pg.UUID(id))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && pg.UUIDValue(row.OwnerID) != userID) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "media not found")
		return dbgen.Medium{}, false
	}
	if err != nil {
		httpx.InternalError(w, err)
		return dbgen.Medium{}, false
	}
	return row, true
}

// destroy removes the offending object and row, then writes the rejection.
func (h *Handler) destroy(w http.ResponseWriter, r *http.Request, row dbgen.Medium, status int, code httpx.ErrorCode, msg string) {
	if err := h.Store.Delete(r.Context(), row.BucketKey); err != nil {
		slog.Error("media: deleting rejected object", "key", row.BucketKey, "error", err)
	}
	if row.ThumbBucketKey != nil && *row.ThumbBucketKey != "" {
		if err := h.Store.Delete(r.Context(), *row.ThumbBucketKey); err != nil {
			slog.Error("media: deleting rejected thumb", "key", *row.ThumbBucketKey, "error", err)
		}
	}
	if err := h.Q.DeleteMedia(r.Context(), row.ID); err != nil {
		slog.Error("media: deleting rejected row", "id", pg.UUIDValue(row.ID), "error", err)
	}
	httpx.Error(w, status, code, msg)
}
