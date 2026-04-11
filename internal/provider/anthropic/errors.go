package anthropic

import (
	"context"
	"encoding/json"
	"net/http"

	runtimecfg "zolem.dev/zolem/internal/runtime"
)

type errorEnvelope struct {
	Type      string   `json:"type"`
	Error     apiError `json:"error"`
	RequestID string   `json:"request_id,omitempty"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Type:  "error",
		Error: apiError{Type: errType, Message: message},
	})
}

func writeUnauthorized(ctx context.Context, w http.ResponseWriter) {
	_ = ctx
	writeErrorWithRequestID(w, http.StatusUnauthorized, "authentication_error", "invalid x-api-key", "req_zolem_auth")
}

func writeInvalidRequest(ctx context.Context, w http.ResponseWriter, message string) {
	_ = ctx
	writeErrorWithRequestID(w, http.StatusBadRequest, "invalid_request_error", message, "req_zolem_invalid")
}

func writeErrorWithRequestID(w http.ResponseWriter, status int, errType, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Type:      "error",
		RequestID: requestID,
		Error:     apiError{Type: errType, Message: message},
	})
}

func writePermissionDenied(w http.ResponseWriter) {
	writeErrorWithRequestID(w, http.StatusForbidden, "permission_error", "permission denied", "req_zolem_permission")
}

func writeRateLimit(w http.ResponseWriter) {
	writeErrorWithRequestID(w, http.StatusTooManyRequests, "rate_limit_error", "rate limit exceeded", "req_zolem_rate_limit")
}

func writeServerError(w http.ResponseWriter) {
	writeErrorWithRequestID(w, http.StatusInternalServerError, "api_error", "internal server error", "req_zolem_server")
}

func writeForcedProfileError(ctx context.Context, w http.ResponseWriter) bool {
	errorType, ok := runtimecfg.ForcedErrorTypeForRequest(ctx)
	if !ok {
		return false
	}
	switch errorType {
	case runtimecfg.ErrorTypeAuthentication:
		writeUnauthorized(ctx, w)
	case runtimecfg.ErrorTypePermission:
		writePermissionDenied(w)
	case runtimecfg.ErrorTypeInvalidRequest:
		writeInvalidRequest(ctx, w, "invalid request")
	case runtimecfg.ErrorTypeRateLimit:
		writeRateLimit(w)
	case runtimecfg.ErrorTypeServerError:
		writeServerError(w)
	default:
		writeServerError(w)
	}
	return true
}
