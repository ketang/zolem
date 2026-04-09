package ollama

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MinContextLength  = 8192
	MinParameterCount = uint64(4_000_000_000)
	probeTimeout      = 5 * time.Second
)

type Config struct {
	BinaryPath string
	Model      string
}

type Client struct {
	binaryPath string
	model      string
	run        commandRunner
}

type commandRunner func(context.Context, string, ...string) ([]byte, error)

type detector struct {
	lookPath func(string) (string, error)
	run      commandRunner
}

type modelInfo struct {
	Name           string
	ContextLength  int
	ParameterCount uint64
	Capabilities   map[string]bool
}

func Detect(ctx context.Context, cfg Config) (*Client, []string) {
	d := detector{
		lookPath: exec.LookPath,
		run:      runCommand,
	}
	return d.detect(ctx, cfg)
}

func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	out, err := c.run(ctx, c.binaryPath, "run", c.model, prompt, "--nowordwrap", "--hidethinking")
	cleaned := cleanCLIOutput(string(out))
	if err != nil {
		if cleaned != "" {
			return "", fmt.Errorf("ollama run %q: %w: %s", c.model, err, cleaned)
		}
		return "", fmt.Errorf("ollama run %q: %w", c.model, err)
	}
	if cleaned == "" {
		return "", errors.New("ollama returned an empty response")
	}
	return cleaned, nil
}

func (c *Client) SelectedModel() string {
	return c.model
}

func (d detector) detect(ctx context.Context, cfg Config) (*Client, []string) {
	binaryPath, warnings, ok := d.resolveBinary(cfg.BinaryPath)
	if !ok {
		return nil, warnings
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	listOutput, err := d.run(probeCtx, binaryPath, "list")
	if err != nil {
		return nil, append(warnings, fmt.Sprintf("ollama detected at %q but model listing failed: %v", binaryPath, err))
	}

	models := parseListOutput(string(listOutput))
	if len(models) == 0 {
		return nil, append(warnings, fmt.Sprintf("ollama detected at %q but no installed models were found", binaryPath))
	}

	candidates := append([]string(nil), models...)
	if cfg.Model != "" {
		candidates = []string{cfg.Model}
	} else {
		sort.Strings(candidates)
	}

	for _, name := range candidates {
		showOutput, err := d.run(probeCtx, binaryPath, "show", name)
		if err != nil {
			if cfg.Model != "" {
				return nil, append(warnings, fmt.Sprintf("configured ollama model %q could not be inspected: %v", name, err))
			}
			continue
		}

		info, err := parseShowOutput(name, string(showOutput))
		if err != nil {
			if cfg.Model != "" {
				return nil, append(warnings, fmt.Sprintf("configured ollama model %q returned unparseable metadata: %v", name, err))
			}
			continue
		}

		reasons := ineligibilityReasons(info)
		if len(reasons) == 0 {
			return &Client{
				binaryPath: binaryPath,
				model:      name,
				run:        d.run,
			}, warnings
		}

		if cfg.Model != "" {
			return nil, append(warnings, fmt.Sprintf("configured ollama model %q is not eligible: %s", name, strings.Join(reasons, ", ")))
		}
	}

	if cfg.Model != "" {
		return nil, append(warnings, fmt.Sprintf("configured ollama model %q was not found in the installed model list", cfg.Model))
	}
	return nil, append(warnings, fmt.Sprintf("ollama detected at %q but no eligible models were found", binaryPath))
}

func (d detector) resolveBinary(configuredPath string) (string, []string, bool) {
	if configuredPath != "" {
		if !strings.Contains(configuredPath, "/") {
			return "", []string{fmt.Sprintf("configured ollama binary path %q must include a path separator", configuredPath)}, false
		}
		if _, err := os.Stat(configuredPath); err != nil {
			return "", []string{fmt.Sprintf("configured ollama binary path %q is not usable: %v", configuredPath, err)}, false
		}
		return configuredPath, nil, true
	}

	binaryPath, err := d.lookPath("ollama")
	if err != nil {
		return "", nil, false
	}
	return binaryPath, nil, true
}

func parseListOutput(output string) []string {
	lines := strings.Split(output, "\n")
	models := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "NAME") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		models = append(models, fields[0])
	}
	return models
}

func parseShowOutput(name, output string) (modelInfo, error) {
	info := modelInfo{
		Name:         name,
		Capabilities: make(map[string]bool),
	}

	section := ""
	for _, rawLine := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			continue
		}

		switch trimmed {
		case "Model", "Capabilities", "Parameters", "License":
			section = trimmed
			continue
		}

		if section == "Model" {
			if value, ok := parseIndentedValue(trimmed, "parameters"); ok {
				count, err := parseParameterCount(value)
				if err != nil {
					return modelInfo{}, err
				}
				info.ParameterCount = count
			}
			if value, ok := parseIndentedValue(trimmed, "context length"); ok {
				contextLength, err := strconv.Atoi(strings.ReplaceAll(value, ",", ""))
				if err != nil {
					return modelInfo{}, fmt.Errorf("parse context length %q: %w", value, err)
				}
				info.ContextLength = contextLength
			}
			continue
		}

		if section == "Capabilities" {
			info.Capabilities[trimmed] = true
		}
	}

	if info.ParameterCount == 0 {
		return modelInfo{}, errors.New("missing parameter count")
	}
	if info.ContextLength == 0 {
		return modelInfo{}, errors.New("missing context length")
	}
	return info, nil
}

func parseIndentedValue(line, prefix string) (string, bool) {
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if value == "" {
		return "", false
	}
	return value, true
}

func ineligibilityReasons(info modelInfo) []string {
	var reasons []string
	if !info.Capabilities["completion"] {
		reasons = append(reasons, "missing completion capability")
	}
	if info.ContextLength < MinContextLength {
		reasons = append(reasons, fmt.Sprintf("context length %d is below %d", info.ContextLength, MinContextLength))
	}
	if info.ParameterCount < MinParameterCount {
		reasons = append(reasons, fmt.Sprintf("parameter count %d is below %d", info.ParameterCount, MinParameterCount))
	}
	return reasons
}

var parameterCountPattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)([KMBT])$`)

func parseParameterCount(value string) (uint64, error) {
	matches := parameterCountPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 3 {
		return 0, fmt.Errorf("parse parameter count %q", value)
	}

	number, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("parse parameter count %q: %w", value, err)
	}

	multiplier := float64(1)
	switch matches[2] {
	case "K":
		multiplier = 1_000
	case "M":
		multiplier = 1_000_000
	case "B":
		multiplier = 1_000_000_000
	case "T":
		multiplier = 1_000_000_000_000
	default:
		return 0, fmt.Errorf("unsupported parameter suffix %q", matches[2])
	}

	return uint64(number * multiplier), nil
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func cleanCLIOutput(output string) string {
	output = ansiPattern.ReplaceAllString(output, "")
	output = strings.Map(func(r rune) rune {
		switch {
		case r >= 0x2800 && r <= 0x28ff:
			return -1
		case r < 32 && r != '\n' && r != '\t':
			return -1
		default:
			return r
		}
	}, output)

	lines := strings.Split(output, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

func runCommand(ctx context.Context, binaryPath string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Env = append(os.Environ(), "TERM=dumb", "OLLAMA_NOHISTORY=1")
	return cmd.CombinedOutput()
}
