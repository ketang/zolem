package zolemerr

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ketang/zolem/internal/fixture"
)

func Write(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Zolem-Error", "true")
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// WriteFixtureSelectionError reports a fixture selection failure. An exhausted
// on_exhaust: error sequence is written by the provider's own server-error
// writer (HTTP 500 in its native envelope) tagged X-Zolem-Error: true; any
// other selector error (e.g. a failing selector.wasm) becomes a zolem
// infrastructure error via Write.
func WriteFixtureSelectionError(w http.ResponseWriter, err error, exhausted func(http.ResponseWriter, string)) {
	var ee *fixture.ExhaustError
	if errors.As(err, &ee) {
		w.Header().Set("X-Zolem-Error", "true")
		exhausted(w, ee.Error())
		return
	}
	Write(w, "fixture selection failed: "+err.Error())
}
