package server

import (
	"net/http"
	"strings"
	"time"

	"memoria-backend/internal/auth"
	"memoria-backend/internal/config"
	"memoria-backend/internal/httpx"
)

const (
	globalIPLimit   = 100
	globalIPWindow  = time.Minute
	globalUserLimit = 300
	globalUserWindow = time.Minute
)

// RateLimitIP applies the global per-IP sliding-window limit. Health probes are
// excluded so orchestrators are not throttled. Disabled in development so local
// integration tests are not starved on a shared httptest server.
func RateLimitIP(limiter *auth.Limiter, env config.Env) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if env != config.EnvProduction {
				next.ServeHTTP(w, r)
				return
			}
			if r.Method == http.MethodOptions || isHealthProbe(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			ip := httpx.ClientIP(r)
			if !limiter.Allow("global:ip:"+ip, globalIPLimit, globalIPWindow) {
				httpx.Error(w, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many requests")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitUser applies the per-authenticated-user limit. Mount after RequireAuth
// so httpx.UserID is on the request context. Disabled in development.
func RateLimitUser(limiter *auth.Limiter, env config.Env) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if env != config.EnvProduction {
				next.ServeHTTP(w, r)
				return
			}
			userID, ok := httpx.UserID(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			if !limiter.Allow("global:user:"+userID.String(), globalUserLimit, globalUserWindow) {
				httpx.Error(w, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isHealthProbe(path string) bool {
	return path == "/healthz" || path == "/readyz"
}

// ClientIPFromTrustedProxy returns the client IP when the request passes through
// a reverse proxy that sets X-Forwarded-For or X-Real-IP. Use only when the
// proxy strips untrusted values (documented in RUNBOOK.md).
func ClientIPFromTrustedProxy(r *http.Request, fallback string) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip := strings.TrimSpace(strings.Split(xff, ",")[0]); ip != "" {
			return ip
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	return fallback
}
