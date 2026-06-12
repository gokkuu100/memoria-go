package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/mailer"
	"memoria-backend/internal/pg"
	"memoria-backend/internal/users"
)

const (
	otpTTL         = 10 * time.Minute
	otpMaxAttempts = 5
)

var usernameRe = regexp.MustCompile(`^[a-z0-9_.]{3,20}$`)

type Handler struct {
	Q       *dbgen.Queries
	Mailer  mailer.Mailer
	Secret  []byte
	Limiter *Limiter
}

func (h *Handler) Mount(r chi.Router) {
	r.Post("/auth/otp/request", h.requestOTP)
	r.Post("/auth/otp/verify", h.verifyOTP)
	r.Post("/auth/signup", h.signup)
	r.Get("/auth/username-available", h.usernameAvailable)
	r.Post("/auth/login", h.login)
	r.Post("/auth/reset-password", h.resetPassword)
	r.Post("/auth/refresh", h.refresh)
	r.Post("/auth/logout", h.logout)
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (h *Handler) issueTokens(ctx context.Context, user dbgen.User, deviceName string) (*tokenPair, error) {
	access, err := SignAccess(h.Secret, pg.UUIDValue(user.ID))
	if err != nil {
		return nil, err
	}
	refresh, hash, err := NewRefreshToken()
	if err != nil {
		return nil, err
	}
	_, err = h.Q.CreateSession(ctx, dbgen.CreateSessionParams{
		UserID:           user.ID,
		RefreshTokenHash: hash,
		DeviceName:       deviceName,
		ExpiresAt:        pg.Time(time.Now().Add(RefreshTTL)),
	})
	if err != nil {
		return nil, err
	}
	return &tokenPair{AccessToken: access, RefreshToken: refresh, ExpiresIn: int(AccessTTL.Seconds())}, nil
}

// POST /v1/auth/otp/request {email, purpose}
func (h *Handler) requestOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email   string `json:"email"`
		Purpose string `json:"purpose"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	email, ok := normalizeEmail(req.Email)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid email address")
		return
	}
	if req.Purpose != TypSignup && req.Purpose != TypReset {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "purpose must be signup or password_reset")
		return
	}
	if !h.Limiter.Allow("otp:email:"+email, OTPEmailLimit, OTPEmailWindow) ||
		!h.Limiter.Allow("otp:ip:"+httpx.ClientIP(r), OTPIPRequest, OTPIPWindow) {
		httpx.Error(w, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many codes requested, try again later")
		return
	}

	taken, err := h.Q.EmailTaken(r.Context(), email)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if req.Purpose == TypSignup && taken {
		httpx.Error(w, http.StatusConflict, "email_taken", "an account with this email already exists")
		return
	}
	if req.Purpose == TypReset && !taken {
		// Don't reveal whether an account exists.
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "sent"})
		return
	}

	code, err := newOTPCode()
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if err := h.Q.DeleteOTPs(r.Context(), dbgen.DeleteOTPsParams{Email: email, Purpose: req.Purpose}); err != nil {
		httpx.InternalError(w, err)
		return
	}
	_, err = h.Q.CreateOTP(r.Context(), dbgen.CreateOTPParams{
		Email:     email,
		CodeHash:  hashOTP(code),
		Purpose:   req.Purpose,
		ExpiresAt: pg.Time(time.Now().Add(otpTTL)),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	subject := "Your Memoria verification code"
	body := fmt.Sprintf("Your Memoria code is %s\n\nIt expires in 10 minutes. If you didn't request this, you can ignore this email.", code)
	if err := h.Mailer.Send(r.Context(), email, subject, body); err != nil {
		httpx.InternalError(w, fmt.Errorf("sending otp email: %w", err))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// POST /v1/auth/otp/verify {email, purpose, code} -> {ticket}
func (h *Handler) verifyOTP(w http.ResponseWriter, r *http.Request) {
	if !h.Limiter.Allow("otp-verify:ip:"+httpx.ClientIP(r), OTPVerifyIP, OTPIPWindow) {
		httpx.Error(w, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many attempts, try again later")
		return
	}
	var req struct {
		Email   string `json:"email"`
		Purpose string `json:"purpose"`
		Code    string `json:"code"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	email, ok := normalizeEmail(req.Email)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid email address")
		return
	}

	otp, err := h.Q.GetActiveOTP(r.Context(), dbgen.GetActiveOTPParams{Email: email, Purpose: req.Purpose})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusBadRequest, "invalid_code", "code is invalid or expired")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	attempts, err := h.Q.IncrementOTPAttempts(r.Context(), otp.ID)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if attempts > otpMaxAttempts {
		httpx.Error(w, http.StatusTooManyRequests, "too_many_attempts", "too many wrong attempts, request a new code")
		return
	}
	if hashOTP(req.Code) != otp.CodeHash {
		httpx.Error(w, http.StatusBadRequest, "invalid_code", "code is invalid or expired")
		return
	}
	if err := h.Q.ConsumeOTP(r.Context(), otp.ID); err != nil {
		httpx.InternalError(w, err)
		return
	}
	ticket, err := SignTicket(h.Secret, req.Purpose, email)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"ticket": ticket})
}

// POST /v1/auth/signup {ticket, password, username, display_name, device_name?}
func (h *Handler) signup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ticket      string `json:"ticket"`
		Password    string `json:"password"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		DeviceName  string `json:"device_name"`
		Timezone    string `json:"timezone"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	claims, err := ParseToken(h.Secret, req.Ticket)
	if err != nil || claims.Typ != TypSignup {
		httpx.Error(w, http.StatusUnauthorized, "invalid_ticket", "signup ticket is invalid or expired")
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 128 {
		httpx.Error(w, http.StatusBadRequest, "weak_password", "password must be 8-128 characters")
		return
	}
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if !usernameRe.MatchString(username) {
		httpx.Error(w, http.StatusBadRequest, "invalid_username", "username must be 3-20 characters: a-z, 0-9, _ or .")
		return
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" || len(displayName) > httpx.MaxDisplayNameLen {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "display name must be 1-100 characters")
		return
	}
	tz := req.Timezone
	if tz == "" {
		tz = "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid timezone")
		return
	}

	taken, err := h.Q.UsernameTaken(r.Context(), username)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if boolVal(taken) {
		httpx.Error(w, http.StatusConflict, "username_taken", "this username is taken")
		return
	}
	emailTaken, err := h.Q.EmailTaken(r.Context(), claims.Email)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if emailTaken {
		httpx.Error(w, http.StatusConflict, "email_taken", "an account with this email already exists")
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	user, err := h.Q.CreateUser(r.Context(), dbgen.CreateUserParams{
		Email:        claims.Email,
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: hash,
		Timezone:     tz,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	tokens, err := h.issueTokens(r.Context(), user, req.DeviceName)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"user": users.ToDTO(user), "tokens": tokens})
}

// GET /v1/auth/username-available?u=
func (h *Handler) usernameAvailable(w http.ResponseWriter, r *http.Request) {
	if !h.Limiter.Allow("uname:ip:"+httpx.ClientIP(r), UsernameIPLimit, UsernameIPWindow) {
		httpx.Error(w, http.StatusTooManyRequests, httpx.CodeRateLimited, "slow down")
		return
	}
	username := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("u")))
	if !usernameRe.MatchString(username) {
		httpx.JSON(w, http.StatusOK, map[string]any{"available": false, "reason": "invalid_format"})
		return
	}
	taken, err := h.Q.UsernameTaken(r.Context(), username)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"available": !boolVal(taken)})
}

// POST /v1/auth/login {email, password, device_name?}
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		DeviceName string `json:"device_name"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	if !h.Limiter.Allow("login:ip:"+httpx.ClientIP(r), LoginIPLimit, LoginIPWindow) {
		httpx.Error(w, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many attempts, try again later")
		return
	}
	email, ok := normalizeEmail(req.Email)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
		return
	}
	user, err := h.Q.GetUserByEmail(r.Context(), email)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	match, err := VerifyPassword(req.Password, user.PasswordHash)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if !match {
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
		return
	}
	tokens, err := h.issueTokens(r.Context(), user, req.DeviceName)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": users.ToDTO(user), "tokens": tokens})
}

// POST /v1/auth/reset-password {ticket, new_password}
func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ticket      string `json:"ticket"`
		NewPassword string `json:"new_password"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	claims, err := ParseToken(h.Secret, req.Ticket)
	if err != nil || claims.Typ != TypReset {
		httpx.Error(w, http.StatusUnauthorized, "invalid_ticket", "reset ticket is invalid or expired")
		return
	}
	if len(req.NewPassword) < 8 || len(req.NewPassword) > 128 {
		httpx.Error(w, http.StatusBadRequest, "weak_password", "password must be 8-128 characters")
		return
	}
	user, err := h.Q.GetUserByEmail(r.Context(), claims.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, "invalid_ticket", "reset ticket is invalid or expired")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if err := h.Q.UpdateUserPassword(r.Context(), dbgen.UpdateUserPasswordParams{ID: user.ID, PasswordHash: hash}); err != nil {
		httpx.InternalError(w, err)
		return
	}
	// A password reset signs out every device.
	if err := h.Q.DeleteSessionsForUser(r.Context(), user.ID); err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "password_updated"})
}

// POST /v1/auth/refresh {refresh_token} — rotates the refresh token.
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	session, err := h.Q.GetSessionByTokenHash(r.Context(), HashToken(req.RefreshToken))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "invalid refresh token")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if time.Now().After(pg.TimeValue(session.ExpiresAt)) {
		_ = h.Q.DeleteSessionByTokenHash(r.Context(), session.RefreshTokenHash)
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "session expired, sign in again")
		return
	}
	user, err := h.Q.GetUserByID(r.Context(), session.UserID)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "account no longer exists")
		return
	}
	newToken, newHash, err := NewRefreshToken()
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	err = h.Q.RotateSession(r.Context(), dbgen.RotateSessionParams{
		ID:                 session.ID,
		RefreshTokenHash:   newHash,
		ExpiresAt:          pg.Time(time.Now().Add(RefreshTTL)),
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	access, err := SignAccess(h.Secret, pg.UUIDValue(user.ID))
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, tokenPair{AccessToken: access, RefreshToken: newToken, ExpiresIn: int(AccessTTL.Seconds())})
}

// POST /v1/auth/logout {refresh_token}
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	if err := h.Q.DeleteSessionByTokenHash(r.Context(), HashToken(req.RefreshToken)); err != nil {
		httpx.InternalError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func normalizeEmail(raw string) (string, bool) {
	addr, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil || addr.Name != "" {
		return "", false
	}
	return strings.ToLower(addr.Address), true
}

func newOTPCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func hashOTP(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// boolVal unwraps sqlc's *bool results for EXISTS queries (never NULL in practice).
func boolVal(b *bool) bool {
	return b != nil && *b
}
