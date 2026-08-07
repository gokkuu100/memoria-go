package media

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
)

// StreamReady writes a ready media object's bytes to the response.
//
// We intentionally stream through the API (not 302 to a signed URL): native
// image loaders forward the Authorization header on redirect, and MinIO then
// rejects the signed GET with 400 — which shows up as a black viewer slide.
func StreamReady(w http.ResponseWriter, r *http.Request, store *Store, row dbgen.Medium) {
	if row.Status != "ready" {
		httpx.Error(w, http.StatusConflict, "media_not_ready", "media is not ready")
		return
	}

	body, err := store.Open(r.Context(), row.BucketKey)
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	defer func() { _ = body.Close() }()

	w.Header().Set("Content-Type", row.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	if row.ByteSize != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", *row.ByteSize))
	}

	if _, err := io.Copy(w, body); err != nil {
		slog.Error("media: stream object", "key", row.BucketKey, "error", err)
	}
}
