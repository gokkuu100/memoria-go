// Package users serves the authenticated account endpoints (/v1/me).
package users

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/media"
	"memoria-backend/internal/pg"
)

type DTO struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Username      string    `json:"username"`
	DisplayName   string    `json:"display_name"`
	Timezone      string    `json:"timezone"`
	Plan          string    `json:"plan"`
	AvatarMediaID *string   `json:"avatar_media_id"`
	AvatarURL     *string   `json:"avatar_url,omitempty"`
	MemberSince   time.Time `json:"member_since"`
}

// ProfileCard is the public-facing slice of a user shown on invite landing and
// friend lists (no email, plan, or other private fields).
type ProfileCard struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"display_name"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
}

func ToProfileCard(u dbgen.User) ProfileCard {
	return ProfileCard{
		ID:          pg.UUIDValue(u.ID).String(),
		Username:    u.Username,
		DisplayName: u.DisplayName,
	}
}

func ToDTO(u dbgen.User) DTO {
	return DTO{
		ID:            pg.UUIDValue(u.ID).String(),
		Email:         u.Email,
		Username:      u.Username,
		DisplayName:   u.DisplayName,
		Timezone:      u.Timezone,
		Plan:          u.Plan,
		AvatarMediaID: pg.UUIDPtr(u.AvatarMediaID),
		MemberSince:   pg.TimeValue(u.CreatedAt),
	}
}

type Handler struct {
	Pool  *pgxpool.Pool
	Q     *dbgen.Queries
	Media *media.Store
}

// Mount attaches the /me routes. The caller wraps this router with the auth
// middleware.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/me", h.getMe)
	r.Patch("/me", h.patchMe)
	r.Put("/me/avatar", h.putAvatar)
	r.Put("/me/push-token", h.putPushToken)
	r.Delete("/me", h.deleteMe)
}

func (h *Handler) getMe(w http.ResponseWriter, r *http.Request) {
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
	dto := ToDTO(user)
	dto.AvatarURL = h.avatarURL(r, user)
	httpx.JSON(w, http.StatusOK, dto)
}

// avatarURL signs a short-lived GET URL for the user's avatar, if set.
// Failures degrade to no URL rather than failing the whole /me response.
func (h *Handler) avatarURL(r *http.Request, user dbgen.User) *string {
	if !user.AvatarMediaID.Valid || h.Media == nil {
		return nil
	}
	row, err := h.Q.GetMediaByID(r.Context(), user.AvatarMediaID)
	if err != nil || row.Status != "ready" {
		return nil
	}
	url, err := h.Media.SignedURL(r.Context(), row.BucketKey, media.SignedURLTTL)
	if err != nil {
		slog.Error("users: signing avatar url", "error", err)
		return nil
	}
	return &url
}

func (h *Handler) putAvatar(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	var req struct {
		MediaID string `json:"media_id"`
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

	row, err := h.Q.GetMediaByID(r.Context(), pg.UUID(mediaID))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && pg.UUIDValue(row.OwnerID) != userID) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "media not found")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if row.Kind != "photo" {
		httpx.Error(w, http.StatusBadRequest, "invalid_avatar_media", "avatar must be a photo")
		return
	}
	if row.Status != "ready" {
		httpx.Error(w, http.StatusConflict, "media_not_ready", "media has not been confirmed yet")
		return
	}

	if err := h.Q.SetUserAvatar(r.Context(), dbgen.SetUserAvatarParams{
		ID:            pg.UUID(userID),
		AvatarMediaID: row.ID,
	}); err != nil {
		httpx.InternalError(w, err)
		return
	}

	user, err := h.Q.GetUserByID(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	dto := ToDTO(user)
	dto.AvatarURL = h.avatarURL(r, user)
	httpx.JSON(w, http.StatusOK, dto)
}

func (h *Handler) patchMe(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	var req struct {
		DisplayName *string `json:"display_name"`
		Timezone    *string `json:"timezone"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	if req.DisplayName != nil && (*req.DisplayName == "" || len(*req.DisplayName) > httpx.MaxDisplayNameLen) {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "display name must be 1-100 characters")
		return
	}
	if req.Timezone != nil {
		if _, err := time.LoadLocation(*req.Timezone); err != nil {
			httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid timezone")
			return
		}
	}
	user, err := h.Q.UpdateUserProfile(r.Context(), dbgen.UpdateUserProfileParams{
		ID:          pg.UUID(userID),
		DisplayName: req.DisplayName,
		Timezone:    req.Timezone,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "account no longer exists")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ToDTO(user))
}

func (h *Handler) putPushToken(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	var req struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	if req.Token == "" || (req.Platform != "ios" && req.Platform != "android") {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "token and platform (ios|android) are required")
		return
	}
	err := h.Q.UpsertPushToken(r.Context(), dbgen.UpsertPushTokenParams{
		UserID:    pg.UUID(userID),
		ExpoToken: req.Token,
		Platform:  req.Platform,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "registered"})
}

// deleteMe soft-deletes the account and permanently retires the username.
// The hard-delete cascade (media objects, memberships) is a B9 background job.
func (h *Handler) deleteMe(w http.ResponseWriter, r *http.Request) {
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

	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	qtx := h.Q.WithTx(tx)
	if err := qtx.RetireUsername(r.Context(), user.Username); err != nil {
		httpx.InternalError(w, err)
		return
	}
	if err := qtx.SoftDeleteUser(r.Context(), user.ID); err != nil {
		httpx.InternalError(w, err)
		return
	}
	if err := qtx.DeleteSessionsForUser(r.Context(), user.ID); err != nil {
		httpx.InternalError(w, err)
		return
	}
	if err := qtx.DeletePushTokensForUser(r.Context(), user.ID); err != nil {
		httpx.InternalError(w, err)
		return
	}
	if err := transferCapsuleAdminOnDelete(r.Context(), qtx, userID); err != nil {
		httpx.InternalError(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.InternalError(w, err)
		return
	}
	ScheduleHardDelete(h.Pool, h.Q, h.Media, userID)
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "account_deleted"})
}

// transferCapsuleAdminOnDelete moves capsule admin to the next accepted member
// when the creator deletes their account (B9 will extend hard-delete cascade).
func transferCapsuleAdminOnDelete(ctx context.Context, q *dbgen.Queries, userID uuid.UUID) error {
	caps, err := q.ListCapsulesOwnedByUser(ctx, pg.UUID(userID))
	if err != nil {
		return err
	}
	for _, cap := range caps {
		next, err := q.GetNextCapsuleAdminCandidate(ctx, dbgen.GetNextCapsuleAdminCandidateParams{
			CapsuleID: cap.ID,
			UserID:    pg.UUID(userID),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err := q.DemoteCapsuleAdmin(ctx, dbgen.DemoteCapsuleAdminParams{
			CapsuleID: cap.ID, UserID: pg.UUID(userID),
		}); err != nil {
			return err
		}
		if err := q.TransferCapsuleAdmin(ctx, dbgen.TransferCapsuleAdminParams{
			CapsuleID: cap.ID, UserID: next.UserID,
		}); err != nil {
			return err
		}
		if err := q.UpdateCapsuleCreator(ctx, dbgen.UpdateCapsuleCreatorParams{
			ID: cap.ID, CreatorID: next.UserID,
		}); err != nil {
			return err
		}
	}
	return nil
}
