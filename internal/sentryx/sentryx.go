// Package sentryx wires optional Sentry error reporting. No-op when SENTRY_DSN
// is unset.
package sentryx

import (
	"log/slog"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
)

// Init configures the global Sentry hub when SENTRY_DSN is set. Returns a flush
// function to call on shutdown (safe to call when disabled).
func Init() (flush func(), enabled bool) {
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		return func() {}, false
	}
	env := os.Getenv("ENV")
	if env == "" {
		env = "development"
	}
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      env,
		TracesSampleRate: 0.1,
	}); err != nil {
		slog.Error("sentry: init failed", "error", err)
		return func() {}, false
	}
	slog.Info("sentry: enabled", "environment", env)
	return func() {
		sentry.Flush(2 * time.Second)
	}, true
}

// Capture reports err to Sentry when the hub is configured.
func Capture(err error) {
	if err == nil {
		return
	}
	if hub := sentry.CurrentHub(); hub != nil && hub.Client() != nil {
		hub.CaptureException(err)
	}
}
