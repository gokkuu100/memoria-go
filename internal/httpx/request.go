package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/google/uuid"
)

// Decode reads a JSON body into v with a 1 MB cap.
func Decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}

// ClientIP returns the remote address without the port. Behind the production
// reverse proxy this is the proxy-provided peer address (configured in B11).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type ctxKey int

const userIDKey ctxKey = iota

func WithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

// UserID returns the authenticated user's ID set by the auth middleware.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userIDKey).(uuid.UUID)
	return id, ok
}
