// Package httpx contains shared HTTP response helpers: the JSON writer and
// the stable error envelope used by every handler.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"memoria-backend/internal/sentryx"
)

// ErrorCode is a stable, machine-readable error identifier. The mobile app
// maps these to user-facing strings, so codes must never change meaning.
type ErrorCode string

const (
	CodeInternal     ErrorCode = "internal_error"
	CodeNotFound     ErrorCode = "not_found"
	CodeBadRequest   ErrorCode = "bad_request"
	CodeUnauthorized ErrorCode = "unauthorized"
	CodeForbidden    ErrorCode = "forbidden"
	CodeRateLimited  ErrorCode = "rate_limited"
)

type errorBody struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

// JSON writes v as a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("httpx: encoding response failed", "error", err)
	}
}

// Error writes the standard error envelope: {"error":{"code","message"}}.
func Error(w http.ResponseWriter, status int, code ErrorCode, message string) {
	JSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

// InternalError logs the underlying error and writes a generic 500 envelope
// so internals never leak to clients.
func InternalError(w http.ResponseWriter, err error) {
	slog.Error("internal server error", "error", err)
	sentryx.Capture(err)
	Error(w, http.StatusInternalServerError, CodeInternal, "something went wrong")
}
