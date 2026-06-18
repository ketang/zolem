package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// Recorder is the storage abstraction for captured request/response pairs.
// Implementations must be safe for concurrent use.
type Recorder interface {
	// NextCallID returns the next monotonically increasing call ID. It is
	// invoked by the capture middleware at request arrival so IDs reflect
	// arrival order even under concurrent requests.
	NextCallID() int64
	// Record persists a completed call.
	Record(call RecordedCall)
	// RecordWS persists a completed WebSocket connection.
	RecordWS(call RecordedWSCall)
	// List returns a defensive copy of all recorded calls in insertion order.
	List() []RecordedCall
	// Clear drops all recorded calls and resets the call_id sequence to 1.
	// Returns the number of calls dropped.
	Clear() int
	// Close releases any resources held by the recorder.
	Close()
}

// RecordedCall is the top-level captured record for one request/response pair.
type RecordedCall struct {
	CallID      int64            `json:"call_id"`
	Listener    string           `json:"listener"`
	ReceivedAt  time.Time        `json:"received_at"`
	CompletedAt time.Time        `json:"completed_at"`
	LatencyMS   int64            `json:"latency_ms"`
	Request     RecordedRequest  `json:"request"`
	Response    RecordedResponse `json:"response"`
	WebSocket   *RecordedWSCall  `json:"websocket,omitempty"`
}

// RecordedWSCall is the compact JSONL shape used for WebSocket connections.
type RecordedWSCall struct {
	CallID         int64  `json:"call_id"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	Status         int    `json:"status"`
	FramesSent     int    `json:"frames_sent"`
	FramesReceived int    `json:"frames_received"`
}

// RecordedRequest is the captured request half of a call.
type RecordedRequest struct {
	Method             string      `json:"method"`
	Path               string      `json:"path"`
	Query              string      `json:"query"`
	Headers            http.Header `json:"headers"`
	RemoteAddr         string      `json:"remote_addr"`
	Body               string      `json:"body,omitempty"`
	BodyBase64         string      `json:"body_base64,omitempty"`
	BodyTruncatedBytes int         `json:"body_truncated_bytes"`
}

// RecordedResponse is the captured response half of a call.
type RecordedResponse struct {
	Status             int           `json:"status"`
	Headers            http.Header   `json:"headers"`
	Body               string        `json:"body,omitempty"`
	BodyBase64         string        `json:"body_base64,omitempty"`
	BodyTruncatedBytes int           `json:"body_truncated_bytes"`
	Stream             *StreamRecord `json:"stream"`
}

// StreamRecord captures the parsed SSE event stream for a response.
type StreamRecord struct {
	EventCount      int           `json:"event_count"`
	Events          []StreamEvent `json:"events"`
	EventsTruncated int           `json:"events_truncated"`
}

// StreamEvent is a single parsed SSE frame.
type StreamEvent struct {
	ReceivedAt time.Time `json:"received_at"`
	Event      string    `json:"event"`
	Data       string    `json:"data"`
}

// setBody assigns raw bytes to the request, choosing the UTF-8 Body field or
// the base64-encoded BodyBase64 field based on UTF-8 validity. The two are
// mutually exclusive.
func (r *RecordedRequest) setBody(raw []byte, truncated int) {
	r.BodyTruncatedBytes = truncated
	if len(raw) == 0 {
		return
	}
	if utf8.Valid(raw) {
		r.Body = string(raw)
		return
	}
	r.BodyBase64 = base64.StdEncoding.EncodeToString(raw)
}

// setBody on the response — see RecordedRequest.setBody.
func (r *RecordedResponse) setBody(raw []byte, truncated int) {
	r.BodyTruncatedBytes = truncated
	if len(raw) == 0 {
		return
	}
	if utf8.Valid(raw) {
		r.Body = string(raw)
		return
	}
	r.BodyBase64 = base64.StdEncoding.EncodeToString(raw)
}

// RecordCaps bounds the bytes/events stored per captured call so a single
// large request cannot exhaust memory.
type RecordCaps struct {
	RequestBodyCapBytes  int
	ResponseBodyCapBytes int
	StreamEventCap       int
}

// DefaultRecordCaps returns the default per-listener caps.
func DefaultRecordCaps() RecordCaps {
	return RecordCaps{
		RequestBodyCapBytes:  262144,
		ResponseBodyCapBytes: 262144,
		StreamEventCap:       1024,
	}
}

// inMemoryRecorder buffers RecordedCalls in memory until explicit reset.
type inMemoryRecorder struct {
	listener string

	mu       sync.Mutex
	calls    []RecordedCall
	nextID   int64
	inFlight int
}

func newInMemoryRecorder(listener string) *inMemoryRecorder {
	return &inMemoryRecorder{listener: listener}
}

// NewInMemoryRecorder returns a Recorder that buffers calls in memory with no
// file I/O, suitable for scans and tests. The listener label is attached to
// each recorded call.
func NewInMemoryRecorder(listener string) Recorder { return newInMemoryRecorder(listener) }

// NewNoopRecorder returns a Recorder that discards every call. It performs no
// file I/O and holds no resources.
func NewNoopRecorder() Recorder { return noopRecorder{} }

func (r *inMemoryRecorder) NextCallID() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.inFlight++
	return r.nextID
}

func (r *inMemoryRecorder) Record(call RecordedCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inFlight > 0 {
		r.inFlight--
	}
	r.calls = append(r.calls, call)
}

func (r *inMemoryRecorder) RecordWS(call RecordedWSCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inFlight > 0 {
		r.inFlight--
	}
	wsCall := call
	r.calls = append(r.calls, RecordedCall{
		CallID:   call.CallID,
		Listener: r.listener,
		Request: RecordedRequest{
			Method: call.Method,
			Path:   call.Path,
		},
		Response: RecordedResponse{
			Status: call.Status,
		},
		WebSocket: &wsCall,
	})
}

func (r *inMemoryRecorder) List() []RecordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *inMemoryRecorder) Clear() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.calls)
	r.calls = nil
	if r.inFlight == 0 {
		r.nextID = 0
	}
	return n
}

func (r *inMemoryRecorder) Close() {}

// jsonlRecorder appends each completed call as a single JSON object followed
// by a newline to an open file. It is safe for concurrent use; writes are
// serialized through a mutex and fsynced before returning to the caller.
type jsonlRecorder struct {
	mu   sync.Mutex
	file *os.File
	next atomic.Int64
}

func newJSONLRecorder(path string) (*jsonlRecorder, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open calls file %q: %w", path, err)
	}
	return &jsonlRecorder{file: f}, nil
}

func (r *jsonlRecorder) NextCallID() int64 {
	return r.next.Add(1)
}

func (r *jsonlRecorder) Record(call RecordedCall) {
	buf, err := json.Marshal(call)
	if err != nil {
		return
	}
	buf = append(buf, '\n')
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.file.Write(buf); err != nil {
		return
	}
	_ = r.file.Sync()
}

func (r *jsonlRecorder) RecordWS(call RecordedWSCall) {
	buf, err := json.Marshal(call)
	if err != nil {
		return
	}
	buf = append(buf, '\n')
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.file.Write(buf); err != nil {
		return
	}
	_ = r.file.Sync()
}

func (r *jsonlRecorder) List() []RecordedCall { return nil }

func (r *jsonlRecorder) Clear() int { return 0 }

func (r *jsonlRecorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file != nil {
		_ = r.file.Close()
		r.file = nil
	}
}

// noopRecorder discards every call. It exists so the recording middleware can
// be wired unconditionally without branching on whether recording is enabled.
type noopRecorder struct{}

func (noopRecorder) NextCallID() int64       { return 0 }
func (noopRecorder) Record(RecordedCall)     {}
func (noopRecorder) RecordWS(RecordedWSCall) {}
func (noopRecorder) List() []RecordedCall    { return nil }
func (noopRecorder) Clear() int              { return 0 }
func (noopRecorder) Close()                  {}

// Compile-time interface assertions.
var (
	_ Recorder = (*inMemoryRecorder)(nil)
	_ Recorder = (*jsonlRecorder)(nil)
	_ Recorder = noopRecorder{}
)

// recordingMiddleware wraps an http.Handler so that every request/response is
// captured into the provided Recorder. The middleware:
//
//  1. Assigns call_id via recorder.NextCallID() before invoking next, so
//     concurrent arrivals get distinct, arrival-ordered IDs.
//  2. Wraps the request body so the downstream handler can stream it normally
//     while capture stores only up to caps.RequestBodyCapBytes.
//  3. Wraps the ResponseWriter to capture status, headers, and body (or SSE
//     event stream, detected via Content-Type: text/event-stream).
func recordingMiddleware(recorder Recorder, caps RecordCaps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			receivedAt := time.Now().UTC()
			callID := recorder.NextCallID()
			ctx, wsStats := runtimecfg.WithWebSocketStats(req.Context())
			req = req.WithContext(ctx)

			bodyCapture := newRecordingRequestBody(req.Body, caps.RequestBodyCapBytes)
			req.Body = bodyCapture

			rw := newRecordingResponseWriter(w, caps)
			next.ServeHTTP(rw, req)
			_ = bodyCapture.drain()
			_ = bodyCapture.Close()
			reqBody, reqTruncated := bodyCapture.snapshot()

			completedAt := time.Now().UTC()
			if wsStats.Upgraded() {
				recorder.RecordWS(RecordedWSCall{
					CallID:         callID,
					Method:         req.Method,
					Path:           req.URL.Path,
					Status:         http.StatusSwitchingProtocols,
					FramesSent:     wsStats.FramesSent(),
					FramesReceived: wsStats.FramesReceived(),
				})
				return
			}

			call := RecordedCall{
				CallID:      callID,
				Listener:    recorderListener(recorder),
				ReceivedAt:  receivedAt,
				CompletedAt: completedAt,
				LatencyMS:   completedAt.Sub(receivedAt).Milliseconds(),
				Request: RecordedRequest{
					Method:     req.Method,
					Path:       req.URL.Path,
					Query:      req.URL.RawQuery,
					Headers:    cloneHeader(req.Header),
					RemoteAddr: req.RemoteAddr,
				},
				Response: RecordedResponse{
					Status:  rw.status,
					Headers: cloneHeader(rw.Header()),
				},
			}
			call.Request.setBody(reqBody, reqTruncated)

			if rw.isSSE {
				call.Response.Stream = &StreamRecord{
					EventCount:      rw.stream.totalEvents,
					Events:          rw.stream.events,
					EventsTruncated: rw.stream.totalEvents - len(rw.stream.events),
				}
			} else {
				call.Response.setBody(rw.body.Bytes(), rw.bodyTruncated)
			}

			recorder.Record(call)
		})
	}
}

// recorderListener pulls the listener name from inMemoryRecorder. Other
// implementations (jsonlRecorder, noopRecorder) can leave Call.Listener
// blank — the JSONL recorder writes the listener inline in the file path.
func recorderListener(r Recorder) string {
	if in, ok := r.(*inMemoryRecorder); ok {
		return in.listener
	}
	return ""
}

func cloneHeader(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for k, v := range h {
		dup := make([]string, len(v))
		copy(dup, v)
		out[k] = dup
	}
	return out
}

type recordingRequestBody struct {
	rc     io.ReadCloser
	cap    int
	buf    bytes.Buffer
	total  int
	closed bool
}

func newRecordingRequestBody(rc io.ReadCloser, cap int) *recordingRequestBody {
	if rc == nil {
		rc = http.NoBody
	}
	return &recordingRequestBody{rc: rc, cap: cap}
}

func (b *recordingRequestBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.capture(p[:n])
	}
	return n, err
}

func (b *recordingRequestBody) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	return b.rc.Close()
}

func (b *recordingRequestBody) drain() error {
	if b.closed {
		return nil
	}
	_, err := io.Copy(io.Discard, b)
	if err == io.EOF {
		return nil
	}
	return err
}

func (b *recordingRequestBody) capture(p []byte) {
	b.total += len(p)
	if b.cap <= 0 {
		b.buf.Write(p)
		return
	}
	room := b.cap - b.buf.Len()
	if room <= 0 {
		return
	}
	if len(p) <= room {
		b.buf.Write(p)
		return
	}
	b.buf.Write(p[:room])
}

func (b *recordingRequestBody) snapshot() ([]byte, int) {
	raw := append([]byte(nil), b.buf.Bytes()...)
	truncated := b.total - len(raw)
	if truncated < 0 {
		truncated = 0
	}
	return raw, truncated
}

// recordingResponseWriter wraps an http.ResponseWriter to capture status,
// body bytes (up to a cap), and, for SSE responses, parsed event frames.
type recordingResponseWriter struct {
	http.ResponseWriter
	caps RecordCaps

	status        int
	headerWritten bool

	body          bytes.Buffer
	bodyCapBytes  int
	bodyTruncated int

	isSSE  bool
	stream *sseAccumulator
}

func newRecordingResponseWriter(w http.ResponseWriter, caps RecordCaps) *recordingResponseWriter {
	return &recordingResponseWriter{
		ResponseWriter: w,
		caps:           caps,
		status:         http.StatusOK,
		bodyCapBytes:   caps.ResponseBodyCapBytes,
	}
}

func (rw *recordingResponseWriter) WriteHeader(status int) {
	if rw.headerWritten {
		rw.ResponseWriter.WriteHeader(status)
		return
	}
	rw.headerWritten = true
	rw.status = status
	ct := rw.ResponseWriter.Header().Get("Content-Type")
	if strings.HasPrefix(strings.ToLower(ct), "text/event-stream") {
		rw.isSSE = true
		rw.stream = newSSEAccumulator(rw.caps.StreamEventCap)
	}
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *recordingResponseWriter) Write(p []byte) (int, error) {
	if !rw.headerWritten {
		rw.WriteHeader(http.StatusOK)
	}
	if rw.isSSE {
		rw.stream.feed(p, time.Now().UTC())
	} else {
		rw.captureBody(p)
	}
	return rw.ResponseWriter.Write(p)
}

func (rw *recordingResponseWriter) captureBody(p []byte) {
	if rw.bodyCapBytes <= 0 {
		rw.body.Write(p)
		return
	}
	room := rw.bodyCapBytes - rw.body.Len()
	if room <= 0 {
		rw.bodyTruncated += len(p)
		return
	}
	if len(p) <= room {
		rw.body.Write(p)
		return
	}
	rw.body.Write(p[:room])
	rw.bodyTruncated += len(p) - room
}

func (rw *recordingResponseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (rw *recordingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

// sseAccumulator parses concatenated SSE chunks into discrete event frames.
// Frames are delimited by "\n\n"; within a frame, "event:" and "data:" lines
// (and only those) are extracted. Partial frames at chunk boundaries are
// buffered across feed() calls.
type sseAccumulator struct {
	cap         int
	buf         bytes.Buffer
	events      []StreamEvent
	totalEvents int
}

func newSSEAccumulator(cap int) *sseAccumulator {
	return &sseAccumulator{cap: cap}
}

func (a *sseAccumulator) feed(p []byte, ts time.Time) {
	a.buf.Write(p)
	for {
		data := a.buf.Bytes()
		idx := bytes.Index(data, []byte("\n\n"))
		if idx < 0 {
			return
		}
		frame := data[:idx]
		// Drain consumed bytes including the delimiter.
		_ = a.buf.Next(idx + 2)
		a.parseFrame(frame, ts)
	}
}

func (a *sseAccumulator) parseFrame(frame []byte, ts time.Time) {
	var event string
	var dataLines []string
	for _, lineBytes := range bytes.Split(frame, []byte("\n")) {
		line := string(lineBytes)
		switch {
		case strings.HasPrefix(line, "event:"):
			event = sseFieldValue(line, "event:")
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, sseFieldValue(line, "data:"))
		}
	}
	data := strings.Join(dataLines, "\n")
	if event == "" && data == "" {
		return
	}
	a.totalEvents++
	if a.cap > 0 && len(a.events) >= a.cap {
		return
	}
	a.events = append(a.events, StreamEvent{
		ReceivedAt: ts,
		Event:      event,
		Data:       data,
	})
}

func sseFieldValue(line, prefix string) string {
	value := strings.TrimPrefix(line, prefix)
	if strings.HasPrefix(value, " ") {
		return strings.TrimPrefix(value, " ")
	}
	return value
}
