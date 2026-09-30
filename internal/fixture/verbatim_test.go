package fixture

import (
	"net/http/httptest"
	"testing"
)

func TestPatchModel(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"2xx patched, keys kept", 200, `{"model":"x","extra":{"a":1}}`, `{"extra":{"a":1},"model":"m"}`},
		{"html chars not escaped", 200, `{"model":"x","t":"a<b> & c"}`, `{"model":"m","t":"a<b> & c"}`},
		{"non-2xx untouched", 429, `{"model":"x"}`, `{"model":"x"}`},
		{"no model key untouched", 200, `{"id":"x"}`, `{"id":"x"}`},
		{"array untouched", 200, `[1,2]`, `[1,2]`},
		{"invalid untouched", 200, `nope`, `nope`},
		{"null untouched", 200, `null`, `null`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(PatchModel(c.status, []byte(c.body), "model", "m")); got != c.want {
				t.Fatalf("got %s want %s", got, c.want)
			}
		})
	}
}

func TestWriteVerbatim(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteVerbatim(rec, 529, []byte(`{"type":"error"}`), "model", "m")
	if rec.Code != 529 || rec.Body.String() != `{"type":"error"}` || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("got %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}
}
