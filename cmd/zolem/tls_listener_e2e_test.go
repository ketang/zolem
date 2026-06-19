package main_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLocalRuntimeTLSListener_E2E spins the zolem binary with TLS configured at
// the admin/listener boundary, then exercises a TLS-terminating listener over a
// real HTTPS handshake. Positive cases assert openai schema + SSE framing;
// negative cases assert the handshake fails with no 200 leak.
func TestLocalRuntimeTLSListener_E2E(t *testing.T) {
	repoRoot := repoRoot(t)

	certs := generateTestTLSCerts(t)
	admin := startLocalAdminServiceTLS(t, repoRoot, certs)
	t.Cleanup(admin.Close)

	listenerBaseURL := createTLSRuntimeListener(t, admin, "openai", map[string]any{
		"backend": "lorem",
	})

	if !strings.HasPrefix(listenerBaseURL, "https://") {
		t.Fatalf("listener base_url should be https, got %q", listenerBaseURL)
	}

	t.Run("non-streaming", func(t *testing.T) {
		client := httpsClientWithRoots(certs.caPool)
		resp, body := mustHTTPSRequest(t, client, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
			`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json",
			"Authorization: Bearer sk-test",
		)
		defer resp.Body.Close()

		if resp.TLS == nil {
			t.Fatalf("expected TLS connection state on response")
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
		}
		var payload struct {
			Object  string `json:"object"`
			Choices []struct {
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		mustJSONUnmarshal(t, body, &payload)
		if payload.Object != "chat.completion" {
			t.Fatalf("object: got %q, want chat.completion", payload.Object)
		}
		if len(payload.Choices) == 0 {
			t.Fatalf("expected at least one choice; body=%s", body)
		}
		if payload.Choices[0].Message.Content == "" {
			t.Fatalf("expected non-empty message content; body=%s", body)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		client := httpsClientWithRoots(certs.caPool)
		resp, body := mustHTTPSRequest(t, client, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
			`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json",
			"Authorization: Bearer sk-test",
		)
		defer resp.Body.Close()

		if resp.TLS == nil {
			t.Fatalf("expected TLS connection state on streaming response")
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream status: got %d, want 200: %s", resp.StatusCode, body)
		}
		assertSSEHeaders(t, resp.Header)

		// Lorem backend uses Generate(30); the chunker frames that as
		// 1 role-opener + 30 token deltas + 1 finalChunk + 1 usageChunk + [DONE].
		assertOpenAIStreamShape(t, body, 30)
	})

	t.Run("wrong-ca", func(t *testing.T) {
		// Empty root pool: server cert is signed by an unknown CA.
		client := &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs: x509.NewCertPool(),
				},
			},
		}
		resp, err := tryHTTPSGet(client, listenerBaseURL+"/v1/chat/completions")
		if err == nil {
			if resp != nil {
				resp.Body.Close()
				t.Fatalf("wrong-ca: handshake unexpectedly succeeded (status=%d)", resp.StatusCode)
			}
			t.Fatal("wrong-ca: expected handshake error, got nil")
		}
		if resp != nil {
			resp.Body.Close()
			t.Fatalf("wrong-ca: response should not be returned alongside an error; got status=%d", resp.StatusCode)
		}

		var (
			urlErr     *url.Error
			verifyErr  *tls.CertificateVerificationError
			unknownErr x509.UnknownAuthorityError
		)
		if !errors.As(err, &urlErr) {
			t.Fatalf("wrong-ca: expected *url.Error, got %T: %v", err, err)
		}
		// Prefer typed checks; fall back to substring match if the runtime
		// only surfaces an opaque error.
		if !errors.As(err, &verifyErr) && !errors.As(err, &unknownErr) {
			msg := err.Error()
			if !strings.Contains(msg, "x509") && !strings.Contains(msg, "unknown authority") && !strings.Contains(msg, "certificate signed by unknown authority") {
				t.Fatalf("wrong-ca: expected x509/unknown-authority error, got: %v", err)
			}
		}
	})

	t.Run("wrong-sni", func(t *testing.T) {
		// Trust the CA but lie about the server name we expect.
		client := &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs:    certs.caPool,
					ServerName: "wrong.example",
				},
			},
		}
		resp, err := tryHTTPSGet(client, listenerBaseURL+"/v1/chat/completions")
		if err == nil {
			if resp != nil {
				resp.Body.Close()
				t.Fatalf("wrong-sni: handshake unexpectedly succeeded (status=%d)", resp.StatusCode)
			}
			t.Fatal("wrong-sni: expected handshake error, got nil")
		}
		if resp != nil {
			resp.Body.Close()
			t.Fatalf("wrong-sni: response should not be returned alongside an error; got status=%d", resp.StatusCode)
		}

		var (
			urlErr      *url.Error
			hostnameErr x509.HostnameError
			verifyErr   *tls.CertificateVerificationError
		)
		if !errors.As(err, &urlErr) {
			t.Fatalf("wrong-sni: expected *url.Error, got %T: %v", err, err)
		}
		if !errors.As(err, &hostnameErr) && !errors.As(err, &verifyErr) {
			msg := err.Error()
			if !strings.Contains(msg, "wrong.example") && !strings.Contains(msg, "valid for") && !strings.Contains(msg, "not wrong.example") {
				t.Fatalf("wrong-sni: expected hostname-mismatch error, got: %v", err)
			}
		}
	})
}

// tryHTTPSGet performs a GET to the given URL and returns the raw response and
// error from the transport, without t.Fatal-ing. Used by negative cases.
func tryHTTPSGet(client *http.Client, fullURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

// mustHTTPSRequest issues a request through the provided HTTPS client and
// returns the response (with body preloaded). It mirrors doRequest but accepts
// a caller-supplied client so the TLS config is explicit.
func mustHTTPSRequest(t *testing.T, client *http.Client, baseURL, method, path, body string, headers ...string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, baseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for _, h := range headers {
		parts := strings.SplitN(h, ": ", 2)
		if len(parts) != 2 {
			t.Fatalf("bad header %q", h)
		}
		req.Header.Set(parts[0], parts[1])
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	bodyBytes, err := readAllAndClose(resp)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, bodyBytes
}

func readAllAndClose(resp *http.Response) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(resp.Body)
	resp.Body.Close()
	// Replace the body so callers can still call Body.Close() as they do for
	// http-only siblings — return a no-op closer.
	resp.Body = nopReadCloser{Reader: bytes.NewReader(buf.Bytes())}
	return buf.Bytes(), err
}

type nopReadCloser struct {
	*bytes.Reader
}

func (nopReadCloser) Close() error { return nil }

// httpsClientWithRoots returns an http.Client that trusts only the given CA
// pool and verifies the server's hostname normally.
func httpsClientWithRoots(roots *x509.CertPool) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: roots,
			},
		},
	}
}

// createTLSRuntimeListener creates a profile + TLS listener via the admin API
// and returns the listener's HTTPS base URL.
func createTLSRuntimeListener(t *testing.T, admin *tlsAdminService, provider string, profile map[string]any) string {
	t.Helper()

	backend, _ := profile["backend"].(string)
	if backend == "" {
		backend = "lorem"
	}
	profileName := provider + "-" + backend + "-tls"
	listenerName := provider + "-" + backend + "-tls-listener"

	profileBody, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}

	pResp, pBody := mustHTTPSRequest(t, admin.client, admin.baseURL, http.MethodPut, "/_zolem/profiles/"+profileName, string(profileBody), "Content-Type: application/json")
	defer pResp.Body.Close()
	if pResp.StatusCode != http.StatusOK {
		t.Fatalf("profile upsert status: got %d, want 200: %s", pResp.StatusCode, pBody)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodDelete, admin.baseURL+"/_zolem/profiles/"+profileName, nil)
		if resp, err := admin.client.Do(req); err == nil && resp != nil {
			resp.Body.Close()
		}
	})

	listenerPayload := fmt.Sprintf(`{"addr":"127.0.0.1:0","provider":"%s","profile":"%s","tls":true}`, provider, profileName)
	lResp, lBody := mustHTTPSRequest(t, admin.client, admin.baseURL, http.MethodPut, "/_zolem/listeners/"+listenerName, listenerPayload, "Content-Type: application/json")
	defer lResp.Body.Close()
	if lResp.StatusCode != http.StatusOK {
		t.Fatalf("listener upsert status: got %d, want 200: %s", lResp.StatusCode, lBody)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodDelete, admin.baseURL+"/_zolem/listeners/"+listenerName, nil)
		if resp, err := admin.client.Do(req); err == nil && resp != nil {
			resp.Body.Close()
		}
	})

	var view struct {
		BaseURL string `json:"base_url"`
		TLS     bool   `json:"tls"`
	}
	mustJSONUnmarshal(t, lBody, &view)
	if !view.TLS {
		t.Fatalf("listener view tls=false; expected tls=true: %s", lBody)
	}
	if view.BaseURL == "" {
		t.Fatalf("missing base_url: %s", lBody)
	}
	return view.BaseURL
}

// tlsAdminService is the TLS-equivalent of localAdminService (which is
// HTTP-only). It reuses the same go-run launch pattern but adds
// -local-tls-cert/-local-tls-key flags and a CA-trusting HTTPS client for
// readiness and admin calls.
type tlsAdminService struct {
	baseURL string
	client  *http.Client
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	done    chan struct{}
	errCh   chan error
	logs    *bytes.Buffer
}

func startLocalAdminServiceTLS(t *testing.T, repoRoot string, certs testCerts) *tlsAdminService {
	t.Helper()

	bin := buildZolemBinary(t)
	port := pickPort(t)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", port)

	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin,
		"-local-admin-addr", adminAddr,
		"-local-tls-cert", certs.certPath,
		"-local-tls-key", certs.keyPath,
	)
	cmd.Env = os.Environ()
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	configureProcReaping(cmd)

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start tls admin: %v", err)
	}

	svc := &tlsAdminService{
		baseURL: "https://" + adminAddr,
		client:  httpsClientWithRoots(certs.caPool),
		cmd:     cmd,
		cancel:  cancel,
		done:    make(chan struct{}),
		errCh:   make(chan error, 1),
		logs:    &logs,
	}
	go func() {
		svc.errCh <- cmd.Wait()
		close(svc.done)
	}()

	deadline := time.Now().Add(90 * time.Second)
	probe := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: certs.caPool},
		},
	}
	for time.Now().Before(deadline) {
		select {
		case <-svc.done:
			err := <-svc.errCh
			t.Fatalf("tls admin exited before readiness: %v\nlogs:\n%s", err, logs.String())
		default:
		}
		req, _ := http.NewRequest(http.MethodGet, svc.baseURL+"/_zolem/health", nil)
		resp, err := probe.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return svc
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for tls admin readiness\nlogs:\n%s", logs.String())
	return nil
}

func (s *tlsAdminService) Close() {
	s.cancel()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
}

// testCerts is the bundle of files+pool the TLS test uses.
type testCerts struct {
	certPath string
	keyPath  string
	caPool   *x509.CertPool
}

// generateTestTLSCerts creates an in-memory CA and leaf cert in t.TempDir(),
// writes the leaf cert + key to disk for the binary's -local-tls-* flags, and
// returns a CA pool the test client can trust.
func generateTestTLSCerts(t *testing.T) testCerts {
	t.Helper()
	dir := t.TempDir()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "zolem-test-ca"},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              time.Now().Add(1 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("sign CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-1 * time.Minute),
		NotAfter:     time.Now().Add(1 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("sign leaf cert: %v", err)
	}

	certPath := filepath.Join(dir, "server.pem")
	keyPath := filepath.Join(dir, "server-key.pem")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	return testCerts{certPath: certPath, keyPath: keyPath, caPool: pool}
}
