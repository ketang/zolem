package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"zolem.dev/zolem/internal/specs"
)

// trackingLoader records Refresh calls and returns configurable results.
type trackingLoader struct {
	mu        sync.Mutex
	calls     []string
	fallbacks map[string]loadResult
	refreshes map[string]loadResult
}

func (t *trackingLoader) LoadFallback(source specs.ContractSource) (specs.NormalizedSchema, error) {
	if result, ok := t.fallbacks[source.Key()]; ok {
		return result.schema, result.err
	}
	return specs.NormalizedSchema{}, errors.New("no fallback")
}

func (t *trackingLoader) Refresh(source specs.ContractSource) (specs.NormalizedSchema, error) {
	t.mu.Lock()
	t.calls = append(t.calls, source.Key())
	result := t.refreshes[source.Key()]
	t.mu.Unlock()
	return result.schema, result.err
}

func (t *trackingLoader) refreshCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.calls)
}

func (t *trackingLoader) setRefresh(key string, result loadResult) {
	t.mu.Lock()
	t.refreshes[key] = result
	t.mu.Unlock()
}

func testRegistry() specs.Registry {
	// Use the default registry which includes sources with and without remotes.
	return specs.DefaultRegistry()
}

func collectLogs(t *testing.T) (func(string, ...any), func() []string) {
	t.Helper()
	var mu sync.Mutex
	var logs []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		msg := format
		if len(args) > 0 {
			msg = fmt.Sprintf(format, args...)
		}
		logs = append(logs, msg)
	}
	getLogs := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(logs))
		copy(out, logs)
		return out
	}
	return logf, getLogs
}

func TestRefreshContracts_Success(t *testing.T) {
	validator := specs.NewValidator()
	// Pre-load a fallback schema for openai so we can verify it gets replaced.
	if err := validator.LoadNormalized("openai", "v1", specs.NormalizedSchema{Bytes: []byte(startupMinimalOpenAISchema)}); err != nil {
		t.Fatal(err)
	}

	// New schema requires "model" and "messages" and "temperature" — different from fallback.
	updatedSchema := `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["model", "messages", "temperature"],
  "properties": {
    "model": {"type": "string"},
    "messages": {"type": "array"},
    "temperature": {"type": "number"}
  },
  "additionalProperties": true
}`

	loader := &trackingLoader{
		refreshes: map[string]loadResult{
			"openai:v1":     {schema: specs.NormalizedSchema{Bytes: []byte(updatedSchema)}},
			"openrouter:v1": {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalOpenAISchema)}},
			"gemini:v1":     {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalGeminiSchema)}},
			"gemini:v1beta": {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalGeminiSchema)}},
		},
	}

	logf, getLogs := collectLogs(t)
	refreshContracts(testRegistry(), loader, validator, logf)

	// Should have refreshed only sources with remotes (4 sources, not anthropic).
	if n := loader.refreshCount(); n != 4 {
		t.Fatalf("expected 4 refresh calls, got %d", n)
	}

	// Verify the updated schema is now active — missing temperature should fail.
	err := validator.Validate("openai", "v1", []byte(`{"model":"gpt-4o","messages":[]}`))
	if err == nil {
		t.Fatal("expected validation error for missing temperature after refresh")
	}

	// With all required fields, should pass.
	err = validator.Validate("openai", "v1", []byte(`{"model":"gpt-4o","messages":[],"temperature":0.5}`))
	if err != nil {
		t.Fatalf("expected validation to pass with updated schema, got %v", err)
	}

	logs := getLogs()
	if !containsLog(logs, "spec refreshed: openai:v1") {
		t.Fatalf("expected success log for openai:v1, got %v", logs)
	}
}

func TestRefreshContracts_FetchFailure_KeepsLastGood(t *testing.T) {
	validator := specs.NewValidator()
	// Pre-load fallback.
	if err := validator.LoadNormalized("openai", "v1", specs.NormalizedSchema{Bytes: []byte(startupMinimalOpenAISchema)}); err != nil {
		t.Fatal(err)
	}

	loader := &trackingLoader{
		refreshes: map[string]loadResult{
			"openai:v1":     {err: errors.New("network timeout")},
			"openrouter:v1": {err: errors.New("network timeout")},
			"gemini:v1":     {err: errors.New("network timeout")},
			"gemini:v1beta": {err: errors.New("network timeout")},
		},
	}

	logf, getLogs := collectLogs(t)
	refreshContracts(testRegistry(), loader, validator, logf)

	// Fallback schema should still validate correctly.
	err := validator.Validate("openai", "v1", []byte(`{"model":"gpt-4o","messages":[]}`))
	if err != nil {
		t.Fatalf("expected fallback schema to still work after failed refresh, got %v", err)
	}

	logs := getLogs()
	if !containsLog(logs, "spec refresh failed for openai:v1") {
		t.Fatalf("expected failure log, got %v", logs)
	}
}

func TestRefreshContracts_InvalidSchema_KeepsLastGood(t *testing.T) {
	validator := specs.NewValidator()
	if err := validator.LoadNormalized("openai", "v1", specs.NormalizedSchema{Bytes: []byte(startupMinimalOpenAISchema)}); err != nil {
		t.Fatal(err)
	}

	loader := &trackingLoader{
		refreshes: map[string]loadResult{
			"openai:v1":     {schema: specs.NormalizedSchema{Bytes: []byte(`not valid json`)}},
			"openrouter:v1": {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalOpenAISchema)}},
			"gemini:v1":     {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalGeminiSchema)}},
			"gemini:v1beta": {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalGeminiSchema)}},
		},
	}

	logf, getLogs := collectLogs(t)
	refreshContracts(testRegistry(), loader, validator, logf)

	// Fallback schema for openai should still work.
	err := validator.Validate("openai", "v1", []byte(`{"model":"gpt-4o","messages":[]}`))
	if err != nil {
		t.Fatalf("expected fallback schema to survive invalid refresh, got %v", err)
	}

	logs := getLogs()
	if !containsLog(logs, "spec refresh compile failed for openai:v1") {
		t.Fatalf("expected compile failure log, got %v", logs)
	}
}

func TestStartRefreshLoop_Shutdown(t *testing.T) {
	loader := &trackingLoader{
		refreshes: map[string]loadResult{
			"openai:v1":     {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalOpenAISchema)}},
			"openrouter:v1": {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalOpenAISchema)}},
			"gemini:v1":     {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalGeminiSchema)}},
			"gemini:v1beta": {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalGeminiSchema)}},
		},
	}

	validator := specs.NewValidator()
	logf, _ := collectLogs(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := startRefreshLoop(ctx, 24*time.Hour, testRegistry(), loader, validator, logf)

	// Cancel immediately — loop should exit without performing any refresh.
	cancel()

	select {
	case <-done:
		// Clean shutdown.
	case <-time.After(2 * time.Second):
		t.Fatal("refresh loop did not shut down within 2s")
	}

	if n := loader.refreshCount(); n != 0 {
		t.Fatalf("expected 0 refresh calls on immediate shutdown, got %d", n)
	}
}

func TestStartRefreshLoop_TicksAndShutdown(t *testing.T) {
	loader := &trackingLoader{
		refreshes: map[string]loadResult{
			"openai:v1":     {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalOpenAISchema)}},
			"openrouter:v1": {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalOpenAISchema)}},
			"gemini:v1":     {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalGeminiSchema)}},
			"gemini:v1beta": {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalGeminiSchema)}},
		},
	}

	validator := specs.NewValidator()
	logf, _ := collectLogs(t)

	ctx, cancel := context.WithCancel(context.Background())
	// Very short interval to trigger at least one tick.
	done := startRefreshLoop(ctx, 10*time.Millisecond, testRegistry(), loader, validator, logf)

	// Wait for at least one refresh cycle.
	deadline := time.After(2 * time.Second)
	for {
		if loader.refreshCount() >= 4 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for refresh, got %d calls", loader.refreshCount())
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh loop did not shut down within 2s")
	}
}

func containsLog(logs []string, substr string) bool {
	for _, l := range logs {
		if contains(l, substr) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
