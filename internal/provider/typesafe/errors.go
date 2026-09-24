package typesafe

import (
	"context"
	"encoding/json"
	"net/http"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// errorEnvelope is zolem's TypeSafe error shape.
//
// TypeSafe's API reference documents error status codes but no single JSON
// body schema. This nested {"error": {"type", "message"}} envelope is
// synthetic, but the official JavaScript SDK accepts an error object with a
// message. See docs/typesafe.md for provenance and the 400/422 distinction.
type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: apiError{Type: errType, Message: message}})
}

func writeInvalidRequest(w http.ResponseWriter, message string) {
	writeError(w, http.StatusBadRequest, "invalid_request_error", message)
}

func writeUnauthorized(w http.ResponseWriter) {
	writeError(w, http.StatusUnauthorized, "authentication_error", "missing or invalid Authorization header")
}

func writeNotFound(w http.ResponseWriter, message string) {
	writeError(w, http.StatusNotFound, "not_found_error", message)
}

// writeAnswerValidationError reports that a backend produced an answer
// violating the response contract. Per the issue, this is always a 500
// naming the question key and the violated rule — the mock enforcing the
// contract is the point, not a silently wrong answer.
func writeAnswerValidationError(w http.ResponseWriter, err *AnswerValidationError) {
	writeError(w, http.StatusInternalServerError, "contract_violation_error", err.Error())
}

func writeBackendError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, "api_error", err.Error())
}

// writeForcedProfileError serves the deterministic error the `error` backend
// pins for this profile, and reports whether it handled the request.
//
// Status codes mirror the ones confirmed in the issue for 401/429; permission
// and generic-server-error status codes are not documented by TypeSafe and
// are chosen for consistency with the other providers zolem mocks.
func writeForcedProfileError(ctx context.Context, w http.ResponseWriter) bool {
	errorType, ok := runtimecfg.ForcedErrorTypeForRequest(ctx)
	if !ok {
		return false
	}
	switch errorType {
	case runtimecfg.ErrorTypeAuthentication:
		writeUnauthorized(w)
	case runtimecfg.ErrorTypePermission:
		writeError(w, http.StatusForbidden, "permission_error", "permission denied")
	case runtimecfg.ErrorTypeInvalidRequest:
		writeInvalidRequest(w, "invalid request")
	case runtimecfg.ErrorTypeRateLimit:
		writeError(w, http.StatusTooManyRequests, "rate_limit_error", "rate limit exceeded")
	case runtimecfg.ErrorTypeServerError:
		writeError(w, http.StatusInternalServerError, "api_error", "internal server error")
	default:
		writeError(w, http.StatusInternalServerError, "api_error", "internal server error")
	}
	return true
}
