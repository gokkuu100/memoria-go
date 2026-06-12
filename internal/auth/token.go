package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	AccessTTL  = 15 * time.Minute
	RefreshTTL = 30 * 24 * time.Hour
	TicketTTL  = 15 * time.Minute

	TypAccess = "access"
	// Tickets are short-lived proofs of OTP verification, exchanged for an
	// account (signup) or a password change (reset).
	TypSignup = "signup"
	TypReset  = "password_reset"
)

type Claims struct {
	jwt.RegisteredClaims
	Typ   string `json:"typ"`
	Email string `json:"email,omitempty"`
}

func SignAccess(secret []byte, userID uuid.UUID) (string, error) {
	return sign(secret, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(AccessTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Typ: TypAccess,
	})
}

func SignTicket(secret []byte, typ, email string) (string, error) {
	return sign(secret, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(TicketTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Typ:   typ,
		Email: email,
	})
}

func sign(secret []byte, claims Claims) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

// ParseToken validates signature and expiry with the algorithm pinned to HS256.
func ParseToken(secret []byte, token string) (*Claims, error) {
	var claims Claims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	return &claims, nil
}

// NewRefreshToken returns an opaque token for the client and the SHA-256 hash
// stored server-side (a DB leak must not leak usable tokens).
func NewRefreshToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
