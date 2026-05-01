package zolemerr

import (
	"encoding/json"
	"net/http"
)

func Write(w http.ResponseWriter, message string) {
	w.Header().Set("X-Zolem-Error", "true")
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]string{"zolem_error": message})
}
