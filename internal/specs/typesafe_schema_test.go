package specs_test

import (
	"strconv"
	"testing"

	"github.com/ketang/zolem/internal/specs"
)

// TestVendoredFallbacks_TypesafeSnapshot pins the request-validation
// invariants the issue's acceptance checks require: a choice with 256
// options, a score with 1 level, an unknown question type, and an empty
// questions map are each rejected, while a well-formed three-question
// request (one of each type) is accepted.
func TestVendoredFallbacks_TypesafeSnapshot(t *testing.T) {
	fallbacks := specs.VendoredFallbacks()
	data, ok := fallbacks["typesafe:v1"]
	if !ok {
		t.Fatal("missing typesafe vendored snapshot")
	}

	validator := specs.NewValidator()
	if err := specs.LoadProviderSchema(validator, "typesafe", "v1", data); err != nil {
		t.Fatalf("load typesafe vendored snapshot: %v", err)
	}

	accepted := []struct {
		name string
		body string
	}{
		{"noul", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":"?"}}}`},
		{"noul with criteria", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":"?","criteria":{"true":"yes","false":"no"}}}}`},
		{"choice", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"choice","instructions":"?","criteria":{"a":"A","b":"B"}}}}`},
		{"score", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"score","instructions":"?","criteria":["low","high"]}}}`},
		{"score object levels", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"score","instructions":"?","criteria":[{"what":"low","examples":"e.g. 1"},{"what":"high"}]}}}`},
		{"state as object", `{"model":"jev-latest","state":{"k":"v"},"questions":{"q":{"type":"noul","instructions":{"nested":true}}}}`},
		{"state as array", `{"model":"jev-latest","state":[1,2,3],"questions":{"q":{"type":"noul","instructions":"?"}}}`},
		{"score at 10 levels", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"score","instructions":"?","criteria":["1","2","3","4","5","6","7","8","9","10"]}}}`},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			if err := validator.Validate("typesafe", "v1", []byte(tc.body)); err != nil {
				t.Fatalf("expected %s to be accepted, got %v", tc.name, err)
			}
		})
	}

	rejected := []struct {
		name string
		body string
	}{
		{"missing model", `{"state":"x","questions":{"q":{"type":"noul","instructions":"?"}}}`},
		{"missing state", `{"model":"jev-latest","questions":{"q":{"type":"noul","instructions":"?"}}}`},
		{"empty questions", `{"model":"jev-latest","state":"x","questions":{}}`},
		{"unknown question type", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"maybe","instructions":"?"}}}`},
		{"choice missing criteria", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"choice","instructions":"?"}}}`},
		{"choice with 1 option", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"choice","instructions":"?","criteria":{"a":"A"}}}}`},
		{"choice with 256 options", choice256OptionsBody()},
		{"score missing criteria", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"score","instructions":"?"}}}`},
		{"score with 1 level", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"score","instructions":"?","criteria":["only"]}}}`},
		{"score with 11 levels", `{"model":"jev-latest","state":"x","questions":{"q":{"type":"score","instructions":"?","criteria":["1","2","3","4","5","6","7","8","9","10","11"]}}}`},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			if err := validator.Validate("typesafe", "v1", []byte(tc.body)); err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
		})
	}
}

func choice256OptionsBody() string {
	criteria := "{"
	for i := 0; i < 256; i++ {
		if i > 0 {
			criteria += ","
		}
		criteria += `"opt` + strconv.Itoa(i) + `":"description"`
	}
	criteria += "}"
	return `{"model":"jev-latest","state":"x","questions":{"q":{"type":"choice","instructions":"?","criteria":` + criteria + `}}}`
}
