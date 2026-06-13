package capsules

import (
	"errors"
	"net/http"
	"time"

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
	capID, ok := h.parseCapsuleID(w, r)
	if !ok {
		return dbgen.Memory{}, false
	}
	memoryID, err := uuid.Parse(chi.URLParam(r, "memoryId"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return dbgen.Memory{}, false
	}

	cap, member, ok := h.loadCapsuleMember(w, r, capID, userID)
	if !ok {
		return dbgen.Memory{}, false
	}
	if IsSealed(cap) {
		httpx.Error(w, http.StatusForbidden, "capsule_sealed", "capsule contents are sealed until unlock")
		return dbgen.Memory{}, false
	}
	if !CanViewMemories(cap, member, time.Now().UTC()) {
		if member.ViewBlocked {
			httpx.Error(w, http.StatusForbidden, "view_blocked", "you are blocked from viewing this capsule")
			return dbgen.Memory{}, false
		}
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden, "capsule is not viewable")
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
	if mem.ContainerType != "capsule" || pg.UUIDValue(mem.ContainerID) != capID {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "memory not found")
		return dbgen.Memory{}, false
	}
	return mem, true
}
