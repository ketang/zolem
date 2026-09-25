package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// maxTopLogprobs is the most alternatives Ollama returns per position.
const maxTopLogprobs = 20

// ErrNoLogprobs reports that the upstream answered without token log
// probabilities, which older Ollama releases (before 0.12.11) do by ignoring
// the logprobs request field.
var ErrNoLogprobs = errors.New("ollama upstream did not return logprobs")

// TokenLogprob is one candidate token and its natural-log probability.
type TokenLogprob struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
}

type generateRequest struct {
	Model       string          `json:"model"`
	Prompt      string          `json:"prompt"`
	Stream      bool            `json:"stream"`
	Logprobs    bool            `json:"logprobs"`
	TopLogprobs int             `json:"top_logprobs"`
	Options     generateOptions `json:"options"`
}

type generateOptions struct {
	NumPredict  int     `json:"num_predict"`
	Temperature float64 `json:"temperature"`
}

type generateResponse struct {
	Logprobs []struct {
		TokenLogprob
		TopLogprobs []TokenLogprob `json:"top_logprobs"`
	} `json:"logprobs"`
}

// maxLogprobResponseBytes bounds how much of an upstream reply is read; a
// one-token answer is a few kilobytes at most.
const maxLogprobResponseBytes = 1 << 20

// maxLoggedBodyBytes bounds how much of a failing upstream's body is logged.
const maxLoggedBodyBytes = 300

// logprobClient bounds a single generation; the request context still cancels
// sooner. Redirects are not followed: the upstream host was checked against
// the loopback/private-host policy, and a redirect would bypass it.
var logprobClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// FirstTokenLogprobs asks the upstream's native /api/generate for exactly one
// greedy token and returns the log probabilities of that position's top
// alternatives (the chosen token alone when the upstream returns no
// alternatives). The OpenAI-compatible endpoint is not used because it drops
// logprobs.
func FirstTokenLogprobs(ctx context.Context, upstream, model, prompt string) ([]TokenLogprob, error) {
	data, err := json.Marshal(generateRequest{
		Model:       model,
		Prompt:      prompt,
		Logprobs:    true,
		TopLogprobs: maxTopLogprobs,
		Options:     generateOptions{NumPredict: 1, Temperature: 0},
	})
	if err != nil {
		return nil, fmt.Errorf("ollama logprob backend: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream+"/api/generate", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ollama logprob backend: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := logprobClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama logprob backend unavailable: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLogprobResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("ollama logprob backend: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// The status is returned to the caller; the body stays in the log, since
		// it is arbitrary upstream content that must not be echoed to clients.
		detail := bytes.TrimSpace(body)
		if len(detail) > maxLoggedBodyBytes {
			detail = detail[:maxLoggedBodyBytes]
		}
		log.Printf("ollama logprob backend: upstream %s returned HTTP %d: %s", upstream, resp.StatusCode, detail)
		return nil, fmt.Errorf("ollama logprob backend error: upstream returned HTTP %d", resp.StatusCode)
	}

	var result generateResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("ollama logprob backend returned unparseable response: %w", err)
	}
	if len(result.Logprobs) == 0 {
		return nil, ErrNoLogprobs
	}

	first := result.Logprobs[0]
	if len(first.TopLogprobs) == 0 {
		return []TokenLogprob{first.TokenLogprob}, nil
	}
	return first.TopLogprobs, nil
}
