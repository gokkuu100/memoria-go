package albums

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/media"
	"memoria-backend/internal/pg"
)

func (h *Handler) streamMemoryMedia(w http.ResponseWriter, r *http.Request) {
	mem, ok := h.loadViewableMemory(w, r)
	if !ok {
		return
	}
	mediaRow, err := h.Q.GetMediaByID(r.Context(), mem.MediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "media not found")
			return
		}
		httpx.InternalError(w, err)
		return
	}
	media.StreamReady(w, r, h.Media, mediaRow)
}

func (h *Handler) streamMemoryVoice(w http.ResponseWriter, r *http.Request) {
	mem, ok := h.loadViewableMemory(w, r)
	if !ok {
		return
	}
	if !mem.VoiceMediaID.Valid {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "voice note not found")
		return
	}
	voiceRow, err := h.Q.GetMediaByID(r.Context(), mem.VoiceMediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "voice note not found")
			return
		}
		httpx.InternalError(w, err)
		return
	}
	media.StreamReady(w, r, h.Media, voiceRow)
}

func (h *Handler) loadViewableMemory(w http.ResponseWriter, r *http.Request) (dbgen.Memory, bool) {
	userID, _ := httpx.UserID(r.Context())
	albumID, ok := h.parseAlbumID(w, r)
	if !ok {
		return dbgen.Memory{}, false
	}
	memoryID, err := uuid.Parse(chi.URLParam(r, "memoryId"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return dbgen.Memory{}, false
	}

	album, _, ok := h.loadAlbumMember(w, r, albumID, userID)
	if !ok {
		return dbgen.Memory{}, false
	}
	if album.State == "deleted" || album.State == "pending" {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "album not found")
		return dbgen.Memory{}, false
	}

	mem, err := h.Q.GetMemoryByID(r.Context(), pg.UUID(memoryID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
			return dbgen.Memory{}, false
		}
		httpx.InternalError(w, err)
		return dbgen.Memory{}, false
	}
	if mem.ContainerType != "album" || pg.UUIDValue(mem.ContainerID) != albumID {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return dbgen.Memory{}, false
	}
	return mem, true
}
