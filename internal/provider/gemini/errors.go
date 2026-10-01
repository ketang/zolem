package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ketang/zolem/internal/fixture"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/zolemerr"
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Status  string      `json:"status"`
	Details []apiDetail `json:"details,omitempty"`
}

type apiDetail struct {
	Type     string            `json:"@type"`
	Reason   string            `json:"reason,omitempty"`
	Domain   string            `json:"domain,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func writeError(w http.ResponseWriter, code int, status, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Error: apiError{Code: code, Message: message, Status: status},
	})
}

func writeForbidden(ctx context.Context, w http.ResponseWriter) {
	_ = ctx
	writeDetailedError(w, http.StatusForbidden, "PERMISSION_DENIED", "API key not valid. Please pass a valid API key.", []apiDetail{{
		Type:   "type.googleapis.com/google.rpc.ErrorInfo",
		Reason: "API_KEY_INVALID",
		Domain: "googleapis.com",
		Metadata: map[string]string{
			"service": "generativelanguage.googleapis.com",
		},
	}})
}

func writeInvalidRequest(ctx context.Context, w http.ResponseWriter, message string) {
	_ = ctx
	writeDetailedError(w, http.StatusBadRequest, "INVALID_ARGUMENT", message, []apiDetail{{
		Type:   "type.googleapis.com/google.rpc.BadRequest",
		Reason: "REQUEST_VALIDATION_FAILED",
		Domain: "generativelanguage.googleapis.com",
	}})
}

func writeDetailedError(w http.ResponseWriter, code int, status, message string, details []apiDetail) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Error: apiError{Code: code, Message: message, Status: status, Details: details},
	})
}

func writePermissionDenied(w http.ResponseWriter) {
	writeDetailedError(w, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied.", []apiDetail{{
		Type:   "type.googleapis.com/google.rpc.ErrorInfo",
		Reason: "ACCESS_DENIED",
		Domain: "googleapis.com",
		Metadata: map[string]string{
			"service": "generativelanguage.googleapis.com",
		},
	}})
}

func writeRateLimit(w http.ResponseWriter) {
	writeDetailedError(w, http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", "Rate limit exceeded.", []apiDetail{{
		Type:   "type.googleapis.com/google.rpc.ErrorInfo",
		Reason: "RATE_LIMIT_EXCEEDED",
		Domain: "googleapis.com",
		Metadata: map[string]string{
			"service": "generativelanguage.googleapis.com",
		},
	}})
}

func writeServerError(w http.ResponseWriter) {
	writeDetailedError(w, http.StatusInternalServerError, "INTERNAL", "Internal server error.", []apiDetail{{
		Type:   "type.googleapis.com/google.rpc.ErrorInfo",
		Reason: "INTERNAL",
		Domain: "googleapis.com",
		Metadata: map[string]string{
			"service": "generativelanguage.googleapis.com",
		},
	}})
}

func writeBackendError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusBadGateway, "INTERNAL", err.Error())
}

func writeForcedProfileError(ctx context.Context, w http.ResponseWriter) bool {
	errorType, ok := runtimecfg.ForcedErrorTypeForRequest(ctx)
	if !ok {
		return false
	}
	switch errorType {
	case runtimecfg.ErrorTypeAuthentication:
		writeForbidden(ctx, w)
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

// writeFixtureExhausted serves the provider's server-error envelope (HTTP 500)
// for an exhausted on_exhaust: error sequence, tagged X-Zolem-Error: true.
func writeFixtureExhausted(w http.ResponseWriter, err error) {
	w.Header().Set("X-Zolem-Error", "true")
	writeDetailedError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
}

// writeFixtureSelectionError reports a fixture selection failure: an exhausted
// on_exhaust: error sequence becomes a provider-native 500, any other selector
// error (e.g. a failing selector.wasm) becomes a zolem infrastructure error.
func writeFixtureSelectionError(w http.ResponseWriter, err error) {
	var ee *fixture.ExhaustError
	if errors.As(err, &ee) {
		writeFixtureExhausted(w, ee)
		return
	}
	zolemerr.Write(w, "fixture selection failed: "+err.Error())
}
