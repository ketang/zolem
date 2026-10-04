package gemini

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ketang/zolem/internal/provider/backend"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// streamSink is the wire encoding for streamGenerateContent. With ?alt=sse the
// real API sends SSE frames; without it (or with alt=json) it sends one JSON
// array of GenerateContentResponse objects.
type streamSink interface {
	// send writes one GenerateContentResponse chunk.
	send(chunk GenerateContentResponse)
	// fail ends the stream with a backend error. Before any chunk was sent the
	// JSON sink answers with a plain non-2xx error instead.
	fail(err error)
	// close finishes the stream; it is a no-op after fail and idempotent.
	close()
}

// newStreamSink picks the encoding from the alt query parameter.
func newStreamSink(w http.ResponseWriter, r *http.Request) streamSink {
	if r.URL.Query().Get("alt") == "sse" {
		return newSSESink(w)
	}
	return &jsonArraySink{w: w}
}

func streamErrorObject(err error) map[string]any {
	return map[string]any{
		"error": map[string]any{"code": 502, "message": err.Error(), "status": "INTERNAL"},
	}
}

type sseSink struct{ sse *response.SSEWriter }

func newSSESink(w http.ResponseWriter) *sseSink {
	s := &sseSink{sse: response.NewSSEWriter(w)}
	s.sse.SetHeaders()
	return s
}

func (s *sseSink) send(chunk GenerateContentResponse) {
	data, _ := json.Marshal(chunk)
	s.sse.WriteData(data)
	s.sse.Flush()
}

func (s *sseSink) fail(err error) {
	data, _ := json.Marshal(streamErrorObject(err))
	s.sse.WriteData(data)
	s.sse.Flush()
}

func (s *sseSink) close() {}

// jsonArraySink streams a JSON array incrementally. The status line, the
// Content-Type header and the opening "[" are deferred until the first chunk so
// a failure before any output can still return a real non-2xx JSON error.
type jsonArraySink struct {
	w       http.ResponseWriter
	started bool
	done    bool
}

func (s *jsonArraySink) open() {
	s.w.Header().Set("Content-Type", "application/json")
	_, _ = s.w.Write([]byte("["))
	s.started = true
}

func (s *jsonArraySink) write(v any) {
	data, _ := json.Marshal(v)
	if !s.started {
		s.open()
	} else {
		_, _ = s.w.Write([]byte(","))
	}
	_, _ = s.w.Write(data)
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *jsonArraySink) send(chunk GenerateContentResponse) {
	if !s.done {
		s.write(chunk)
	}
}

// fail closes the array with the error object as its last element rather than
// truncating it.
func (s *jsonArraySink) fail(err error) {
	if s.done {
		return
	}
	if !s.started {
		s.done = true
		writeBackendError(s.w, err)
		return
	}
	s.write(streamErrorObject(err))
	s.close()
}

func (s *jsonArraySink) close() {
	if s.done {
		return
	}
	s.done = true
	if !s.started {
		s.open()
	}
	_, _ = s.w.Write([]byte("]"))
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

// streamGenerateContent streams a response via ContentBackend.Stream, emitting
// Gemini-native GenerateContentResponse chunks through sink.
func streamGenerateContent(ctx context.Context, sink streamSink, cb backend.ContentBackend, req backend.GenerateRequest, model string, promptTokens int) {
	defer sink.close()

	completionTokens := 0
	err := cb.Stream(ctx, req, func(delta string) error {
		completionTokens++
		sink.send(GenerateContentResponse{
			Candidates: []Candidate{{
				Content: Content{Parts: []Part{{Text: delta}}, Role: "model"},
				Index:   0,
			}},
			UsageMetadata: UsageMetadata{
				PromptTokenCount: promptTokens,
			},
			ModelVersion: model,
		})
		return nil
	})

	if err != nil {
		sink.fail(err)
		return
	}

	sink.send(GenerateContentResponse{
		Candidates: []Candidate{{
			Content:      Content{Parts: []Part{{Text: ""}}, Role: "model"},
			FinishReason: "STOP",
			Index:        0,
		}},
		UsageMetadata: UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: completionTokens,
			TotalTokenCount:      promptTokens + completionTokens,
		},
		ModelVersion: model,
	})
}

// streamFunctionCallContent emits a single Gemini chunk containing the
// synthesized functionCall Part.
func streamFunctionCallContent(sink streamSink, fc FunctionCall, model string, promptTokens int) {
	defer sink.close()
	sink.send(GenerateContentResponse{
		Candidates: []Candidate{{
			Content:      Content{Parts: []Part{{FunctionCall: &fc}}, Role: "model"},
			FinishReason: "STOP",
			Index:        0,
		}},
		UsageMetadata: UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: 1,
			TotalTokenCount:      promptTokens + 1,
		},
		ModelVersion: model,
	})
}

func streamResponse(ctx context.Context, sink streamSink, model string, tokens []string, promptTokens int) {
	defer sink.close()
	delay := runtimecfg.StreamDelayForRequest(ctx)

	if len(tokens) == 0 {
		sink.send(GenerateContentResponse{
			Candidates: []Candidate{{
				Content:      Content{Parts: []Part{{Text: ""}}, Role: "model"},
				FinishReason: "STOP",
				Index:        0,
			}},
			UsageMetadata: UsageMetadata{
				PromptTokenCount: promptTokens,
				TotalTokenCount:  promptTokens,
			},
			ModelVersion: model,
		})
		return
	}

	for i, tok := range tokens {
		isLast := i == len(tokens)-1
		finishReason := ""
		candidateTokenCount := 0
		if isLast {
			finishReason = "STOP"
			candidateTokenCount = response.CountNonEmpty(tokens)
		}

		sink.send(GenerateContentResponse{
			Candidates: []Candidate{{
				Content: Content{
					Parts: []Part{{Text: tok}},
					Role:  "model",
				},
				FinishReason: finishReason,
				Index:        0,
			}},
			UsageMetadata: UsageMetadata{
				PromptTokenCount:     promptTokens,
				CandidatesTokenCount: candidateTokenCount,
				TotalTokenCount:      promptTokens + candidateTokenCount,
			},
			ModelVersion: model,
		})
		if delay != nil {
			if err := delay(ctx); err != nil {
				return
			}
		}
	}
}
