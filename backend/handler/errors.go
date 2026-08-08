package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/luc1ferxx/neural-canvas/backend/logging"
)

// Stable machine-readable error codes.
//
// The point of a code is that a client can branch on it. Before this, the API
// answered with text/plain prose, so the frontend either matched on status alone
// -- which cannot distinguish "username taken" from "password too short", both
// 400 -- or string-matched the message, which silently breaks the moment the
// wording is edited.
const (
	codeInvalidRequest  = "invalid_request"
	codeUnauthorized    = "unauthorized"
	codeSessionRevoked  = "session_revoked"
	codeForbidden       = "forbidden"
	codeNotFound        = "not_found"
	codeUserExists      = "user_exists"
	codeUnsupportedType = "unsupported_media_type"
	codePayloadTooLarge = "payload_too_large"
	codeRateLimited     = "rate_limited"
	codeQuotaExceeded   = "quota_exceeded"
	codeInternal        = "internal"
	codeUpstreamFailed  = "upstream_failed"
	codeUnavailable     = "unavailable"
)

// errorResponse is the single shape every failure takes.
//
// Nested under "error" rather than flattened so a success payload can never be
// mistaken for a failure by a client checking for the key -- posts have a
// "message" field of their own, which a flat {"message": ...} would collide with.
type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	// Code is stable and safe to branch on.
	Code string `json:"code"`
	// Message is human-readable and may be reworded at any time.
	Message string `json:"message"`
	// RequestID matches the X-Request-Id header and every log line this request
	// produced, so a user can quote it and it can be found.
	RequestID string `json:"request_id,omitempty"`
}

// writeError sends a JSON error.
//
// message is shown to the client, so it must never carry internal detail: an
// Elasticsearch URL, a bucket name or a wrapped driver error all leak
// infrastructure. Log the cause separately with logging.FromContext.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	body, err := json.Marshal(errorResponse{Error: errorDetail{
		Code:      code,
		Message:   message,
		RequestID: logging.RequestID(r.Context()),
	}})
	if err != nil {
		// The struct is three strings; this cannot fail. Fall back rather than
		// leave the client with an empty 200.
		logging.FromContext(r.Context()).Error("could not encode error response",
			slog.String("cause", err.Error()))
		http.Error(w, `{"error":{"code":"internal","message":"Something went wrong"}}`,
			http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
