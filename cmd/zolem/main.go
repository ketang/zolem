package main

import (
	"flag"
	"log"
)

func main() {
	localAdminAddr := flag.String("local-admin-addr", "", "listen address for local admin control plane")
	localAddr := flag.String("local-addr", "", "loopback listen address for local fixed-listener mode (e.g. 127.0.0.1:8080); non-loopback addresses are rejected")
	localProvider := flag.String("local-provider", "", "provider for local fixed-listener mode")
	localProfile := flag.String("local-profile", "default", "profile name for local fixed-listener mode")
	localBackend := flag.String("local-backend", "lorem", "backend for local fixed-listener mode")
	localFixturesDir := flag.String("local-fixtures-dir", "", "fixtures directory for local runtime fixture backend; HTTP fixtures use response.json/response.json.tmpl, OpenAI Responses WebSocket fixtures use an array of event objects")
	localTLSCert := flag.String("local-tls-cert", "", "certificate file for local admin or fixed-listener TLS")
	localTLSKey := flag.String("local-tls-key", "", "key file for local admin or fixed-listener TLS")
	localCallsFile := flag.String("local-calls-file", "", "append JSONL records of captured HTTP calls and OpenAI Responses WebSocket connections to this file; empty disables recording")
	localRecordRequestBodyCap := flag.Int("local-record-request-body-cap-bytes", 262144, "maximum bytes of request body to record per call; excess is counted but dropped")
	localRecordResponseBodyCap := flag.Int("local-record-response-body-cap-bytes", 262144, "maximum bytes of response body to record per call; excess is counted but dropped")
	localRecordStreamEventCap := flag.Int("local-record-stream-event-cap", 1024, "maximum SSE events to record per streamed response; excess is counted but dropped")
	flag.Parse()

	if *localAdminAddr != "" {
		if err := runLocalAdmin(localAdminOptions{
			Addr:        *localAdminAddr,
			FixturesDir: *localFixturesDir,
			TLS: localTLSConfig{
				CertFile: *localTLSCert,
				KeyFile:  *localTLSKey,
			},
		}, startupDeps{}); err != nil {
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
			FixturesDir: *localFixturesDir,
			TLS: localTLSConfig{
				CertFile: *localTLSCert,
				KeyFile:  *localTLSKey,
			},
			CallsFile:                  *localCallsFile,
			RecordRequestBodyCapBytes:  *localRecordRequestBodyCap,
			RecordResponseBodyCapBytes: *localRecordResponseBodyCap,
			RecordStreamEventCap:       *localRecordStreamEventCap,
		}, startupDeps{}); err != nil {
			log.Fatal(err)
		}
		return
	}

	log.Fatal("choose either -local-admin-addr or -local-provider")
}
