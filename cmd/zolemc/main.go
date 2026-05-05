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
)

type rootOptions struct {
	adminURL string
	baseURL  string
	json     bool
	timeout  time.Duration
}

type localProfilePayload struct {
	Backend               string `json:"backend,omitempty"`
	BackendModel          string `json:"backend_model,omitempty"`
	ErrorType             string `json:"error_type,omitempty"`
	ResponseModelPolicy   string `json:"response_model_policy,omitempty"`
	ResponseModel         string `json:"response_model,omitempty"`
	FixtureNamespace      string `json:"fixture_namespace,omitempty"`
	Seed                  *int64 `json:"seed,omitempty"`
	WASMModuleBase64      string `json:"wasm_module_base64,omitempty"`
	WASMGenerateTimeoutMS *int   `json:"wasm_generate_timeout_ms,omitempty"`
}

type localListenerPayload struct {
	Addr     string `json:"addr"`
	Provider string `json:"provider"`
	Profile  string `json:"profile"`
	TLS      bool   `json:"tls,omitempty"`
}

type localProfileView struct {
	Name                string `json:"name"`
	Backend             string `json:"backend"`
	BackendModel        string `json:"backend_model,omitempty"`
	ErrorType           string `json:"error_type,omitempty"`
	ResponseModelPolicy string `json:"response_model_policy,omitempty"`
	ResponseModel       string `json:"response_model,omitempty"`
	FixtureNamespace    string `json:"fixture_namespace,omitempty"`
	Seed                *int64 `json:"seed,omitempty"`
}

type localListenerView struct {
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	Provider string `json:"provider"`
	Profile  string `json:"profile"`
	Backend  string `json:"backend"`
	TLS      bool   `json:"tls,omitempty"`
	BaseURL  string `json:"base_url"`
}

type listenerStateView struct {
	Provider string `json:"provider"`
	Profile  string `json:"profile"`
	Backend  string `json:"backend"`
	Listener string `json:"listener"`
	TLS      bool   `json:"tls"`
}

type adminClient struct {
	baseURL string
	http    *http.Client
}

type apiError struct {
	Method string
	URL    string
	Status string
	Body   string
}

func (e *apiError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("%s %s: %s", e.Method, e.URL, e.Status)
	}
	return fmt.Sprintf("%s %s: %s: %s", e.Method, e.URL, e.Status, body)
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	opts := rootOptions{}
	fs := flag.NewFlagSet("zolemc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.adminURL, "admin-url", "http://127.0.0.1:8090", "local admin API base URL")
	fs.StringVar(&opts.baseURL, "base-url", "", "local listener base URL")
	fs.BoolVar(&opts.json, "json", false, "write machine-readable JSON")
	fs.DurationVar(&opts.timeout, "timeout", 10*time.Second, "HTTP request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		usage(stderr)
		return errors.New("missing command")
	}

	client := adminClient{
		baseURL: strings.TrimRight(opts.adminURL, "/"),
		http:    &http.Client{Timeout: opts.timeout},
	}

	switch fs.Arg(0) {
	case "health":
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

func runAdminHealth(ctx context.Context, client adminClient, opts rootOptions, stdout io.Writer) error {
	var payload map[string]string
	if err := client.getJSON(ctx, "/_zolem/health", &payload); err != nil {
		return err
	}
	if opts.json {
		return writeJSONObject(stdout, payload)
	}
	fmt.Fprintf(stdout, "admin health: %s\n", payload["status"])
	return nil
}

func runProfiles(ctx context.Context, client adminClient, opts rootOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("profiles requires list, create, or delete")
	}
	switch args[0] {
	case "list":
		var profiles []localProfileView
		if err := client.getJSON(ctx, "/_zolem/profiles", &profiles); err != nil {
			return err
		}
		if opts.json {
			return writeJSONObject(stdout, profiles)
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
		var profile localProfileView
		if err := client.putJSON(ctx, "/_zolem/profiles/"+url.PathEscape(name), payload, &profile); err != nil {
			return err
		}
		if opts.json {
			return writeJSONObject(stdout, profile)
		}
		fmt.Fprintf(stdout, "profile %s created\n", profile.Name)
		return nil
	case "delete":
		if len(args) != 2 {
			return errors.New("profiles delete requires exactly one profile name")
		}
		if err := client.delete(ctx, "/_zolem/profiles/"+url.PathEscape(args[1])); err != nil {
			return err
		}
		if opts.json {
			return writeJSONObject(stdout, map[string]string{"deleted": args[1]})
		}
		fmt.Fprintf(stdout, "profile %s deleted\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown profiles command %q", args[0])
	}
}

func runListeners(ctx context.Context, client adminClient, opts rootOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("listeners requires list, create, or delete")
	}
	switch args[0] {
	case "list":
		var listeners []localListenerView
		if err := client.getJSON(ctx, "/_zolem/listeners", &listeners); err != nil {
			return err
		}
		if opts.json {
			return writeJSONObject(stdout, listeners)
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
		var listener localListenerView
		if err := client.putJSON(ctx, "/_zolem/listeners/"+url.PathEscape(name), payload, &listener); err != nil {
			return err
		}
		if opts.json {
			return writeJSONObject(stdout, listener)
		}
		fmt.Fprintf(stdout, "listener %s created: %s\n", listener.Name, listener.BaseURL)
		return nil
	case "delete":
		if len(args) != 2 {
			return errors.New("listeners delete requires exactly one listener name")
		}
		if err := client.delete(ctx, "/_zolem/listeners/"+url.PathEscape(args[1])); err != nil {
			return err
		}
		if opts.json {
			return writeJSONObject(stdout, map[string]string{"deleted": args[1]})
		}
		fmt.Fprintf(stdout, "listener %s deleted\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown listeners command %q", args[0])
	}
}

func runListener(ctx context.Context, opts rootOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return errors.New("listener requires health or state")
	}
	if opts.baseURL == "" {
		return errors.New("listener commands require -base-url")
	}
	client := adminClient{baseURL: strings.TrimRight(opts.baseURL, "/"), http: &http.Client{Timeout: opts.timeout}}
	switch args[0] {
	case "health":
		var payload map[string]string
		if err := client.getJSON(ctx, "/_zolem/health", &payload); err != nil {
			return err
		}
		if opts.json {
			return writeJSONObject(stdout, payload)
		}
		fmt.Fprintf(stdout, "listener health: %s\n", payload["status"])
		return nil
	case "state":
		var payload listenerStateView
		if err := client.getJSON(ctx, "/_zolem/state", &payload); err != nil {
			return err
		}
		if opts.json {
			return writeJSONObject(stdout, payload)
		}
		fmt.Fprintf(stdout, "provider=%s profile=%s backend=%s listener=%s tls=%v\n", payload.Provider, payload.Profile, payload.Backend, payload.Listener, payload.TLS)
		return nil
	default:
		return fmt.Errorf("unknown listener command %q", args[0])
	}
}

func runProviderRequest(ctx context.Context, opts rootOptions, args []string, stdout, stderr io.Writer) error {
	if opts.baseURL == "" {
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

	target, err := joinBaseAndPath(opts.baseURL, requestPath)
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

	resp, err := (&http.Client{Timeout: opts.timeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{Method: method, URL: target, Status: resp.Status, Body: string(respBody)}
	}
	if opts.json {
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

func (c adminClient) getJSON(ctx context.Context, p string, out any) error {
	return c.doJSON(ctx, http.MethodGet, p, nil, out)
}

func (c adminClient) putJSON(ctx context.Context, p string, in, out any) error {
	return c.doJSON(ctx, http.MethodPut, p, in, out)
}

func (c adminClient) delete(ctx context.Context, p string) error {
	return c.doJSON(ctx, http.MethodDelete, p, nil, nil)
}

func (c adminClient) doJSON(ctx context.Context, method, p string, in, out any) error {
	target, err := joinBaseAndPath(c.baseURL, p)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{Method: method, URL: target, Status: resp.Status, Body: string(data)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s %s response: %w: %s", method, target, err, strings.TrimSpace(string(data)))
	}
	return nil
}

func joinBaseAndPath(base, p string) (string, error) {
	if base == "" {
		return "", errors.New("base URL is required")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid base URL %q", base)
	}
	rel, err := url.Parse(p)
	if err != nil {
		return "", err
	}
	if rel.IsAbs() || rel.Host != "" {
		return "", fmt.Errorf("request path must be relative to base URL: %q", p)
	}
	relPath := rel.Path
	if !strings.HasPrefix(relPath, "/") {
		relPath = "/" + relPath
	}
	u.Path = strings.TrimRight(u.Path, "/") + relPath
	u.RawQuery = rel.RawQuery
	return u.String(), nil
}

func writeJSONObject(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
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

Global flags:
  -admin-url URL    local admin API base URL (default http://127.0.0.1:8090)
  -base-url URL     local listener base URL for listener/request commands
  -json             write machine-readable JSON

Commands:
  health
  profiles list
  profiles create <name> [-backend lorem|faker|fixture|ollama|wasm|error] [...]
    [-wasm-module-file PATH] [-wasm-timeout-ms N]
  profiles delete <name>
  listeners list
  listeners create <name> -provider openai|anthropic|gemini -profile <name> [-addr 127.0.0.1:0] [-tls]
  listeners delete <name>
  listener health
  listener state
  request -method POST -path /v1/chat/completions [-H 'Name: value'] [-json-body JSON|-body-file PATH]`)
}
