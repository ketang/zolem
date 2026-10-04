package gemini_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ketang/zolem/internal/provider/gemini"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

type modelsPayload struct {
	Models []struct {
		Name                       string   `json:"name"`
		Version                    string   `json:"version"`
		DisplayName                string   `json:"displayName"`
		InputTokenLimit            int      `json:"inputTokenLimit"`
		OutputTokenLimit           int      `json:"outputTokenLimit"`
		SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
	} `json:"models"`
	NextPageToken *string `json:"nextPageToken"`
}

func getModels(t *testing.T, h *gemini.Handler, path string, rt *runtimecfg.ListenerRuntime, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	if rt != nil {
		req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), *rt))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func modelNames(t *testing.T, rr *httptest.ResponseRecorder) []string {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type: got %q", ct)
	}
	var p modelsPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.NextPageToken == nil || *p.NextPageToken != "" {
		t.Errorf("nextPageToken: want present and empty, got %v", p.NextPageToken)
	}
	var names []string
	for _, m := range p.Models {
		names = append(names, m.Name)
	}
	return names
}

func TestListModels_Catalog(t *testing.T) {
	h := newHandler(t)
	for _, path := range []string{"/v1/models", "/v1beta/models"} {
		rr := getModels(t, h, path, nil, "x-goog-api-key", "k")
		names := modelNames(t, rr)
		for _, want := range []string{"models/gemini-2.5-pro", "models/gemini-2.5-flash", "models/gemini-2.0-flash", "models/gemini-2.0-flash-lite"} {
			found := false
			for _, n := range names {
				found = found || n == want
			}
			if !found {
				t.Errorf("%s: missing %s in %v", path, want, names)
			}
		}
		var p modelsPayload
		_ = json.Unmarshal(rr.Body.Bytes(), &p)
		for _, m := range p.Models {
			if m.Version == "" || m.DisplayName == "" || m.InputTokenLimit == 0 || m.OutputTokenLimit == 0 || len(m.SupportedGenerationMethods) != 2 {
				t.Errorf("%s: incomplete model object %+v", path, m)
			}
		}
	}
}

func TestListModels_KeyQueryAuthAndMissingKey(t *testing.T) {
	h := newHandler(t)
	if rr := getModels(t, h, "/v1beta/models?key=abc", nil); rr.Code != http.StatusOK {
		t.Fatalf("?key= auth: got %d", rr.Code)
	}
	if rr := getModels(t, h, "/v1beta/models", nil); rr.Code != http.StatusForbidden {
		t.Fatalf("missing key: got %d, want 403", rr.Code)
	}
	if rr := getModels(t, h, "/v1beta/models/gemini-2.0-flash", nil); rr.Code != http.StatusForbidden {
		t.Fatalf("missing key (get): got %d, want 403", rr.Code)
	}
}

func TestGetModel(t *testing.T) {
	h := newHandler(t)
	for _, path := range []string{
		"/v1beta/models/gemini-2.0-flash",
		"/v1beta/models/models/gemini-2.0-flash",
		"/v1/models/gemini-2.0-flash",
	} {
		rr := getModels(t, h, path, nil, "x-goog-api-key", "k")
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", path, rr.Code, rr.Body.String())
		}
		var m struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &m)
		if m.Name != "models/gemini-2.0-flash" {
			t.Errorf("%s: name %q", path, m.Name)
		}
	}

	rr := getModels(t, h, "/v1beta/models/no-such-model", nil, "x-goog-api-key", "k")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown: status %d", rr.Code)
	}
	var env struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Status != "NOT_FOUND" {
		t.Errorf("error.status: got %q", env.Error.Status)
	}
}

func rtWith(p runtimecfg.RuntimeProfile) *runtimecfg.ListenerRuntime {
	return &runtimecfg.ListenerRuntime{Profile: p}
}

func TestListModels_PinnedModelFirst(t *testing.T) {
	h := newHandler(t)
	cases := []struct {
		name    string
		profile runtimecfg.RuntimeProfile
		first   string
	}{
		{"force_literal_new", runtimecfg.RuntimeProfile{Backend: "lorem", ResponseModelPolicy: "force_literal", ResponseModel: "pinned-x"}, "models/pinned-x"},
		{"force_backend", runtimecfg.RuntimeProfile{Backend: "lorem", ResponseModelPolicy: "force_backend", BackendModel: "gemini-x"}, "models/gemini-x"},
		{"force_literal_existing", runtimecfg.RuntimeProfile{Backend: "lorem", ResponseModelPolicy: "force_literal", ResponseModel: "gemini-2.5-pro"}, "models/gemini-2.5-pro"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			names := modelNames(t, getModels(t, h, "/v1beta/models", rtWith(tc.profile), "x-goog-api-key", "k"))
			if names[0] != tc.first {
				t.Fatalf("first: got %s, want %s (%v)", names[0], tc.first, names)
			}
			count := 0
			for _, n := range names {
				if n == tc.first {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("%s appears %d times: %v", tc.first, count, names)
			}
		})
	}

	rr := getModels(t, h, "/v1beta/models/pinned-x", rtWith(cases[0].profile), "x-goog-api-key", "k")
	if rr.Code != http.StatusOK {
		t.Fatalf("get pinned: %d", rr.Code)
	}
	var m struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &m)
	if m.Name != "models/pinned-x" {
		t.Errorf("name: %q", m.Name)
	}
}

func TestModels_ForcedErrorBeforeKeyCheck(t *testing.T) {
	h := newHandler(t)
	cases := map[string]int{"rate_limit": http.StatusTooManyRequests, "authentication": http.StatusForbidden}
	for et, want := range cases {
		p := runtimecfg.RuntimeProfile{Backend: "error", ErrorType: et}
		for _, path := range []string{"/v1beta/models", "/v1beta/models/gemini-2.0-flash"} {
			rr := getModels(t, h, path, rtWith(p)) // no key
			if rr.Code != want {
				t.Errorf("%s %s: got %d, want %d", et, path, rr.Code, want)
			}
		}
	}
}

func TestModels_WrongMethodJSON405(t *testing.T) {
	h := newHandler(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodDelete, "/v1beta/models"},
		{http.MethodPut, "/v1beta/models/gemini-2.0-flash"},
		{http.MethodPost, "/v1beta/models"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got %d, want 405", tc.method, tc.path, rr.Code)
			continue
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s %s: content-type %q", tc.method, tc.path, ct)
		}
		var env struct {
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil || env.Error.Code != 405 {
			t.Errorf("%s %s: bad envelope %q", tc.method, tc.path, rr.Body.String())
		}
	}
}
