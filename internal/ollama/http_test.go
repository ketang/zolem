package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPChatCompletion_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] != false {
			t.Errorf("expected stream=false, got %v", req["stream"])
		}
		if req["model"] != "gemma3:4b" {
			t.Errorf("expected model=gemma3:4b, got %v", req["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "Hello from ollama"}},
			},
		})
	}))
	defer srv.Close()
	text, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hello from ollama" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestHTTPChatCompletion_ConnectionRefused(t *testing.T) {
	_, err := HTTPChatCompletion(context.Background(), "http://127.0.0.1:1", []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if !strings.Contains(err.Error(), "ollama backend unavailable") {
		t.Fatalf("expected 'ollama backend unavailable', got: %v", err)
	}
}

func TestHTTPChatCompletion_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model not loaded"}`))
	}))
	defer srv.Close()
	_, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err == nil {
		t.Fatal("expected error for upstream 500")
	}
	if !strings.Contains(err.Error(), "ollama backend error") {
		t.Fatalf("expected 'ollama backend error', got: %v", err)
	}
}

func TestHTTPChatCompletion_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	_, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err == nil {
		t.Fatal("expected error for malformed response")
	}
	if !strings.Contains(err.Error(), "unparseable") {
		t.Fatalf("expected 'unparseable', got: %v", err)
	}
}

func TestHTTPChatCompletion_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := HTTPChatCompletion(ctx, srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
