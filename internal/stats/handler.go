// Package stats serves profile contribution analytics (/v1/me/stats).
package stats

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"memoria-backend/internal/billing"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/pg"
)

type Handler struct {
	Q *dbgen.Queries
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/me/stats/heatmap", h.heatmap)
	r.Get("/me/stats/lifetime", h.lifetime)
}

type heatmapDay struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

func (h *Handler) heatmap(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	user, err := h.Q.GetUserByID(r.Context(), pg.UUID(userID))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "account no longer exists")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	loc, err := time.LoadLocation(user.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	toDay := startOfDay(now)
	fromDay := parseDayQuery(r.URL.Query().Get("from"), toDay.AddDate(0, -3, 0))
	toQuery := parseDayQuery(r.URL.Query().Get("to"), toDay)
	if toQuery.After(toDay) {
		toQuery = toDay
	}
	if fromDay.After(toQuery) {
		fromDay = toQuery
	}

	plan := billing.ForUser(user.Plan)
	if !billing.IsUnlimited(plan.HeatmapHistoryDays) {
		earliest := toDay.AddDate(0, 0, -plan.HeatmapHistoryDays)
		if fromDay.Before(earliest) {
			fromDay = earliest
		}
	}

	fromTS := time.Date(fromDay.Year(), fromDay.Month(), fromDay.Day(), 0, 0, 0, 0, loc).UTC()
	toTS := time.Date(toQuery.Year(), toQuery.Month(), toQuery.Day(), 0, 0, 0, 0, loc).Add(24 * time.Hour).UTC()

	albumRows, err := h.Q.HeatmapAlbumContributions(r.Context(), dbgen.HeatmapAlbumContributionsParams{
		AuthorID: pg.UUID(userID),
		Tz:       user.Timezone,
		FromTs:   pg.Time(fromTS),
		ToTs:     pg.Time(toTS),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	capsuleRows, err := h.Q.HeatmapCapsuleContributions(r.Context(), dbgen.HeatmapCapsuleContributionsParams{
		AuthorID: pg.UUID(userID),
		Tz:       user.Timezone,
		FromDay:  pg.Date(fromDay),
		ToDay:    pg.Date(toQuery),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	counts := map[string]int64{}
	for _, row := range albumRows {
		if row.Day.Valid {
			counts[row.Day.Time.Format("2006-01-02")] += row.Count
		}
	}
	for _, row := range capsuleRows {
		if row.Day.Valid {
			counts[row.Day.Time.Format("2006-01-02")] += row.Count
		}
	}

	days := make([]heatmapDay, 0, len(counts))
	for d := fromDay; !d.After(toQuery); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if c := counts[key]; c > 0 {
			days = append(days, heatmapDay{Date: key, Count: c})
		}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"from": fromDay.Format("2006-01-02"),
		"to":   toQuery.Format("2006-01-02"),
		"days": days,
	})
}

func (h *Handler) lifetime(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	user, err := h.Q.GetUserByID(r.Context(), pg.UUID(userID))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "account no longer exists")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	capsules, err := h.Q.CountCapsulesJoined(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	albums, err := h.Q.CountAlbumsJoined(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	byKind, err := h.Q.CountUserMemoriesByKind(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	resp := map[string]any{
		"capsules_joined": capsules,
		"albums":          albums,
		"memories":        byKind.Total,
		"photos":          byKind.Photos,
		"videos":          byKind.Videos,
		"voice_notes":     byKind.VoiceNotes,
	}

	month, err := h.Q.MostActiveMonth(r.Context(), dbgen.MostActiveMonthParams{
		AuthorID: pg.UUID(userID),
		Tz:       user.Timezone,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		httpx.InternalError(w, err)
		return
	}
	if err == nil && month.Month != "" {
		resp["most_active_month"] = month.Month
	}

	httpx.JSON(w, http.StatusOK, resp)
}

func parseDayQuery(raw string, fallback time.Time) time.Time {
	if raw == "" {
		return fallback
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return fallback
	}
	return t
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
