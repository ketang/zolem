package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	localAdminAddr := flag.String("local-admin-addr", "", "listen address for local admin control plane")
	localAddr := flag.String("local-addr", "", "loopback listen address for local fixed-listener mode (e.g. 127.0.0.1:8080); non-loopback addresses are rejected")
	localProvider := flag.String("local-provider", "", "provider for local fixed-listener mode")
	localProfile := flag.String("local-profile", "default", "profile name for local fixed-listener mode")
	localBackend := flag.String("local-backend", "lorem", "backend for local fixed-listener mode")
	localErrorType := flag.String("local-error-type", "", "error type for the error backend in fixed-listener mode: authentication, permission, invalid_request, rate_limit, or server_error; required when -local-backend is error")
	localFixturesDir := flag.String("local-fixtures-dir", "", "fixtures directory for local runtime fixture backend; HTTP fixtures use response.json/response.json.tmpl, OpenAI Responses WebSocket fixtures use an array of event objects")
	localTLSCert := flag.String("local-tls-cert", "", "certificate file for local admin or fixed-listener TLS")
	localTLSKey := flag.String("local-tls-key", "", "key file for local admin or fixed-listener TLS")
	localCallsFile := flag.String("local-calls-file", "", "append JSONL records of captured HTTP calls and OpenAI Responses WebSocket connections to this file; empty disables recording")
	localRecordRequestBodyCap := flag.Int("local-record-request-body-cap-bytes", 262144, "maximum bytes of request body to record per call; excess is counted but dropped")
	localRecordResponseBodyCap := flag.Int("local-record-response-body-cap-bytes", 262144, "maximum bytes of response body to record per call; excess is counted but dropped")
	localRecordStreamEventCap := flag.Int("local-record-stream-event-cap", 1024, "maximum SSE events to record per streamed response; excess is counted but dropped")
	flag.Parse()

	deps := signalAwareStartupDeps(ctx)
	if *localAdminAddr != "" {
		if err := runLocalAdmin(localAdminOptions{
			Addr:        *localAdminAddr,
			FixturesDir: *localFixturesDir,
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
		if err := runLocal(localOptions{
			Addr:        *localAddr,
			Provider:    *localProvider,
			Profile:     *localProfile,
			Backend:     *localBackend,
			ErrorType:   *localErrorType,
			FixturesDir: *localFixturesDir,
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

	log.Fatal("choose either -local-admin-addr or -local-provider")
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
