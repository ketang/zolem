package gemini

import (
	"encoding/json"
	"net/http"
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func writeError(w http.ResponseWriter, code int, status, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(errorEnvelope{
		Error: apiError{Code: code, Message: message, Status: status},
	})
}

func writeForbidden(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "API key not valid. Please pass a valid API key.")
}

func writeInvalidRequest(w http.ResponseWriter, message string) {
	writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", message)
}
