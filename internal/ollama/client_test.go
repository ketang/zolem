package ollama

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type fakeCLI struct {
	outputs        map[string]fakeResult
	lookPathResult string
	lookPathErr    error
	lookPathCalls  int
}

type fakeResult struct {
	output string
	err    error
}

func (f *fakeCLI) lookPath(string) (string, error) {
	f.lookPathCalls++
	return f.lookPathResult, f.lookPathErr
}

func (f *fakeCLI) run(_ context.Context, binary string, args ...string) ([]byte, error) {
	key := binary + " " + strings.Join(args, " ")
	result, ok := f.outputs[key]
	if !ok {
		return nil, errors.New("unexpected command: " + key)
	}
	return []byte(result.output), result.err
}

func TestDetectUsesConfiguredBinaryPathWithoutPATHLookup(t *testing.T) {
	binaryPath := writeExecutable(t)
	cli := &fakeCLI{
		lookPathResult: "/usr/bin/ollama",
		outputs: map[string]fakeResult{
			binaryPath + " list":            {output: "NAME ID SIZE MODIFIED\ngemma4:e4b abc 9.6 GB now\n"},
			binaryPath + " show gemma4:e4b": {output: eligibleShowOutput},
		},
	}

	client, warnings := detector{
		lookPath: cli.lookPath,
		run:      cli.run,
	}.detect(context.Background(), Config{
		BinaryPath: binaryPath,
		Model:      "gemma4:e4b",
	})

	if cli.lookPathCalls != 0 {
		t.Fatalf("expected no PATH lookup, got %d calls", cli.lookPathCalls)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if client == nil || client.SelectedModel() != "gemma4:e4b" {
		t.Fatalf("unexpected client: %#v", client)
	}
}

func TestDetectDoesNotFallbackToPATHWhenConfiguredBinaryIsInvalid(t *testing.T) {
	cli := &fakeCLI{
		lookPathResult: "/usr/bin/ollama",
	}

	client, warnings := detector{
		lookPath: cli.lookPath,
		run:      cli.run,
	}.detect(context.Background(), Config{
		BinaryPath: "/missing/ollama",
	})

	if cli.lookPathCalls != 0 {
		t.Fatalf("expected no PATH lookup, got %d calls", cli.lookPathCalls)
	}
	if client != nil {
		t.Fatalf("expected nil client, got %#v", client)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "configured ollama binary path") {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
}

func TestDetectRejectsConfiguredIneligibleModel(t *testing.T) {
	binaryPath := writeExecutable(t)
	cli := &fakeCLI{
		outputs: map[string]fakeResult{
			binaryPath + " list":            {output: "NAME ID SIZE MODIFIED\ngemma4:e4b abc 9.6 GB now\n"},
			binaryPath + " show gemma4:e4b": {output: ineligibleShowOutput},
		},
	}

	client, warnings := detector{
		lookPath: cli.lookPath,
		run:      cli.run,
	}.detect(context.Background(), Config{
		BinaryPath: binaryPath,
		Model:      "gemma4:e4b",
	})

	if client != nil {
		t.Fatalf("expected nil client, got %#v", client)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "not eligible") {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
}

func TestDetectSelectsFirstEligibleModelInSortedOrder(t *testing.T) {
	binaryPath := writeExecutable(t)
	cli := &fakeCLI{
		outputs: map[string]fakeResult{
			binaryPath + " list":              {output: "NAME ID SIZE MODIFIED\nzeta:latest abc 9.6 GB now\nalpha:latest def 9.6 GB now\n"},
			binaryPath + " show alpha:latest": {output: eligibleShowOutput},
			binaryPath + " show zeta:latest":  {output: eligibleShowOutput},
		},
	}

	client, warnings := detector{
		lookPath: cli.lookPath,
		run:      cli.run,
	}.detect(context.Background(), Config{
		BinaryPath: binaryPath,
	})

	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if client == nil || client.SelectedModel() != "alpha:latest" {
		t.Fatalf("unexpected client: %#v", client)
	}
}

func TestCleanCLIOutputRemovesSpinnerNoise(t *testing.T) {
	raw := "\x1b[?2026h\x1b[?25l\x1b[1G⠋ \x1b[K\x1b[?25h\x1b[?2026lok\x1b[?25l\x1b[?25h\n"
	if got := cleanCLIOutput(raw); got != "ok" {
		t.Fatalf("cleaned output: got %q, want ok", got)
	}
}

func writeExecutable(t *testing.T) string {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "ollama-*")
	if err != nil {
		t.Fatalf("create temp executable: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close temp executable: %v", err)
	}
	if err := os.Chmod(file.Name(), 0o755); err != nil {
		t.Fatalf("chmod temp executable: %v", err)
	}
	return file.Name()
}

const eligibleShowOutput = `
  Model
    parameters          8.0B
    context length      131072

  Capabilities
    completion
    tools
`

const ineligibleShowOutput = `
  Model
    parameters          2.0B
    context length      4096

  Capabilities
    tools
`
