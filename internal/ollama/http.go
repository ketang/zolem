package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ketang/zolem/internal/upstreamip"
)

// ChatMessage represents a single message in a chat conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatCompletionChoice struct {
	Message ChatMessage `json:"message"`
}

type chatCompletionResponse struct {
	Choices []chatCompletionChoice `json:"choices"`
}

// --- Upstream policy context plumbing -------------------------------------

// allowExternalUpstreamKey is the context key carrying whether the calling
// profile permits connecting to non-loopback/non-private ollama upstreams.
type allowExternalUpstreamKey struct{}

// WithAllowExternalUpstream attaches the caller's ollama upstream policy to
// ctx. When allowExternal is true, HTTPChatCompletion and
// HTTPChatCompletionStream may dial public IP addresses (still refusing
// link-local, unspecified, and multicast addresses); when false (or when no
// policy has been attached), only loopback and private (RFC1918/RFC4193)
// addresses are permitted.
func WithAllowExternalUpstream(ctx context.Context, allowExternal bool) context.Context {
	return context.WithValue(ctx, allowExternalUpstreamKey{}, allowExternal)
}

// allowExternalUpstreamFromContext reports the policy attached by
// WithAllowExternalUpstream, defaulting to false (the stricter policy) when
// none was attached.
func allowExternalUpstreamFromContext(ctx context.Context) bool {
	v := ctx.Value(allowExternalUpstreamKey{})
	if v == nil {
		return false
	}
	allow, ok := v.(bool)
	return ok && allow
}

// --- Dial-time IP policy ----------------------------------------------------

type ipClass = upstreamip.Class

const (
	classPrivate = upstreamip.Private
	classPublic  = upstreamip.Public
	classBlocked = upstreamip.Blocked
)

// overrideIPClass lets tests fake IP classification for addresses that
// cannot otherwise be made to look "public" or "private" in a unit test
// (e.g. 127.0.0.2, which is really loopback). Production code never sets
// this.
var overrideIPClass func(netip.Addr) (ipClass, bool)

// classifyIP classifies addr for the dial-time policy check via the shared
// upstreamip contract (also used by profile-create validation).
func classifyIP(addr netip.Addr) ipClass {
	if overrideIPClass != nil {
		if class, ok := overrideIPClass(upstreamip.Embedded(addr)); ok {
			return class
		}
	}
	return upstreamip.Classify(addr)
}

// dialAllowed reports whether a connection to addr is permitted under the
// given policy, returning a descriptive error when it is not.
func dialAllowed(addr netip.Addr, allowExternal bool) error {
	switch classifyIP(addr) {
	case classBlocked:
		return fmt.Errorf("ollama upstream dial refused: %s is a link-local, unspecified, or multicast address", addr)
	case classPublic:
		if !allowExternal {
			return fmt.Errorf("ollama upstream dial refused: %s is a public address and external ollama upstreams are not allowed", addr)
		}
	}
	return nil
}

// --- Upstream HTTP clients --------------------------------------------------

const (
	dialTimeout              = 5 * time.Second
	tlsHandshakeTimeout      = 5 * time.Second
	responseHeaderTimeout    = 60 * time.Second
	defaultOverallTimeout    = 120 * time.Second
	defaultStreamIdleTimeout = 60 * time.Second

	maxResponseBodyBytes = 8 * 1024 * 1024 // 8 MiB
	maxErrorBodyBytes    = 1024
	maxScanLineBytes     = 1024 * 1024 // 1 MiB
)

// overallTimeout and streamIdleTimeout are package constants above the
// exported surface, but are declared as variables (with unexported test
// overrides) so tests can shrink them without waiting out the real values.
var (
	overallTimeout    = defaultOverallTimeout
	streamIdleTimeout = defaultStreamIdleTimeout
)

// refuseRedirect is the CheckRedirect used by both upstream clients: ollama
// upstreams must never be followed through a redirect, since that would let
// an allowed upstream retarget the request to a disallowed host (e.g. a
// cloud metadata endpoint) after policy checks have already passed.
func refuseRedirect(req *http.Request, _ []*http.Request) error {
	return fmt.Errorf("ollama upstream redirect refused: %s", req.URL)
}

// newUpstreamDialer returns a *net.Dialer whose Control hook enforces the
// dial-time IP policy for allowExternal.
func newUpstreamDialer(allowExternal bool) *net.Dialer {
	return &net.Dialer{
		Timeout: dialTimeout,
		Control: func(_, address string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("ollama upstream dial refused: could not parse resolved address %q: %w", address, err)
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("ollama upstream dial refused: could not parse resolved address %q: %w", host, err)
			}
			return dialAllowed(addr, allowExternal)
		},
	}
}

// newUpstreamClient builds a dedicated *http.Client (and its own
// *http.Transport, never shared with the other policy) for the given
// allow-external-upstream policy. Keeping the transports separate ensures a
// pooled keep-alive connection accepted under one policy is never reused to
// serve a request under the other policy, which would otherwise skip the
// dial-time check.
func newUpstreamClient(allowExternal bool) (*http.Client, *net.Dialer) {
	dialer := newUpstreamDialer(allowExternal)
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: refuseRedirect,
	}
	return client, dialer
}

var (
	defaultPolicyClient, defaultPolicyDialer   = newUpstreamClient(false)
	externalPolicyClient, externalPolicyDialer = newUpstreamClient(true)
)

// clientForContext selects the upstream client matching the policy attached
// to ctx by WithAllowExternalUpstream.
func clientForContext(ctx context.Context) *http.Client {
	if allowExternalUpstreamFromContext(ctx) {
		return externalPolicyClient
	}
	return defaultPolicyClient
}

// --- Response reading helpers -----------------------------------------------

// readCappedBody reads up to limit+1 bytes from r and returns an error if
// the body exceeded limit.
func readCappedBody(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("exceeds %d byte limit", limit)
	}
	return body, nil
}

// readTruncatedErrorBody reads up to maxErrorBodyBytes from r for use in an
// error message; it never fails on its own (truncation is not an error).
func readTruncatedErrorBody(r io.Reader) []byte {
	body, _ := io.ReadAll(io.LimitReader(r, maxErrorBodyBytes))
	return body
}

// HTTPChatCompletion sends a non-streaming chat completion request to the
// Ollama-compatible HTTP API at upstream and returns the assistant's reply.
//
// The request is bounded by an internal 120s overall timeout (in addition to
// ctx), never follows redirects, never uses an HTTP(S) proxy, and refuses to
// dial link-local/unspecified/multicast addresses or (unless the context
// carries an external-upstream policy via WithAllowExternalUpstream) any
// other public address.
func HTTPChatCompletion(ctx context.Context, upstream string, messages []ChatMessage, model string) (string, error) {
	reqBody := chatCompletionRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("ollama backend unavailable: marshal request: %w", err)
	}

	client := clientForContext(ctx)

	reqCtx, cancel := context.WithTimeout(ctx, overallTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, upstream+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("ollama backend unavailable: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama backend unavailable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body := readTruncatedErrorBody(resp.Body)
		return "", fmt.Errorf("ollama backend error (HTTP %d): %s", resp.StatusCode, body)
	}

	body, err := readCappedBody(resp.Body, maxResponseBodyBytes)
	if err != nil {
		return "", fmt.Errorf("ollama backend unavailable: read response: %w", err)
	}

	var result chatCompletionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("ollama backend returned unparseable response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("ollama backend returned empty response")
	}

	return result.Choices[0].Message.Content, nil
}

type streamDelta struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// HTTPChatCompletionStream sends a streaming chat completion request and
// invokes fn for each content delta. Returns when the stream ends or on
// error.
//
// The request never follows redirects and never uses an HTTP(S) proxy; dial
// policy matches HTTPChatCompletion. There is no overall deadline (streams
// may legitimately run long), but an idle timer bounds any single stall: if
// no line is read within 60s of the last one (or of the stream starting),
// the request is cancelled.
func HTTPChatCompletionStream(ctx context.Context, upstream string, messages []ChatMessage, model string, fn func(delta string) error) error {
	reqBody := chatCompletionRequest{
		Model:    model,
		Messages: messages,
		Stream:   true,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("ollama backend unavailable: marshal request: %w", err)
	}

	client := clientForContext(ctx)

	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, upstream+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("ollama backend unavailable: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	var idleTimedOut atomic.Bool
	timer := time.AfterFunc(streamIdleTimeout, func() {
		idleTimedOut.Store(true)
		cancel()
	})
	defer timer.Stop()

	resp, err := client.Do(req)
	if err != nil {
		if idleTimedOut.Load() {
			return fmt.Errorf("ollama backend unavailable: stream idle timeout exceeded before response headers arrived")
		}
		return fmt.Errorf("ollama backend unavailable: %w", err)
	}
	defer resp.Body.Close()
	// A stray AfterFunc fire can race with this Reset if it lands at nearly
	// the same instant real progress is made (client.Do returning, or a
	// line being scanned): time.Timer.Reset does not stop a callback that
	// has already started running. Clearing the flag here means a stray
	// fire only sticks if no further progress follows it — a genuine
	// stall, not a scheduling race on a successful stream.
	idleTimedOut.Store(false)
	timer.Reset(streamIdleTimeout)

	if resp.StatusCode != http.StatusOK {
		body := readTruncatedErrorBody(resp.Body)
		return fmt.Errorf("ollama backend error (HTTP %d): %s", resp.StatusCode, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScanLineBytes)
	for scanner.Scan() {
		idleTimedOut.Store(false)
		timer.Reset(streamIdleTimeout)
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var delta streamDelta
		if err := json.Unmarshal([]byte(payload), &delta); err != nil {
			continue
		}
		if len(delta.Choices) == 0 {
			continue
		}
		content := delta.Choices[0].Delta.Content
		if content == "" {
			continue
		}
		if err := fn(content); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		if idleTimedOut.Load() {
			return fmt.Errorf("ollama backend unavailable: stream idle timeout exceeded: %w", err)
		}
		return fmt.Errorf("ollama backend unavailable: %w", err)
	}
	if idleTimedOut.Load() {
		return errors.New("ollama backend unavailable: stream idle timeout exceeded")
	}

	return nil
}
