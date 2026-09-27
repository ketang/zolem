package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProviderResponseJSONBodyEncoding covers zolem-jzt: runProviderRequest's
// -json output path must classify the response body as base64 (invalid
// UTF-8), unchanged JSON (valid JSON), or text (anything else, including an
// empty body), in that priority order.
func TestProviderResponseJSONBodyEncoding(t *testing.T) {
	t.Run("empty_body_is_text", func(t *testing.T) {
		out := providerResponseJSON(http.StatusOK, nil)
		if out["body_encoding"] != "text" {
			t.Fatalf("body_encoding = %#v, want %q", out["body_encoding"], "text")
		}
		if out["body"] != "" {
			t.Fatalf("body = %#v, want empty string", out["body"])
		}
	})

	t.Run("invalid_utf8_is_base64", func(t *testing.T) {
		raw := []byte{0xff, 0xfe}
		out := providerResponseJSON(http.StatusOK, raw)
		if out["body_encoding"] != "base64" {
			t.Fatalf("body_encoding = %#v, want %q", out["body_encoding"], "base64")
		}
		got, ok := out["body"].(string)
		if !ok {
			t.Fatalf("body is not a string: %#v", out["body"])
		}
		decoded, err := base64.StdEncoding.DecodeString(got)
		if err != nil {
			t.Fatalf("decode base64 body: %v", err)
		}
		if string(decoded) != string(raw) {
			t.Fatalf("decoded body = %q, want %q", decoded, raw)
		}
	})

	t.Run("quoted_invalid_byte_is_base64_not_raw_json", func(t *testing.T) {
		// {"a":"<0xff>"} is syntactically valid JSON (the quoted byte is just
		// an opaque string byte to the JSON grammar) but is not valid UTF-8.
		// json.Valid would accept it, so the UTF-8 check must run first or
		// the invalid byte would be silently passed through as "valid" JSON.
		raw := []byte(`{"a":"` + string([]byte{0xff}) + `"}`)
		if !json.Valid(raw) {
			t.Fatalf("test fixture is not valid JSON syntax")
		}
		out := providerResponseJSON(http.StatusOK, raw)
		if out["body_encoding"] != "base64" {
			t.Fatalf("body_encoding = %#v, want %q", out["body_encoding"], "base64")
		}
		got, ok := out["body"].(string)
		if !ok {
			t.Fatalf("body is not a string: %#v", out["body"])
		}
		decoded, err := base64.StdEncoding.DecodeString(got)
		if err != nil {
			t.Fatalf("decode base64 body: %v", err)
		}
		if string(decoded) != string(raw) {
			t.Fatalf("decoded body = %q, want exact original bytes %q", decoded, raw)
		}
	})

	t.Run("valid_json_is_unchanged", func(t *testing.T) {
		raw := []byte(`{"object":"chat.completion"}`)
		out := providerResponseJSON(http.StatusOK, raw)
		if _, ok := out["body_encoding"]; ok {
			t.Fatalf("body_encoding should be absent for valid JSON, got %#v", out["body_encoding"])
		}
		data, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal output: %v", err)
		}
		if !strings.Contains(string(data), `"object":"chat.completion"`) {
			t.Fatalf("output does not contain unchanged JSON body: %s", data)
		}
	})

	t.Run("sse_text_body", func(t *testing.T) {
		raw := []byte("data: {\"foo\":1}\n\ndata: [DONE]\n\n")
		out := providerResponseJSON(http.StatusOK, raw)
		if out["body_encoding"] != "text" {
			t.Fatalf("body_encoding = %#v, want %q", out["body_encoding"], "text")
		}
		if out["body"] != string(raw) {
			t.Fatalf("body = %#v, want %q", out["body"], raw)
		}
	})
}

// TestRunProviderRequestJSONOverHTTP exercises the -json request path against
// an httptest.Server, covering the acceptance checks for an empty body and
// invalid-UTF-8 bytes end to end through run().
func TestRunProviderRequestJSONOverHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/empty":
			w.WriteHeader(http.StatusOK)
		case "/binary":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte{0xff, 0xfe})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	t.Run("empty_body", func(t *testing.T) {
		var stdout, stderr strings.Builder
		if err := run(context.Background(), []string{"-json", "-base-url", server.URL, "request", "-path", "/empty"}, &stdout, &stderr); err != nil {
			t.Fatalf("run failed: %v\nstderr: %s", err, stderr.String())
		}
		var envelope struct {
			Status       int    `json:"status"`
			Body         string `json:"body"`
			BodyEncoding string `json:"body_encoding"`
		}
		if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
			t.Fatalf("decode output: %v\n%s", err, stdout.String())
		}
		if envelope.BodyEncoding != "text" || envelope.Body != "" {
			t.Fatalf("empty body output = %+v", envelope)
		}
	})

	t.Run("binary_body", func(t *testing.T) {
		var stdout, stderr strings.Builder
		if err := run(context.Background(), []string{"-json", "-base-url", server.URL, "request", "-path", "/binary"}, &stdout, &stderr); err != nil {
			t.Fatalf("run failed: %v\nstderr: %s", err, stderr.String())
		}
		var envelope struct {
			Status       int    `json:"status"`
			Body         string `json:"body"`
			BodyEncoding string `json:"body_encoding"`
		}
		if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
			t.Fatalf("decode output: %v\n%s", err, stdout.String())
		}
		if envelope.BodyEncoding != "base64" {
			t.Fatalf("binary body output = %+v", envelope)
		}
		decoded, err := base64.StdEncoding.DecodeString(envelope.Body)
		if err != nil || string(decoded) != "\xff\xfe" {
			t.Fatalf("decoded binary body = %q, err=%v", decoded, err)
		}
	})
}
