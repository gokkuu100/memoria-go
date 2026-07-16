package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"memoria-backend/internal/httpx"
)

// Handler serves client event ingest and the founder metrics summary.
type Handler struct {
	Pool    *pgxpool.Pool
	Tracker *Tracker
	// MetricsKey protects GET /metrics/summary. Empty disables the route in production wiring.
	MetricsKey string
}

func (h *Handler) MountAuthed(r chi.Router) {
	r.Post("/analytics/events", h.ingest)
}

func (h *Handler) MountPublic(r chi.Router) {
	if h.MetricsKey == "" {
		return
	}
	r.Get("/metrics/summary", h.summary)
}

type clientEventIn struct {
	Name       string         `json:"name"`
	OccurredAt *time.Time     `json:"occurred_at"`
	Properties map[string]any `json:"properties"`
}

type ingestReq struct {
	Events []clientEventIn `json:"events"`
}

const maxBatch = 50

func (h *Handler) ingest(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	var req ingestReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	if len(req.Events) == 0 {
		httpx.JSON(w, http.StatusAccepted, map[string]any{"accepted": 0})
		return
	}
	if len(req.Events) > maxBatch {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "max 50 events per request")
		return
	}

	accepted := 0
	uid := userID
	now := time.Now().UTC()
	for _, e := range req.Events {
		if !IsClientEventAllowed(e.Name) {
			continue
		}
		occurred := now
		if e.OccurredAt != nil && !e.OccurredAt.IsZero() {
			// Reject far-future / ancient timestamps; clamp to now.
			occurred = e.OccurredAt.UTC()
			if occurred.After(now.Add(5 * time.Minute)) {
				occurred = now
			}
			if occurred.Before(now.Add(-30 * 24 * time.Hour)) {
				occurred = now
			}
		}
		if h.Tracker != nil {
			h.Tracker.Enqueue(Event{
				UserID:     &uid,
				Name:       e.Name,
				Properties: SanitizeProps(e.Properties),
				OccurredAt: occurred,
				Source:     SourceClient,
			})
		}
		accepted++
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"accepted": accepted})
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("X-Metrics-Key")
	if key == "" || key != h.MetricsKey {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "invalid metrics key")
		return
	}
	days := 90
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 365 {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "days must be 1-365")
			return
		}
		days = n
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	out, err := ComputeSummary(ctx, h.Pool, days)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// EncodeProps is a tiny helper for tests.
func EncodeProps(m map[string]any) json.RawMessage {
	b, _ := json.Marshal(SanitizeProps(m))
	return b
}

// UserPtr returns a pointer for Track helpers in tests.
func UserPtr(id uuid.UUID) *uuid.UUID { return &id }
