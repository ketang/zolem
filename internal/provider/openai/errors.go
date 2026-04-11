package openai

import (
	"context"
	"encoding/json"
	"net/http"

	runtimecfg "zolem.dev/zolem/internal/runtime"
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param,omitempty"`
	Code    *string `json:"code"`
}

func writeError(w http.ResponseWriter, status int, errType, message string, code *string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Error: apiError{Message: message, Type: errType, Code: code},
	})
}

func writeUnauthorized(ctx context.Context, w http.ResponseWriter) {
	code := "invalid_api_key"
	writeError(w, http.StatusUnauthorized, "invalid_request_error", "You didn't provide an API key. You need to provide your API key in an Authorization header using Bearer auth.", &code)
}

func writeInvalidRequest(ctx context.Context, w http.ResponseWriter, message string) {
	_ = ctx
	writeError(w, http.StatusBadRequest, "invalid_request_error", message, nil)
}

func writePermissionDenied(w http.ResponseWriter) {
	code := "insufficient_permissions"
	writeError(w, http.StatusForbidden, "invalid_request_error", "You are not allowed to perform this action.", &code)
}

func writeRateLimit(w http.ResponseWriter) {
	code := "rate_limit_exceeded"
	writeError(w, http.StatusTooManyRequests, "rate_limit_error", "Rate limit exceeded.", &code)
}

func writeServerError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "server_error", "The server had an error while processing your request.", nil)
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
		writeInvalidRequest(ctx, w, "Invalid request.")
	case runtimecfg.ErrorTypeRateLimit:
		writeRateLimit(w)
	case runtimecfg.ErrorTypeServerError:
		writeServerError(w)
	default:
		writeServerError(w)
	}
	return true
}
