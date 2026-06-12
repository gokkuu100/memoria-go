package auth

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"memoria-backend/internal/httpx"
)

// RequireAuth validates the bearer access token and puts the user ID on the
// request context (read via httpx.UserID).
func RequireAuth(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || token == "" {
				httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "missing bearer token")
				return
			}
			claims, err := ParseToken(secret, token)
			if err != nil || claims.Typ != TypAccess {
				httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "invalid or expired token")
				return
			}
			userID, err := uuid.Parse(claims.Subject)
			if err != nil {
				httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "invalid or expired token")
				return
			}
			next.ServeHTTP(w, r.WithContext(httpx.WithUserID(r.Context(), userID)))
		})
	}
}
