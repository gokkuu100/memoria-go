package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/getsentry/sentry-go"

	"memoria-backend/internal/httpx"
)

// panicRecoverer logs panics, reports to Sentry when configured, and returns 500.
func panicRecoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				slog.Error("panic recovered",
					"panic", rec,
					"path", r.URL.Path,
					"method", r.Method,
					"stack", string(stack),
				)
				if hub := sentry.CurrentHub(); hub != nil {
					hub.WithScope(func(scope *sentry.Scope) {
						scope.SetTag("path", r.URL.Path)
						scope.SetTag("method", r.Method)
						hub.RecoverWithContext(r.Context(), fmt.Errorf("%v", rec))
					})
				}
				httpx.Error(w, http.StatusInternalServerError, httpx.CodeInternal, "something went wrong")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
