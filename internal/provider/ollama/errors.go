package ollama

import (
	"context"
	"encoding/json"
	"net/http"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// errorEnvelope is Ollama's error shape: a flat {"error": "message"} object,
// not the nested {"error": {"message": ...}} envelope OpenAI and Anthropic use.
type errorEnvelope struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: message})
}

func writeInvalidRequest(w http.ResponseWriter, message string) {
	writeError(w, http.StatusBadRequest, message)
}

func writeNotFound(w http.ResponseWriter, message string) {
	writeError(w, http.StatusNotFound, message)
}

// writeBackendError reports an upstream generation failure. Ollama surfaces
// these as 500s rather than 502s; it is the origin server, not a gateway.
func writeBackendError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, err.Error())
}

// writeForcedProfileError serves the deterministic error the `error` backend
// pins for this profile, and reports whether it handled the request.
//
// Ollama is unauthenticated, so authentication and permission are not states a
// real Ollama reaches on /api/chat. They are mapped anyway so the `error`
// backend behaves uniformly across all four providers — this is deliberate,
// not an oversight.
func writeForcedProfileError(ctx context.Context, w http.ResponseWriter) bool {
	errorType, ok := runtimecfg.ForcedErrorTypeForRequest(ctx)
	if !ok {
		return false
	}
	switch errorType {
	case runtimecfg.ErrorTypeAuthentication:
		writeError(w, http.StatusUnauthorized, "authorization failed")
	case runtimecfg.ErrorTypePermission:
		writeError(w, http.StatusForbidden, "access denied")
	case runtimecfg.ErrorTypeInvalidRequest:
		writeError(w, http.StatusBadRequest, "invalid request")
	case runtimecfg.ErrorTypeRateLimit:
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
	case runtimecfg.ErrorTypeServerError:
		writeError(w, http.StatusInternalServerError, "server error")
	default:
		writeError(w, http.StatusInternalServerError, "server error")
	}
	return true
}
