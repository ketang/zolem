package anthropic

import (
	"encoding/json"
	"net/http"
)

type errorEnvelope struct {
	Type  string   `json:"type"`
	Error apiError `json:"error"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorEnvelope{
		Type:  "error",
		Error: apiError{Type: errType, Message: message},
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	writeError(w, http.StatusUnauthorized, "authentication_error", "invalid x-api-key")
}

func writeInvalidRequest(w http.ResponseWriter, message string) {
	writeError(w, http.StatusBadRequest, "invalid_request_error", message)
}
