package ollama

import (
	"context"
	"net/http"
	"time"

	"github.com/ketang/zolem/internal/provider/backend"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// streamChat streams generated content as NDJSON.
//
// Framing differs from every other zolem provider: bare JSON objects separated
// by newlines, no "data: " prefix, no [DONE] sentinel. The stream ends with an
// object carrying done=true, and only that final object carries the metrics
// block.
func streamChat(ctx context.Context, w http.ResponseWriter, cb backend.ContentBackend, req backend.GenerateRequest, model string, promptTokens int) {
	nd := response.NewNDJSONWriter(w)

	created := nowRFC3339Nano()
	start := time.Now()

	// Headers are deferred until the first delta so a backend that fails before
	// producing anything can still be reported as a real HTTP error. Ollama
	// returns a 5xx flat envelope when it fails pre-first-token, and only
	// reports failures in-band once the stream has started.
	headersSent := false
	sendHeaders := func() {
		if !headersSent {
			nd.SetHeaders()
			headersSent = true
		}
	}

	evalTokens := 0
	err := cb.Stream(ctx, req, func(delta string) error {
		sendHeaders()
		if delta != "" {
			// Match the non-streaming path, which counts via
			// response.CountNonEmpty, so the same request reports the same
			// eval_count whether or not it streamed.
			evalTokens++
		}
		return nd.WriteObject(ChatResponse{
			Model:     model,
			CreatedAt: created,
			Message:   Message{Role: "assistant", Content: delta},
			Done:      false,
		})
	})

	if err != nil {
		if !headersSent {
			writeBackendError(w, err)
			return
		}
		// Mid-stream: the status line is long gone, so the failure has to ride
		// in-band as an {"error": ...} object inside the already-200 body.
		_ = nd.WriteObject(errorEnvelope{Error: err.Error()})
		return
	}

	sendHeaders()

	final := ChatResponse{
		Model:      model,
		CreatedAt:  created,
		Message:    Message{Role: "assistant", Content: ""},
		Done:       true,
		DoneReason: doneReasonStop,
	}
	applyMetrics(&final, time.Since(start), promptTokens, evalTokens)
	_ = nd.WriteObject(final)
}

// streamFixture streams a pre-materialized fixture response, honoring the
// profile's stream pacing the way the other providers' token-list emitters do.
func streamFixture(ctx context.Context, w http.ResponseWriter, resp ChatResponse) {
	nd := response.NewNDJSONWriter(w)
	nd.SetHeaders()

	created := resp.CreatedAt
	if created == "" {
		created = nowRFC3339Nano()
	}
	delay := runtimecfg.StreamDelayForRequest(ctx)

	for _, tok := range backend.Tokenize(resp.Message.Content) {
		if err := nd.WriteObject(ChatResponse{
			Model:     resp.Model,
			CreatedAt: created,
			Message:   Message{Role: "assistant", Content: tok},
			Done:      false,
		}); err != nil {
			return
		}
		if delay != nil {
			if err := delay(ctx); err != nil {
				return
			}
		}
	}

	final := resp
	final.CreatedAt = created
	final.Message = Message{Role: "assistant", Content: "", ToolCalls: resp.Message.ToolCalls}
	final.Done = true
	if final.DoneReason == "" {
		final.DoneReason = doneReasonStop
	}
	_ = nd.WriteObject(final)
}
