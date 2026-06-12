package friends

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/media"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/pg"
	"memoria-backend/internal/users"
)

type Handler struct {
	Pool   *pgxpool.Pool
	Q      *dbgen.Queries
	Media  *media.Store
	Notify notifications.Sender
}

func (h *Handler) Mount(r chi.Router) {
	r.Post("/me/invite-link", h.createInviteLink)
	r.Get("/users/by-invite/{token}", h.getByInvite)
	r.Get("/users/{username}", h.getByUsername)

	r.Post("/friends/requests", h.sendRequest)
	r.Get("/friends/requests", h.listRequests)
	r.Post("/friends/requests/{userId}/accept", h.acceptRequest)
	r.Post("/friends/requests/{userId}/decline", h.declineRequest)
	r.Delete("/friends/requests/{userId}", h.cancelRequest)
	r.Get("/friends", h.listFriends)
	r.Delete("/friends/{userId}", h.removeFriend)

	r.Post("/blocks/{userId}", h.blockUser)
	r.Delete("/blocks/{userId}", h.unblockUser)
}

type connectionContext struct {
	Status string `json:"status"`
}

type profileWithConnection struct {
	users.ProfileCard
	Connection *connectionContext `json:"connection,omitempty"`
}

type requestsList struct {
	Incoming []users.ProfileCard `json:"incoming"`
	Outgoing []users.ProfileCard `json:"outgoing"`
}

// POST /v1/me/invite-link — stable shareable token (create or return existing).
func (h *Handler) createInviteLink(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	link, err := h.Q.GetInviteLinkByUser(r.Context(), pg.UUID(userID))
	if errors.Is(err, pgx.ErrNoRows) {
		token, genErr := newInviteToken()
		if genErr != nil {
			httpx.InternalError(w, genErr)
			return
		}
		link, err = h.Q.CreateInviteLink(r.Context(), dbgen.CreateInviteLinkParams{
			Token:  token,
			UserID: pg.UUID(userID),
		})
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
	} else if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"token": link.Token})
}

// GET /v1/users/by-invite/{token}
func (h *Handler) getByInvite(w http.ResponseWriter, r *http.Request) {
	viewerID, _ := httpx.UserID(r.Context())
	token := chi.URLParam(r, "token")
	if token == "" {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "user not found")
		return
	}

	link, err := h.Q.GetInviteLinkByToken(r.Context(), token)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "user not found")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	targetID := pg.UUIDValue(link.UserID)
	if targetID == viewerID {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "user not found")
		return
	}
	if !AssertNotBlocked(w, r.Context(), h.Q, viewerID, targetID) {
		return
	}

	user, err := h.Q.GetUserByID(r.Context(), link.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "user not found")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	card := h.profileCard(r, user)
	httpx.JSON(w, http.StatusOK, card)
}

// GET /v1/users/{username}
func (h *Handler) getByUsername(w http.ResponseWriter, r *http.Request) {
	viewerID, _ := httpx.UserID(r.Context())
	username := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "username")))
	if username == "" {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "user not found")
		return
	}

	user, err := h.Q.GetUserByUsername(r.Context(), username)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "user not found")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	targetID := pg.UUIDValue(user.ID)
	if targetID == viewerID {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "user not found")
		return
	}
	if !AssertNotBlocked(w, r.Context(), h.Q, viewerID, targetID) {
		return
	}

	resp := profileWithConnection{
		ProfileCard: h.profileCard(r, user),
		Connection:  &connectionContext{Status: h.connectionStatus(r.Context(), viewerID, targetID)},
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (h *Handler) connectionStatus(ctx context.Context, viewer, target uuid.UUID) string {
	f, err := h.Q.GetFriendship(ctx, dbgen.GetFriendshipParams{UserA: pg.UUID(viewer), UserB: pg.UUID(target)})
	if errors.Is(err, pgx.ErrNoRows) {
		return "none"
	}
	if err != nil {
		slog.Error("friends: loading connection status", "error", err)
		return "none"
	}
	switch f.Status {
	case "accepted":
		return "accepted"
	case "pending":
		if pg.UUIDValue(f.RequestedBy) == viewer {
			return "pending_outgoing"
		}
		return "pending_incoming"
	default:
		return "none"
	}
}

// POST /v1/friends/requests {user_id}
func (h *Handler) sendRequest(w http.ResponseWriter, r *http.Request) {
	callerID, _ := httpx.UserID(r.Context())
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	targetID, err := uuid.Parse(req.UserID)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "user_id must be a valid UUID")
		return
	}
	if targetID == callerID {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "cannot send a friend request to yourself")
		return
	}

	target, err := h.Q.GetUserByID(r.Context(), pg.UUID(targetID))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "user not found")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if !AssertNotBlocked(w, r.Context(), h.Q, callerID, targetID) {
		return
	}

	existing, err := h.Q.GetFriendship(r.Context(), dbgen.GetFriendshipParams{UserA: pg.UUID(callerID), UserB: pg.UUID(targetID)})
	if err == nil {
		switch existing.Status {
		case "accepted":
			httpx.Error(w, http.StatusConflict, "already_friends", "you are already connected")
			return
		case "pending":
			if pg.UUIDValue(existing.RequestedBy) == callerID {
				httpx.Error(w, http.StatusConflict, "friend_request_exists", "friend request already sent")
			} else {
				httpx.Error(w, http.StatusConflict, "friend_request_incoming", "this user already sent you a request")
			}
			return
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		httpx.InternalError(w, err)
		return
	}

	_, err = h.Q.CreateFriendship(r.Context(), dbgen.CreateFriendshipParams{
		UserA:       pg.UUID(callerID),
		UserB:       pg.UUID(targetID),
		RequestedBy: pg.UUID(callerID),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	if h.Notify != nil {
		h.Notify.FriendRequestReceived(r.Context(), targetID, callerID)
	}

	card := h.profileCard(r, target)
	httpx.JSON(w, http.StatusCreated, card)
}

// POST /v1/friends/requests/{userId}/accept
func (h *Handler) acceptRequest(w http.ResponseWriter, r *http.Request) {
	recipientID, _ := httpx.UserID(r.Context())
	requesterID, ok := parseUserParam(w, r, "userId")
	if !ok {
		return
	}
	if !AssertNotBlocked(w, r.Context(), h.Q, recipientID, requesterID) {
		return
	}

	f, err := h.Q.AcceptFriendship(r.Context(), dbgen.AcceptFriendshipParams{
		Recipient: pg.UUID(recipientID),
		Requester: pg.UUID(requesterID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "friend request not found")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	_ = f

	requester, err := h.Q.GetUserByID(r.Context(), pg.UUID(requesterID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	if h.Notify != nil {
		h.Notify.FriendRequestAccepted(r.Context(), requesterID, recipientID)
	}

	card := h.profileCard(r, requester)
	httpx.JSON(w, http.StatusOK, card)
}

// POST /v1/friends/requests/{userId}/decline
func (h *Handler) declineRequest(w http.ResponseWriter, r *http.Request) {
	recipientID, _ := httpx.UserID(r.Context())
	requesterID, ok := parseUserParam(w, r, "userId")
	if !ok {
		return
	}

	rows, err := h.Q.DeleteIncomingFriendRequest(r.Context(), dbgen.DeleteIncomingFriendRequestParams{
		Recipient: pg.UUID(recipientID),
		Requester: pg.UUID(requesterID),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if rows == 0 {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "friend request not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "declined"})
}

// DELETE /v1/friends/requests/{userId} — cancel outgoing pending request.
func (h *Handler) cancelRequest(w http.ResponseWriter, r *http.Request) {
	requesterID, _ := httpx.UserID(r.Context())
	targetID, ok := parseUserParam(w, r, "userId")
	if !ok {
		return
	}

	rows, err := h.Q.DeleteOutgoingFriendRequest(r.Context(), dbgen.DeleteOutgoingFriendRequestParams{
		Requester: pg.UUID(requesterID),
		Target:    pg.UUID(targetID),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if rows == 0 {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "friend request not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// GET /v1/friends — accepted connections; optional ?q= search among friends.
func (h *Handler) listFriends(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	var (
		rows []dbgen.User
		err  error
	)
	if q == "" {
		rows, err = h.Q.ListAcceptedFriends(r.Context(), pg.UUID(userID))
	} else {
		rows, err = h.Q.SearchAcceptedFriends(r.Context(), dbgen.SearchAcceptedFriendsParams{
			UserA: pg.UUID(userID),
			Query: &q,
		})
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	friends := make([]users.ProfileCard, 0, len(rows))
	for _, u := range rows {
		friends = append(friends, h.profileCard(r, u))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"friends": friends})
}

// GET /v1/friends/requests — incoming and outgoing pending requests.
func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())

	incomingRows, err := h.Q.ListIncomingFriendRequests(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	outgoingRows, err := h.Q.ListOutgoingFriendRequests(r.Context(), pg.UUID(userID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	resp := requestsList{
		Incoming: make([]users.ProfileCard, 0, len(incomingRows)),
		Outgoing: make([]users.ProfileCard, 0, len(outgoingRows)),
	}
	for _, u := range incomingRows {
		resp.Incoming = append(resp.Incoming, h.profileCard(r, u))
	}
	for _, u := range outgoingRows {
		resp.Outgoing = append(resp.Outgoing, h.profileCard(r, u))
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// DELETE /v1/friends/{userId} — remove an accepted connection.
func (h *Handler) removeFriend(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	otherID, ok := parseUserParam(w, r, "userId")
	if !ok {
		return
	}

	rows, err := h.Q.DeleteAcceptedFriendship(r.Context(), dbgen.DeleteAcceptedFriendshipParams{
		UserA: pg.UUID(userID),
		UserB: pg.UUID(otherID),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if rows == 0 {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "connection not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// POST /v1/blocks/{userId} — silent block; removes any friendship/requests.
func (h *Handler) blockUser(w http.ResponseWriter, r *http.Request) {
	blockerID, _ := httpx.UserID(r.Context())
	blockedID, ok := parseUserParam(w, r, "userId")
	if !ok {
		return
	}
	if blockedID == blockerID {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "cannot block yourself")
		return
	}

	if _, err := h.Q.GetUserByID(r.Context(), pg.UUID(blockedID)); errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "user not found")
		return
	} else if err != nil {
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
	if err := qtx.CreateBlock(r.Context(), dbgen.CreateBlockParams{
		BlockerID: pg.UUID(blockerID),
		BlockedID: pg.UUID(blockedID),
	}); err != nil {
		httpx.InternalError(w, err)
		return
	}
	if err := qtx.DeleteFriendship(r.Context(), dbgen.DeleteFriendshipParams{
		UserA: pg.UUID(blockerID),
		UserB: pg.UUID(blockedID),
	}); err != nil {
		httpx.InternalError(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.InternalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /v1/blocks/{userId}
func (h *Handler) unblockUser(w http.ResponseWriter, r *http.Request) {
	blockerID, _ := httpx.UserID(r.Context())
	blockedID, ok := parseUserParam(w, r, "userId")
	if !ok {
		return
	}

	rows, err := h.Q.DeleteBlock(r.Context(), dbgen.DeleteBlockParams{
		BlockerID: pg.UUID(blockerID),
		BlockedID: pg.UUID(blockedID),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if rows == 0 {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "block not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) profileCard(r *http.Request, user dbgen.User) users.ProfileCard {
	card := users.ToProfileCard(user)
	card.AvatarURL = h.avatarURL(r, user)
	return card
}

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
		slog.Error("friends: signing avatar url", "error", err)
		return nil
	}
	return &url
}

func parseUserParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	raw := chi.URLParam(r, name)
	id, err := uuid.Parse(raw)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, name+" must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

func newInviteToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
