// Package friends implements mutual connections, invite links, and silent blocks.
//
// Capsule and album invite handlers (B5/B6) MUST call IsBlocked or
// AssertNotBlocked before sending invites — blocked users must never receive
// invites and must not learn they were blocked.
package friends

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/pg"
)

// IsBlocked reports whether a block exists in either direction between a and b.
func IsBlocked(ctx context.Context, q *dbgen.Queries, a, b uuid.UUID) (bool, error) {
	return q.IsBlockedEitherDirection(ctx, dbgen.IsBlockedEitherDirectionParams{
		BlockerID: pg.UUID(a),
		BlockedID: pg.UUID(b),
	})
}

// AssertNotBlocked writes a 404 not_found envelope when a block exists between
// viewer and target, and returns false. Returns true when no block is present.
func AssertNotBlocked(w http.ResponseWriter, ctx context.Context, q *dbgen.Queries, viewer, target uuid.UUID) bool {
	blocked, err := IsBlocked(ctx, q, viewer, target)
	if err != nil {
		httpx.InternalError(w, err)
		return false
	}
	if blocked {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "user not found")
		return false
	}
	return true
}
