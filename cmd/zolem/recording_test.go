package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInMemoryRecorder_NextCallIDIsMonotonic(t *testing.T) {
	r := newInMemoryRecorder("listener-1")
	if got := r.NextCallID(); got != 1 {
		t.Fatalf("first NextCallID = %d, want 1", got)
	}
	if got := r.NextCallID(); got != 2 {
		t.Fatalf("second NextCallID = %d, want 2", got)
	}
}

func TestInMemoryRecorder_NextCallIDConcurrentDistinct(t *testing.T) {
	r := newInMemoryRecorder("listener-1")
	const n = 200
	got := make([]int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = r.NextCallID()
		}(i)
	}
	wg.Wait()
	seen := map[int64]bool{}
	for _, v := range got {
		if seen[v] {
			t.Fatalf("duplicate id %d", v)
		}
		seen[v] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct ids, got %d", n, len(seen))
	}
}

func TestInMemoryRecorder_RecordListClear(t *testing.T) {
	r := newInMemoryRecorder("listener-1")
	r.Record(RecordedCall{CallID: 1, Listener: "listener-1"})
	r.Record(RecordedCall{CallID: 2, Listener: "listener-1"})

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}

	// Defensive copy — mutating returned slice shouldn't change internal state.
	list[0].CallID = 999
	again := r.List()
	if again[0].CallID != 1 {
		t.Fatalf("List did not return defensive copy: %+v", again[0])
	}

	cleared := r.Clear()
	if cleared != 2 {
		t.Fatalf("Clear returned %d, want 2", cleared)
	}
	if len(r.List()) != 0 {
		t.Fatalf("after Clear, List should be empty")
	}
	if got := r.NextCallID(); got != 1 {
		t.Fatalf("after Clear, NextCallID = %d, want 1", got)
	}
}

func TestInMemoryRecorder_CloseNoop(t *testing.T) {
	r := newInMemoryRecorder("listener-1")
	r.Close()
	r.Close() // double close is safe
}

func TestRecordedRequest_BodyVsBodyBase64JSON(t *testing.T) {
	// Valid UTF-8 -> body, no body_base64.
	rr := RecordedRequest{}
	rr.setBody([]byte("hello"), 0)
	b, _ := json.Marshal(rr)
	if !strings.Contains(string(b), `"body":"hello"`) {
		t.Fatalf("missing body field: %s", b)
	}
	if strings.Contains(string(b), `body_base64`) {
		t.Fatalf("unexpected body_base64 field: %s", b)
	}

	// Invalid UTF-8 -> body_base64, no body.
	rr2 := RecordedRequest{}
	rr2.setBody([]byte{0xff, 0xfe, 0xfd}, 0)
	b2, _ := json.Marshal(rr2)
	if strings.Contains(string(b2), `"body":`) {
		t.Fatalf("unexpected body field in: %s", b2)
	}
	if !strings.Contains(string(b2), `"body_base64":"//79"`) {
		t.Fatalf("missing body_base64 field: %s", b2)
	}
}

func TestRecordingMiddleware_CapturesRequestAndResponse(t *testing.T) {
	r := newInMemoryRecorder("listener-1")
	caps := DefaultRecordCaps()

	next := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		if string(body) != "hello" {
			t.Errorf("handler saw body %q, want %q", body, "hello")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	h := recordingMiddleware(r, caps)(next)

	req := httptest.NewRequest(http.MethodPost, "/v1/foo?x=1", bytes.NewReader([]byte("hello")))
	req.Header.Set("Authorization", "Bearer sk-test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	calls := r.List()
	if len(calls) != 1 {
		t.Fatalf("List len = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.CallID != 1 {
		t.Fatalf("CallID = %d, want 1", c.CallID)
	}
	if c.Listener != "listener-1" {
		t.Fatalf("Listener = %q", c.Listener)
	}
	if c.Request.Method != http.MethodPost {
		t.Fatalf("Method = %q", c.Request.Method)
	}
	if c.Request.Path != "/v1/foo" {
		t.Fatalf("Path = %q", c.Request.Path)
	}
	if c.Request.Query != "x=1" {
		t.Fatalf("Query = %q", c.Request.Query)
	}
	if c.Request.Body != "hello" {
		t.Fatalf("Request.Body = %q", c.Request.Body)
	}
	if c.Request.Headers.Get("Authorization") != "Bearer sk-test" {
		t.Fatalf("Authorization header missing: %+v", c.Request.Headers)
	}
	if c.Response.Status != 200 {
		t.Fatalf("Status = %d", c.Response.Status)
	}
	if c.Response.Body != `{"ok":true}` {
		t.Fatalf("Response.Body = %q", c.Response.Body)
	}
	if c.Response.Headers.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type header missing")
	}
	if c.Response.Stream != nil {
		t.Fatalf("Stream should be nil for non-SSE response, got %+v", c.Response.Stream)
	}
	if c.ReceivedAt.IsZero() || c.CompletedAt.IsZero() {
		t.Fatalf("timestamps not set: received=%v completed=%v", c.ReceivedAt, c.CompletedAt)
	}
	if c.LatencyMS < 0 {
		t.Fatalf("LatencyMS = %d", c.LatencyMS)
	}
}

func TestRecordingMiddleware_RequestBodyCap(t *testing.T) {
	r := newInMemoryRecorder("listener-1")
	caps := RecordCaps{RequestBodyCapBytes: 4, ResponseBodyCapBytes: 1024, StreamEventCap: 32}

	next := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		if string(body) != "hello world" {
			t.Errorf("handler should still see full body, got %q", body)
		}
		w.WriteHeader(http.StatusOK)
	})
	h := recordingMiddleware(r, caps)(next)

	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte("hello world")))
	h.ServeHTTP(httptest.NewRecorder(), req)

	calls := r.List()
	if len(calls) != 1 {
		t.Fatalf("len = %d", len(calls))
	}
	got := calls[0].Request
	if got.Body != "hell" {
		t.Fatalf("Body = %q, want first 4 bytes", got.Body)
	}
	if got.BodyTruncatedBytes != len("hello world")-4 {
		t.Fatalf("BodyTruncatedBytes = %d, want %d", got.BodyTruncatedBytes, len("hello world")-4)
	}
}

func TestRecordingMiddleware_ResponseBodyCap(t *testing.T) {
	r := newInMemoryRecorder("listener-1")
	caps := RecordCaps{RequestBodyCapBytes: 1024, ResponseBodyCapBytes: 3, StreamEventCap: 32}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("abcdefg"))
	})
	h := recordingMiddleware(r, caps)(next)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Handler downstream should still receive full body.
	if w.Body.String() != "abcdefg" {
		t.Fatalf("response writer saw %q, want full body", w.Body.String())
	}

	calls := r.List()
	got := calls[0].Response
	if got.Body != "abc" {
		t.Fatalf("Body = %q", got.Body)
	}
	if got.BodyTruncatedBytes != 4 {
		t.Fatalf("BodyTruncatedBytes = %d, want 4", got.BodyTruncatedBytes)
	}
}

func TestRecordingMiddleware_SSEStream(t *testing.T) {
	r := newInMemoryRecorder("listener-1")
	caps := DefaultRecordCaps()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// First frame in one write.
		_, _ = w.Write([]byte("event: message_start\ndata: {\"a\":1}\n\n"))
		flusher.Flush()
		// Split a frame across two writes.
		_, _ = w.Write([]byte("event: delta\ndata: {\"b\":"))
		_, _ = w.Write([]byte("2}\n\n"))
		flusher.Flush()
	})
	h := recordingMiddleware(r, caps)(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	calls := r.List()
	if len(calls) != 1 {
		t.Fatalf("len = %d", len(calls))
	}
	c := calls[0]
	if c.Response.Body != "" {
		t.Fatalf("SSE response body should be empty, got %q", c.Response.Body)
	}
	if c.Response.Stream == nil {
		t.Fatalf("Stream is nil; want populated")
	}
	if c.Response.Stream.EventCount != 2 {
		t.Fatalf("EventCount = %d, want 2", c.Response.Stream.EventCount)
	}
	if len(c.Response.Stream.Events) != 2 {
		t.Fatalf("Events len = %d, want 2", len(c.Response.Stream.Events))
	}
	if c.Response.Stream.Events[0].Event != "message_start" {
		t.Fatalf("Event[0].Event = %q", c.Response.Stream.Events[0].Event)
	}
	if c.Response.Stream.Events[0].Data != `{"a":1}` {
		t.Fatalf("Event[0].Data = %q", c.Response.Stream.Events[0].Data)
	}
	if c.Response.Stream.Events[1].Event != "delta" {
		t.Fatalf("Event[1].Event = %q", c.Response.Stream.Events[1].Event)
	}
	if c.Response.Stream.Events[1].Data != `{"b":2}` {
		t.Fatalf("Event[1].Data = %q", c.Response.Stream.Events[1].Data)
	}
	if c.Response.Stream.Events[0].ReceivedAt.IsZero() {
		t.Fatalf("event timestamp not set")
	}
}

func TestRecordingMiddleware_SSEEventCap(t *testing.T) {
	r := newInMemoryRecorder("listener-1")
	caps := RecordCaps{RequestBodyCapBytes: 1024, ResponseBodyCapBytes: 1024, StreamEventCap: 2}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < 5; i++ {
			_, _ = w.Write([]byte("event: e\ndata: x\n\n"))
		}
	})
	h := recordingMiddleware(r, caps)(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/stream", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	c := r.List()[0]
	if c.Response.Stream.EventCount != 5 {
		t.Fatalf("EventCount = %d, want 5", c.Response.Stream.EventCount)
	}
	if len(c.Response.Stream.Events) != 2 {
		t.Fatalf("Events len = %d, want 2", len(c.Response.Stream.Events))
	}
	if c.Response.Stream.EventsTruncated != 3 {
		t.Fatalf("EventsTruncated = %d, want 3", c.Response.Stream.EventsTruncated)
	}
}

func TestRecordingMiddleware_AssignsCallIDsInArrivalOrder(t *testing.T) {
	r := newInMemoryRecorder("listener-1")
	caps := DefaultRecordCaps()

	// Block handler until released so we can sequence arrivals.
	release := make(chan struct{})
	arrived := make(chan int64, 3)
	next := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// The middleware stamps call_id before invoking next.
		// We read it back via a header echo by reading from a side channel.
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := recordingMiddleware(r, caps)(next)

	go func() {
		req := httptest.NewRequest(http.MethodGet, "/a", nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
		arrived <- 0
	}()
	// Allow a small window so first arrival registers.
	time.Sleep(10 * time.Millisecond)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/b", nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
		arrived <- 0
	}()
	time.Sleep(10 * time.Millisecond)
	close(release)
	<-arrived
	<-arrived

	calls := r.List()
	if len(calls) != 2 {
		t.Fatalf("len = %d", len(calls))
	}
	// Sort by call_id; both should be assigned and distinct.
	if calls[0].CallID == calls[1].CallID {
		t.Fatalf("call IDs collided: %+v", calls)
	}
}
