package billing

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"memoria-backend/internal/config"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/pg"
)

type Handler struct {
	Pool *pgxpool.Pool
	Q    *dbgen.Queries
	Cfg  *config.Config
	// OnPlanUpgrade is called when a webhook upgrade raises the user's plan tier
	// (e.g. to re-stamp capsule viewable_until). Wired from server to avoid
	// an import cycle with capsules.
	OnPlanUpgrade func(ctx context.Context, q *dbgen.Queries, userID uuid.UUID, plan Plan) error
}

func (h *Handler) MountAuthed(r chi.Router) {
	r.Get("/me/subscription", h.getSubscription)
}

func (h *Handler) MountWebhook(r chi.Router) {
	r.Post("/webhooks/revenuecat", h.revenueCatWebhook)
}

type subscriptionResponse struct {
	Plan          string    `json:"plan"`
	PlanExpiresAt *string   `json:"plan_expires_at"`
	Limits        LimitsDTO `json:"limits"`
}

func (h *Handler) getSubscription(w http.ResponseWriter, r *http.Request) {
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

	plan := ForUser(user.Plan)
	resp := subscriptionResponse{
		Plan:   user.Plan,
		Limits: LimitsForPlan(plan),
	}
	if user.PlanExpiresAt.Valid {
		s := pg.TimeValue(user.PlanExpiresAt).UTC().Format(time.RFC3339)
		resp.PlanExpiresAt = &s
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type revenueCatPayload struct {
	Event revenueCatEvent `json:"event"`
}

type revenueCatEvent struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	AppUserID      string          `json:"app_user_id"`
	ProductID      string          `json:"product_id"`
	ExpirationAtMs *int64          `json:"expiration_at_ms"`
	Raw            json.RawMessage `json:"-"`
}

func (h *Handler) revenueCatWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid body")
		return
	}

	if !verifyWebhookAuth(r.Header.Get("Authorization"), h.Cfg.RevenueCatWebhookSecret) {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "invalid webhook authorization")
		return
	}

	var payload revenueCatPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid json")
		return
	}
	ev := payload.Event
	if ev.ID == "" || ev.Type == "" || ev.AppUserID == "" {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "missing required event fields")
		return
	}

	userID, err := uuid.Parse(ev.AppUserID)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "app_user_id must be a user UUID")
		return
	}

	dup, err := h.processRevenueCatEvent(r.Context(), userID, ev, body)
	if err != nil {
		if errors.Is(err, errUnknownProduct) {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "unknown product_id")
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "user not found")
			return
		}
		httpx.InternalError(w, err)
		return
	}

	status := map[string]any{"status": "ok"}
	if dup {
		status["duplicate"] = true
	}
	httpx.JSON(w, http.StatusOK, status)
}

var errUnknownProduct = errors.New("unknown product_id")

func verifyWebhookAuth(header, secret string) bool {
	if secret == "" {
		return false
	}
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	expected := secret
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		header = strings.TrimSpace(header[7:])
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(expected)) == 1
}

func (h *Handler) processRevenueCatEvent(ctx context.Context, userID uuid.UUID, ev revenueCatEvent, raw []byte) (duplicate bool, err error) {
	switch ev.Type {
	case "INITIAL_PURCHASE", "RENEWAL", "CANCELLATION":
		// handled below
	case "EXPIRATION":
		// handled below
	default:
		slog.Debug("billing: ignoring revenuecat event", "type", ev.Type, "id", ev.ID)
		return false, nil
	}

	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := h.Q.WithTx(tx)

	existing, err := qtx.GetSubscriptionEventByRCID(ctx, ev.ID)
	if err == nil {
		_ = existing
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}

	user, err := qtx.GetUserByID(ctx, pg.UUID(userID))
	if err != nil {
		return false, err
	}

	oldPlan := user.Plan
	newPlan, expiresAt, err := h.planFromEvent(ev, user.Plan)
	if err != nil {
		return false, err
	}

	if _, err := qtx.InsertSubscriptionEvent(ctx, dbgen.InsertSubscriptionEventParams{
		UserID:             user.ID,
		RevenuecatEventID:  ev.ID,
		EventType:          ev.Type,
		Plan:               newPlan,
		Raw:                json.RawMessage(raw),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		return false, err
	}

	var planExpires pgtype.Timestamptz
	if expiresAt != nil {
		planExpires = pg.Time(*expiresAt)
	}

	updated, err := qtx.UpdateUserPlan(ctx, dbgen.UpdateUserPlanParams{
		ID:            user.ID,
		Plan:          newPlan,
		PlanExpiresAt: planExpires,
	})
	if err != nil {
		return false, err
	}

	if IsUpgrade(oldPlan, updated.Plan) && h.OnPlanUpgrade != nil {
		if err := h.OnPlanUpgrade(ctx, qtx, userID, ForUser(updated.Plan)); err != nil {
			return false, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return false, nil
}

func (h *Handler) planFromEvent(ev revenueCatEvent, currentPlan string) (plan string, expiresAt *time.Time, err error) {
	switch ev.Type {
	case "EXPIRATION":
		return PlanSpark, nil, nil
	case "INITIAL_PURCHASE", "RENEWAL", "CANCELLATION":
		plan, err := h.productToPlan(ev.ProductID)
		if err != nil {
			return "", nil, err
		}
		if ev.Type == "CANCELLATION" && plan == PlanSpark && currentPlan != PlanSpark {
			plan = currentPlan
		}
		exp := msToTime(ev.ExpirationAtMs)
		return plan, exp, nil
	default:
		return currentPlan, msToTime(ev.ExpirationAtMs), nil
	}
}

func (h *Handler) productToPlan(productID string) (string, error) {
	switch productID {
	case h.Cfg.RevenueCatProductPlus:
		return PlanPlus, nil
	case h.Cfg.RevenueCatProductPro:
		return PlanPro, nil
	default:
		return "", errUnknownProduct
	}
}

func msToTime(ms *int64) *time.Time {
	if ms == nil || *ms <= 0 {
		return nil
	}
	t := time.UnixMilli(*ms).UTC()
	return &t
}
