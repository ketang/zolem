package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ketang/zolem/internal/adminapi"
	"github.com/ketang/zolem/internal/admincli"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// Type aliases for the canonical wire types in internal/adminapi.
type (
	localProfilePayload  = adminapi.ProfilePayload
	localListenerPayload = adminapi.ListenerPayload
	localListenerView    = adminapi.ListenerView
	listenerStateView    = adminapi.ListenerStateView
)

// localProfileView is the response shape for profile create/list operations.
// The server returns runtimecfg.RuntimeProfile; this type captures the fields
// zolemc displays.
type localProfileView = runtimecfg.RuntimeProfile

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	opts := admincli.Options{}
	fs := flag.NewFlagSet("zolemc", flag.ContinueOnError)
	var parseOutput bytes.Buffer
	fs.SetOutput(&parseOutput)
	fs.StringVar(&opts.AdminURL, "admin-url", "http://127.0.0.1:8090", "admin control-plane base URL")
	fs.StringVar(&opts.BaseURL, "base-url", "", "listener data-plane base URL; required only by the listener and request commands")
	fs.BoolVar(&opts.JSON, "json", false, "write machine-readable JSON")
	fs.DurationVar(&opts.Timeout, "timeout", 10*time.Second, "HTTP request timeout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(stdout)
			return nil
		}
		if parseOutput.Len() > 0 {
			if _, writeErr := stderr.Write(parseOutput.Bytes()); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	if fs.NArg() == 0 {
		usage(stderr)
		return errors.New("missing command")
	}

	client := admincli.NewClient(opts.AdminURL, &http.Client{Timeout: opts.Timeout})

	switch fs.Arg(0) {
	case "health":
		// Global flags only bind before the command, so a flag placed after a
		// no-arg command (e.g. `zolemc health -json`) lands here as a trailing
		// arg the flag package never parsed. Reject it rather than silently
		// ignoring it.
		if extra := fs.Args()[1:]; len(extra) > 0 {
			return errUnexpectedArgs("health", extra)
		}
		return runAdminHealth(ctx, client, opts, stdout)
	case "profiles":
		return runProfiles(ctx, client, opts, fs.Args()[1:], stdout, stderr)
	case "listeners":
		return runListeners(ctx, client, opts, fs.Args()[1:], stdout, stderr)
	case "listener":
		return runListener(ctx, opts, fs.Args()[1:], stdout, stderr)
	case "request":
		return runProviderRequest(ctx, opts, fs.Args()[1:], stdout, stderr)
	case "help", "-help", "--help":
		usage(stdout)
		return nil
	default:
		usage(stderr)
		return fmt.Errorf("unknown command %q", fs.Arg(0))
	}
}

func runAdminHealth(ctx context.Context, client admincli.Client, opts admincli.Options, stdout io.Writer) error {
	var payload map[string]string
	if err := client.GetJSON(ctx, "/_zolem/health", &payload); err != nil {
		return err
	}
	if opts.JSON {
		return writeJSONObject(stdout, payload)
	}
	fmt.Fprintf(stdout, "admin health: %s\n", payload["status"])
	return nil
}

func runProfiles(ctx context.Context, client admincli.Client, opts admincli.Options, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("profiles requires list, create, or delete")
	}
	switch args[0] {
	case "list":
		if extra := args[1:]; len(extra) > 0 {
			return errUnexpectedArgs("profiles list", extra)
		}
		var raw json.RawMessage
		if err := client.GetJSON(ctx, "/_zolem/profiles", &raw); err != nil {
			return err
		}
		if opts.JSON {
			return writeRawJSON(stdout, raw)
		}
		var profiles []localProfileView
		if err := json.Unmarshal(raw, &profiles); err != nil {
			return fmt.Errorf("decode profiles response: %w", err)
		}
		if len(profiles) == 0 {
			fmt.Fprintln(stdout, "no profiles")
			return nil
		}
		for _, profile := range profiles {
			fmt.Fprintf(stdout, "%s\tbackend=%s\n", profile.Name, profile.Backend)
		}
		return nil
	case "create":
		fs := flag.NewFlagSet("zolemc profiles create", flag.ContinueOnError)
		fs.SetOutput(stderr)
		payload := localProfilePayload{}
		var seed int64
		var seedSet bool
		var wasmModuleFile string
		var wasmTimeoutMS int
		var streamDelayMS, streamDelayMinMS, streamDelayMaxMS int
		name, flagArgs := splitOptionalLeadingName(args[1:])
		fs.StringVar(&payload.Backend, "backend", "lorem", "backend: lorem, faker, fixture, ollama, wasm, or error")
		fs.StringVar(&payload.BackendModel, "backend-model", "", "backend model override")
		fs.StringVar(&payload.ErrorType, "error-type", "", "error backend type")
		fs.StringVar(&payload.ResponseModelPolicy, "response-model-policy", "", "response model policy")
		fs.StringVar(&payload.ResponseModel, "response-model", "", "response model override")
		fs.StringVar(&payload.FixtureNamespace, "fixture-namespace", "", "relative fixture namespace")
		fs.Int64Var(&seed, "seed", 0, "deterministic random seed")
		fs.BoolVar(&seedSet, "seed-set", false, "include the -seed value in the profile payload")
		fs.StringVar(&wasmModuleFile, "wasm-module-file", "", "binary WASM generator module file; implies -backend wasm when -backend is unset")
		fs.IntVar(&wasmTimeoutMS, "wasm-timeout-ms", 0, "WASM generation timeout in milliseconds; omitted when unset")
		fs.StringVar(&payload.OllamaUpstream, "ollama-upstream", "", "ollama upstream URL (loopback or RFC1918 only, e.g. http://127.0.0.1:11434)")
		fs.BoolVar(&payload.AllowExternalOllamaUpstream, "allow-external-ollama-upstream", false, "allow ollama-upstream to point outside loopback/RFC1918")
		fs.StringVar(&payload.StreamDelay.Mode, "stream-delay-mode", "", "streaming pacing mode: fixed, uniform, or token")
		fs.IntVar(&streamDelayMS, "stream-delay-ms", 0, "fixed streaming delay in milliseconds")
		fs.IntVar(&streamDelayMinMS, "stream-delay-min-ms", 0, "minimum streaming delay in milliseconds (uniform mode)")
		fs.IntVar(&streamDelayMaxMS, "stream-delay-max-ms", 0, "maximum streaming delay in milliseconds (uniform mode)")
		if err := fs.Parse(flagArgs); err != nil {
			return err
		}
		if name == "" && fs.NArg() == 1 {
			name = fs.Arg(0)
		}
		if name == "" || fs.NArg() > 1 {
			return errors.New("profiles create requires exactly one profile name")
		}
		if seedSet {
			payload.Seed = &seed
		}
		if flagWasSet(fs, "stream-delay-ms") {
			payload.StreamDelay.MS = streamDelayMS
		}
		if flagWasSet(fs, "stream-delay-min-ms") {
			payload.StreamDelay.MinMS = streamDelayMinMS
		}
		if flagWasSet(fs, "stream-delay-max-ms") {
			payload.StreamDelay.MaxMS = streamDelayMaxMS
		}
		backendSet := flagWasSet(fs, "backend")
		wasmTimeoutSet := flagWasSet(fs, "wasm-timeout-ms")
		if wasmModuleFile != "" {
			if backendSet && payload.Backend != "wasm" {
				return fmt.Errorf("-wasm-module-file requires -backend wasm, got %q", payload.Backend)
			}
			payload.Backend = "wasm"
			wasmBytes, err := os.ReadFile(wasmModuleFile)
			if err != nil {
				return fmt.Errorf("read -wasm-module-file %q: %w", wasmModuleFile, err)
			}
			payload.WASMModuleBase64 = base64.StdEncoding.EncodeToString(wasmBytes)
		}
		if wasmTimeoutSet {
			if wasmTimeoutMS < 0 {
				return errors.New("-wasm-timeout-ms must be non-negative")
			}
			if payload.Backend != "wasm" {
				return errors.New("-wasm-timeout-ms requires -backend wasm or -wasm-module-file")
			}
			payload.WASMGenerateTimeoutMS = &wasmTimeoutMS
		}
		var raw json.RawMessage
		if err := client.PutJSON(ctx, "/_zolem/profiles/"+url.PathEscape(name), payload, &raw); err != nil {
			return err
		}
		if opts.JSON {
			return writeRawJSON(stdout, raw)
		}
		var profile localProfileView
		if err := json.Unmarshal(raw, &profile); err != nil {
			return fmt.Errorf("decode profile response: %w", err)
		}
		fmt.Fprintf(stdout, "profile %s created\n", profile.Name)
		return nil
	case "delete":
		if len(args) != 2 {
			return errors.New("profiles delete requires exactly one profile name")
		}
		if err := client.Delete(ctx, "/_zolem/profiles/"+url.PathEscape(args[1])); err != nil {
			return err
		}
		if opts.JSON {
			return writeJSONObject(stdout, map[string]string{"deleted": args[1]})
		}
		fmt.Fprintf(stdout, "profile %s deleted\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown profiles command %q", args[0])
	}
}

func runListeners(ctx context.Context, client admincli.Client, opts admincli.Options, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("listeners requires list, create, delete, or calls")
	}
	switch args[0] {
	case "calls":
		return runListenerCalls(ctx, client, opts, args[1:], stdout, stderr)
	case "list":
		if extra := args[1:]; len(extra) > 0 {
			return errUnexpectedArgs("listeners list", extra)
		}
		var raw json.RawMessage
		if err := client.GetJSON(ctx, "/_zolem/listeners", &raw); err != nil {
			return err
		}
		if opts.JSON {
			return writeRawJSON(stdout, raw)
		}
		var listeners []localListenerView
		if err := json.Unmarshal(raw, &listeners); err != nil {
			return fmt.Errorf("decode listeners response: %w", err)
		}
		if len(listeners) == 0 {
			fmt.Fprintln(stdout, "no listeners")
			return nil
		}
		for _, listener := range listeners {
			fmt.Fprintf(stdout, "%s\t%s\tprofile=%s\t%s\n", listener.Name, listener.Provider, listener.Profile, listener.BaseURL)
		}
		return nil
	case "create":
		fs := flag.NewFlagSet("zolemc listeners create", flag.ContinueOnError)
		fs.SetOutput(stderr)
		payload := localListenerPayload{Addr: "127.0.0.1:0"}
		name, flagArgs := splitOptionalLeadingName(args[1:])
		fs.StringVar(&payload.Addr, "addr", payload.Addr, "listener loopback address")
		fs.StringVar(&payload.Provider, "provider", "", "provider: anthropic, openai, or gemini")
		fs.StringVar(&payload.Profile, "profile", "", "profile name")
		fs.BoolVar(&payload.TLS, "tls", false, "request a TLS listener")
		if err := fs.Parse(flagArgs); err != nil {
			return err
		}
		if name == "" && fs.NArg() == 1 {
			name = fs.Arg(0)
		}
		if name == "" || fs.NArg() > 1 {
			return errors.New("listeners create requires exactly one listener name")
		}
		var raw json.RawMessage
		if err := client.PutJSON(ctx, "/_zolem/listeners/"+url.PathEscape(name), payload, &raw); err != nil {
			return err
		}
		if opts.JSON {
			return writeRawJSON(stdout, raw)
		}
		var listener localListenerView
		if err := json.Unmarshal(raw, &listener); err != nil {
			return fmt.Errorf("decode listener response: %w", err)
		}
		fmt.Fprintf(stdout, "listener %s created: %s\n", listener.Name, listener.BaseURL)
		return nil
	case "delete":
		if len(args) != 2 {
			return errors.New("listeners delete requires exactly one listener name")
		}
		if err := client.Delete(ctx, "/_zolem/listeners/"+url.PathEscape(args[1])); err != nil {
			return err
		}
		if opts.JSON {
			return writeJSONObject(stdout, map[string]string{"deleted": args[1]})
		}
		fmt.Fprintf(stdout, "listener %s deleted\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown listeners command %q", args[0])
	}
}

func runListenerCalls(ctx context.Context, client admincli.Client, opts admincli.Options, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("calls requires list or clear")
	}
	switch args[0] {
	case "list":
		return runListenerCallsList(ctx, client, opts, args[1:], stdout, stderr)
	case "clear":
		return runListenerCallsClear(ctx, client, opts, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown calls command %q", args[0])
	}
}

type recordedCallView struct {
	CallID     int64  `json:"call_id"`
	ReceivedAt string `json:"received_at"`
	LatencyMS  int64  `json:"latency_ms"`
	Request    struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	} `json:"request"`
	Response struct {
		Status int             `json:"status"`
		Stream json.RawMessage `json:"stream"`
	} `json:"response"`
}

func runListenerCallsList(ctx context.Context, client admincli.Client, opts admincli.Options, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("zolemc listeners calls list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var since int64
	fs.Int64Var(&since, "since", 0, "only show calls with call_id greater than this id")
	name, flagArgs := splitOptionalLeadingName(args)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if name == "" && fs.NArg() == 1 {
		name = fs.Arg(0)
	}
	if name == "" || fs.NArg() > 1 {
		return errors.New("calls list requires exactly one listener name")
	}

	var raw json.RawMessage
	if err := client.GetJSON(ctx, "/_zolem/listeners/"+url.PathEscape(name)+"/calls", &raw); err != nil {
		return err
	}

	if opts.JSON {
		if since <= 0 {
			// No client-side filter: pass the complete API bytes through so
			// request/response headers and bodies survive intact.
			return writeRawJSON(stdout, raw)
		}
		// With -since we still emit complete call records: filter on call_id
		// but keep each call's raw bytes rather than re-marshaling through the
		// narrow recordedCallView, which would drop headers and bodies. Sibling
		// envelope fields are preserved, so each retained call carries the same
		// fields it would in the unfiltered passthrough (the envelope is
		// re-marshaled, so top-level key order may differ).
		filtered, err := filterCallsRawSince(raw, since)
		if err != nil {
			return err
		}
		return writeRawJSON(stdout, filtered)
	}

	var envelope struct {
		Calls []recordedCallView `json:"calls"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode calls response: %w", err)
	}

	filtered := envelope.Calls
	if since > 0 {
		kept := make([]recordedCallView, 0, len(filtered))
		for _, c := range filtered {
			if c.CallID > since {
				kept = append(kept, c)
			}
		}
		filtered = kept
	}

	if len(filtered) == 0 {
		fmt.Fprintln(stdout, "no calls")
		return nil
	}

	fmt.Fprintf(stdout, "%-4s %-7s %-26s %-7s %-10s %s\n", "ID", "METHOD", "PATH", "STATUS", "LATENCY_MS", "RECEIVED_AT")
	for _, c := range filtered {
		status := fmt.Sprintf("%d", c.Response.Status)
		if len(c.Response.Stream) > 0 && string(c.Response.Stream) != "null" {
			status = "~" + status
		}
		fmt.Fprintf(stdout, "%-4d %-7s %-26s %-7s %-10d %s\n",
			c.CallID, c.Request.Method, c.Request.Path, status, c.LatencyMS, c.ReceivedAt)
	}
	return nil
}

func runListenerCallsClear(ctx context.Context, client admincli.Client, opts admincli.Options, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("zolemc listeners calls clear", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name, flagArgs := splitOptionalLeadingName(args)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if name == "" && fs.NArg() == 1 {
		name = fs.Arg(0)
	}
	if name == "" || fs.NArg() > 1 {
		return errors.New("calls clear requires exactly one listener name")
	}

	var resp struct {
		Cleared int `json:"cleared"`
	}
	if err := client.DoJSON(ctx, http.MethodDelete, "/_zolem/listeners/"+url.PathEscape(name)+"/calls", nil, &resp); err != nil {
		return err
	}
	if opts.JSON {
		return writeJSONObject(stdout, map[string]int{"cleared": resp.Cleared})
	}
	fmt.Fprintf(stdout, "cleared %d calls from listener %s\n", resp.Cleared, name)
	return nil
}

func runListener(ctx context.Context, opts admincli.Options, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("listener requires health or state")
	}
	sub := args[0]
	if sub != "health" && sub != "state" {
		return fmt.Errorf("unknown listener command %q", sub)
	}
	if extra := args[1:]; len(extra) > 0 {
		return errUnexpectedArgs("listener "+sub, extra)
	}
	if opts.BaseURL == "" {
		return errors.New("listener commands require -base-url")
	}
	client := admincli.NewClient(opts.BaseURL, &http.Client{Timeout: opts.Timeout})
	switch sub {
	case "health":
		var payload map[string]string
		if err := client.GetJSON(ctx, "/_zolem/health", &payload); err != nil {
			return err
		}
		if opts.JSON {
			return writeJSONObject(stdout, payload)
		}
		fmt.Fprintf(stdout, "listener health: %s\n", payload["status"])
		return nil
	case "state":
		var raw json.RawMessage
		if err := client.GetJSON(ctx, "/_zolem/state", &raw); err != nil {
			return err
		}
		if opts.JSON {
			return writeRawJSON(stdout, raw)
		}
		var payload listenerStateView
		if err := json.Unmarshal(raw, &payload); err != nil {
			return fmt.Errorf("decode state response: %w", err)
		}
		fmt.Fprintf(stdout, "provider=%s profile=%s backend=%s listener=%s tls=%v\n", payload.Provider, payload.Profile, payload.Backend, payload.Listener, payload.TLS)
		return nil
	default:
		// Unreachable: sub is validated to health or state above. Kept so the
		// switch is total for the compiler.
		return fmt.Errorf("unknown listener command %q", sub)
	}
}

func runProviderRequest(ctx context.Context, opts admincli.Options, args []string, stdout, stderr io.Writer) error {
	if opts.BaseURL == "" {
		return errors.New("request requires -base-url")
	}

	fs := flag.NewFlagSet("zolemc request", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var method string
	var requestPath string
	var headers repeatedStrings
	var jsonBody string
	var bodyFile string
	fs.StringVar(&method, "method", http.MethodGet, "HTTP method")
	fs.StringVar(&requestPath, "path", "", "provider request path")
	fs.Var(&headers, "H", "HTTP header in 'Name: value' form")
	fs.StringVar(&jsonBody, "json-body", "", "JSON request body")
	fs.StringVar(&bodyFile, "body-file", "", "file containing request body")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected request arguments: %s", strings.Join(fs.Args(), " "))
	}
	if requestPath == "" {
		return errors.New("request requires -path")
	}
	body, err := requestBody(jsonBody, bodyFile)
	if err != nil {
		return err
	}

	target, err := admincli.JoinBaseAndPath(opts.BaseURL, requestPath)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for _, header := range headers {
		name, value, ok := strings.Cut(header, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return fmt.Errorf("invalid header %q: expected 'Name: value'", header)
		}
		req.Header.Add(strings.TrimSpace(name), strings.TrimSpace(value))
	}
	if jsonBody != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := (&http.Client{Timeout: opts.Timeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &admincli.APIError{Method: method, URL: target, Status: resp.Status, Body: string(respBody)}
	}
	if opts.JSON {
		return writeJSONObject(stdout, map[string]any{
			"status": resp.StatusCode,
			"body":   json.RawMessage(respBody),
		})
	}
	_, err = stdout.Write(respBody)
	if len(respBody) == 0 || respBody[len(respBody)-1] != '\n' {
		fmt.Fprintln(stdout)
	}
	return err
}

func requestBody(jsonBody, bodyFile string) ([]byte, error) {
	if jsonBody != "" && bodyFile != "" {
		return nil, errors.New("use only one of -json-body or -body-file")
	}
	if jsonBody != "" {
		return []byte(jsonBody), nil
	}
	if bodyFile != "" {
		return os.ReadFile(bodyFile)
	}
	return nil, nil
}

// errUnexpectedArgs reports trailing args left after a command that takes none.
// The hint steers users away from the most common trap: a global flag such as
// -json placed after the command, where the flag package never parses it.
func errUnexpectedArgs(command string, extra []string) error {
	return fmt.Errorf("unexpected arguments after %s: %s (global flags such as -json go before the command)", command, strings.Join(extra, " "))
}

func splitOptionalLeadingName(args []string) (string, []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", args
	}
	return args[0], args[1:]
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	seen := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			seen = true
		}
	})
	return seen
}

func writeJSONObject(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// filterCallsRawSince keeps only calls whose call_id exceeds since while
// preserving each retained call's complete raw JSON and any sibling fields on
// the envelope. It reports an error if the response is not the expected
// {"calls":[...]} shape.
func filterCallsRawSince(raw json.RawMessage, since int64) (json.RawMessage, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode calls response: %w", err)
	}
	// A JSON null body unmarshals to a nil map without error; initialize it so
	// the assignment below does not panic.
	if envelope == nil {
		envelope = map[string]json.RawMessage{}
	}
	var calls []json.RawMessage
	if rawCalls, ok := envelope["calls"]; ok && len(rawCalls) > 0 {
		if err := json.Unmarshal(rawCalls, &calls); err != nil {
			return nil, fmt.Errorf("decode calls list: %w", err)
		}
	}
	kept := make([]json.RawMessage, 0, len(calls))
	for _, c := range calls {
		var id struct {
			CallID int64 `json:"call_id"`
		}
		if err := json.Unmarshal(c, &id); err != nil {
			return nil, fmt.Errorf("decode call id: %w", err)
		}
		if id.CallID > since {
			kept = append(kept, c)
		}
	}
	keptBytes, err := json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	envelope["calls"] = keptBytes
	out, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// writeRawJSON writes API response bytes to w unchanged, normalized to exactly
// one trailing newline. Passing the raw bytes through preserves every field the
// API returned; the narrow view structs are reserved for human-readable output
// and would otherwise silently drop fields they do not model.
func writeRawJSON(w io.Writer, raw json.RawMessage) error {
	if _, err := w.Write(bytes.TrimRight(raw, "\n")); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

type repeatedStrings []string

func (v *repeatedStrings) String() string {
	return strings.Join(*v, ", ")
}

func (v *repeatedStrings) Set(s string) error {
	*v = append(*v, s)
	return nil
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: zolemc [global flags] <command> [args]

zolemc talks to two planes, each addressed by its own flag:
  -admin-url  the admin control plane (zolem -local-admin-addr): manage profiles
              and listeners. This is the default plane for most commands.
  -base-url   a single listener's data plane (its base_url from listeners create
              or list): inspect that listener and send provider requests through it.
Put global flags BEFORE the command; flags after a command are rejected.

Global flags:
  -admin-url URL    admin control-plane base URL (default http://127.0.0.1:8090)
  -base-url URL     listener data-plane base URL (required only by listener/request commands)
  -json             write machine-readable JSON
  -timeout DUR      HTTP request timeout (default 10s)

Admin control-plane commands (use -admin-url):
  health
  profiles list
  profiles create <name> [-backend lorem|faker|fixture|ollama|wasm|error] [...]
    [-wasm-module-file PATH] [-wasm-timeout-ms N]
  profiles delete <name>
  listeners list
  listeners create <name> -provider openai|anthropic|gemini -profile <name> [-addr 127.0.0.1:0] [-tls]
  listeners delete <name>
  listeners calls list <name> [-since <id>]
  listeners calls clear <name>

Listener data-plane commands (use -base-url):
  listener health
  listener state
  request -method POST -path /v1/chat/completions [-H 'Name: value'] [-json-body JSON|-body-file PATH]`)
}
