package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestHTTPChatCompletionStream_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] != true {
			t.Errorf("expected stream=true, got %v", req["stream"])
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{"Hello ", "from ", "ollama"}
		for _, chunk := range chunks {
			data, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{
					{"delta": map[string]string{"content": chunk}},
				},
			})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()
	var deltas []string
	err := HTTPChatCompletionStream(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deltas) != 3 {
		t.Fatalf("expected 3 deltas, got %d: %v", len(deltas), deltas)
	}
	joined := strings.Join(deltas, "")
	if joined != "Hello from ollama" {
		t.Fatalf("unexpected text: %q", joined)
	}
}

func TestHTTPChatCompletionStream_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model not loaded"}`))
	}))
	defer srv.Close()
	err := HTTPChatCompletionStream(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error {
		t.Fatal("callback should not be called")
		return nil
	})
	if err == nil {
		t.Fatal("expected error for upstream 500")
	}
	if !strings.Contains(err.Error(), "ollama backend error") {
		t.Fatalf("expected 'ollama backend error', got: %v", err)
	}
}

func TestHTTPChatCompletionStream_ConnectionRefused(t *testing.T) {
	err := HTTPChatCompletionStream(context.Background(), "http://127.0.0.1:1", []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error { return nil })
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if !strings.Contains(err.Error(), "ollama backend unavailable") {
		t.Fatalf("expected 'ollama backend unavailable', got: %v", err)
	}
}

func TestHTTPChatCompletionStream_CallbackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]string{"content": "hello"}},
			},
		})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}))
	defer srv.Close()
	callbackErr := errors.New("writer closed")
	err := HTTPChatCompletionStream(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error { return callbackErr })
	if !errors.Is(err, callbackErr) {
		t.Fatalf("expected callback error, got: %v", err)
	}
}
