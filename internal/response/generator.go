package response

import (
	"encoding/json"
	"net/http"
)

// Generator produces deterministic token slices for synthetic responses.
type Generator interface {
	Generate(n int) []string
}

func CountNonEmpty(chunks []string) int {
	n := 0
	for _, chunk := range chunks {
		if chunk != "" {
			n++
		}
	}
	return n
}

func WriteZolemError(w http.ResponseWriter, message string) {
	w.Header().Set("X-Zolem-Error", "true")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]string{"zolem_error": message})
}
