package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PathUUID parses a chi URL path parameter as a UUID. On failure it writes a
// 400 response and returns false.
func PathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	raw := chi.URLParam(r, name)
	id, err := uuid.Parse(raw)
	if err != nil {
		Error(w, http.StatusBadRequest, CodeBadRequest, name+" must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}
