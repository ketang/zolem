package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// version is the zolem build version. It can be stamped at build time with
// -ldflags "-X main.version=<v>"; when unset it falls back to the module
// version recorded in the build info, or "dev" for unstamped builds.
var version = ""

func zolemVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	flag.CommandLine.Usage = func() { usage(os.Stderr) }
	showVersion := flag.Bool("version", false, "print version and exit")
	localAdminAddr := flag.String("local-admin-addr", "", "listen address for local admin control plane")
	localAddr := flag.String("local-addr", "", "loopback listen address for local fixed-listener mode (e.g. 127.0.0.1:8080); non-loopback addresses are rejected")
	localProvider := flag.String("local-provider", "", "provider for local fixed-listener mode")
	localProfile := flag.String("local-profile", "default", "profile name for local fixed-listener mode")
	localBackend := flag.String("local-backend", "lorem", "backend for local fixed-listener mode")
	localErrorType := flag.String("local-error-type", "", "error type for the error backend in fixed-listener mode: authentication, permission, invalid_request, rate_limit, or server_error; required when -local-backend is error")
	localBackendModel := flag.String("local-backend-model", "", "model the backend uses; required when -local-backend is ollama-logprob")
	localOllamaUpstream := flag.String("local-ollama-upstream", "", "ollama upstream URL for the ollama and ollama-logprob backends (loopback or RFC1918 only; default http://localhost:11434)")
	localCalibrationTemperature := flag.Float64("local-calibration-temperature", 0, "ollama-logprob only: divide each log probability by this positive number before exponentiating (default 1.0; flatter above 1, sharper below)")
	localFixturesDir := flag.String("local-fixtures-dir", "", "fixtures directory for local runtime fixture backend; HTTP fixtures use response.json/response.json.tmpl, OpenAI Responses WebSocket fixtures use an array of event objects")
	localTLSCert := flag.String("local-tls-cert", "", "certificate file for local admin or fixed-listener TLS")
	localTLSKey := flag.String("local-tls-key", "", "key file for local admin or fixed-listener TLS")
	localCallsFile := flag.String("local-calls-file", "", "append JSONL records of captured HTTP calls and OpenAI Responses WebSocket connections to this file; empty disables recording; credential headers and secret-bearing query values are redacted; file is created 0600 and an existing looser file is tightened to 0600")
	localRecordRequestBodyCap := flag.Int("local-record-request-body-cap-bytes", 262144, "maximum bytes of request body to record per call; excess is counted but dropped")
	localRecordResponseBodyCap := flag.Int("local-record-response-body-cap-bytes", 262144, "maximum bytes of response body to record per call; excess is counted but dropped")
	localRecordStreamEventCap := flag.Int("local-record-stream-event-cap", 1024, "maximum SSE events to record per streamed response; excess is counted but dropped")
	var allowedHosts stringList
	flag.Var(&allowedHosts, "allowed-host", "extra Host header value accepted in addition to localhost and loopback IPs (repeatable; any port is ignored); other Hosts get 403 (DNS-rebinding guard). Applies to both modes")
	flag.Parse()

	if *showVersion {
		fmt.Println(zolemVersion())
		return
	}

	deps := signalAwareStartupDeps(ctx)
	if *localAdminAddr != "" {
		if err := runLocalAdmin(localAdminOptions{
			Addr:         *localAdminAddr,
			FixturesDir:  *localFixturesDir,
			AllowedHosts: allowedHosts,
			TLS: localTLSConfig{
				CertFile: *localTLSCert,
				KeyFile:  *localTLSKey,
			},
		}, deps); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *localProvider != "" {
		var calibrationTemperature *float64
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "local-calibration-temperature" {
				calibrationTemperature = localCalibrationTemperature
			}
		})
		if err := runLocal(localOptions{
			AllowedHosts:           allowedHosts,
			Addr:                   *localAddr,
			Provider:               *localProvider,
			Profile:                *localProfile,
			Backend:                *localBackend,
			ErrorType:              *localErrorType,
			BackendModel:           *localBackendModel,
			OllamaUpstream:         *localOllamaUpstream,
			CalibrationTemperature: calibrationTemperature,
			FixturesDir:            *localFixturesDir,
			TLS: localTLSConfig{
				CertFile: *localTLSCert,
				KeyFile:  *localTLSKey,
			},
			CallsFile:                  *localCallsFile,
			RecordRequestBodyCapBytes:  *localRecordRequestBodyCap,
			RecordResponseBodyCapBytes: *localRecordResponseBodyCap,
			RecordStreamEventCap:       *localRecordStreamEventCap,
		}, deps); err != nil {
			log.Fatal(err)
		}
		return
	}

	fmt.Fprintln(os.Stderr, "zolem: choose a mode: -local-provider (fixed-listener) or -local-admin-addr (admin control plane)")
	fmt.Fprintln(os.Stderr)
	usage(os.Stderr)
	os.Exit(2)
}

// usage prints a synopsis naming both run modes and grouping the flags that
// apply to each. It is wired as flag.CommandLine.Usage and is also printed when
// zolem is run with no mode selected.
func usage(w io.Writer) {
	fmt.Fprint(w, `zolem — local mock server for LLM provider APIs

Usage:
  zolem -local-provider <provider> [fixed-listener flags]   # fixed-listener mode
  zolem -local-admin-addr <addr> [admin flags]              # admin control-plane mode
  zolem -version

zolem runs in exactly one of two modes, selected by which flag you pass:

Fixed-listener mode (-local-provider) serves a single provider on one loopback
listener with a fixed profile and backend:
  -local-provider PROVIDER    anthropic, gemini, ollama, openai, or typesafe (selects this mode)
  -local-addr ADDR            loopback listen address (default 127.0.0.1:8080)
  -local-profile NAME         profile name (default "default")
  -local-backend BACKEND      lorem, faker, fixture, ollama, ollama-logprob, wasm, or error
                              (default "lorem"; typesafe supports lorem, faker, fixture,
                              ollama-logprob, error; ollama-logprob is typesafe-only)
  -local-backend-model MODEL  model the backend uses; required for ollama-logprob
  -local-ollama-upstream URL  ollama upstream for ollama and ollama-logprob (loopback or
                              RFC1918 only; default http://localhost:11434)
  -local-calibration-temperature T
                              ollama-logprob only: positive divisor applied to each log
                              probability (default 1.0; above 1 flattens, below 1 sharpens)
  -local-error-type TYPE      error backend type; required when -local-backend is error
  -local-fixtures-dir DIR     fixtures directory for the fixture backend
  -local-calls-file PATH      append JSONL records of captured calls (credentials redacted; file kept 0600) to this file
  -local-record-request-body-cap-bytes N   max request-body bytes recorded per call
  -local-record-response-body-cap-bytes N  max response-body bytes recorded per call
  -local-record-stream-event-cap N          max SSE events recorded per response

Admin control-plane mode (-local-admin-addr) serves the /_zolem admin API so
profiles and listeners can be created and torn down at runtime with zolemc:
  -local-admin-addr ADDR      loopback listen address for the admin API (selects this mode)
  -local-fixtures-dir DIR     fixtures directory shared by fixture-backend listeners

Flags shared by both modes:
  -local-tls-cert FILE        certificate file for admin or fixed-listener TLS
  -local-tls-key FILE         key file for admin or fixed-listener TLS
  -allowed-host NAME          extra Host header accepted besides localhost and loopback IPs
                              (repeatable; port ignored). Any other Host gets 403 to block
                              DNS-rebinding; use for an /etc/hosts alias or custom-hostname TLS
  -version                    print version and exit
`)
}

func signalAwareStartupDeps(ctx context.Context) startupDeps {
	return startupDeps{
		listen: func(addr string, handler http.Handler) error {
			server := &http.Server{Addr: addr, Handler: handler}
			return serveHTTPWithContext(ctx, server, server.ListenAndServe)
		},
		listenTLS: func(addr, certFile, keyFile string, handler http.Handler) error {
			server := &http.Server{Addr: addr, Handler: handler}
			return serveHTTPWithContext(ctx, server, func() error {
				return server.ListenAndServeTLS(certFile, keyFile)
			})
		},
	}
}

func serveHTTPWithContext(ctx context.Context, server *http.Server, serve func() error) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- serve()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string { return fmt.Sprint([]string(*l)) }

// Set validates and normalizes one -allowed-host value: whitespace is trimmed,
// a port is dropped, empty and URL/path-looking values are rejected, and
// duplicates (case-insensitive) are ignored.
func (l *stringList) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errors.New("value must not be empty")
	}
	if strings.Contains(v, "/") {
		return fmt.Errorf("%q must be a bare hostname (optionally with a port), not a URL or path", v)
	}
	hosts := runtimecfg.NormalizeAllowedHosts([]string{v})
	if len(hosts) == 0 {
		return fmt.Errorf("%q is not a valid host", v)
	}
	for _, existing := range *l {
		if strings.EqualFold(existing, hosts[0]) {
			return nil
		}
	}
	*l = append(*l, strings.ToLower(hosts[0]))
	return nil
}
