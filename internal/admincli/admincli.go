// Package admincli holds the reusable dependency values for the zolemc admin
// CLI. The types live here, rather than in package main, so Shatter and tests
// can construct admin/listener/profile command helpers without synthesizing
// unexported package main structs.
package admincli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// InertURL is a loopback address that performs no real I/O. The scan- and
// test-safe constructors target it so building CLI dependency values never
// reaches the network until command logic explicitly issues a request.
const InertURL = "http://127.0.0.1:0"

// DefaultTimeout matches the zolemc -timeout default.
const DefaultTimeout = 10 * time.Second

// Options holds the root CLI dependency values shared by zolemc commands.
type Options struct {
	AdminURL string
	BaseURL  string
	JSON     bool
	Timeout  time.Duration
}

// NewInertOptions returns Options pointing at InertURL, suitable for scans and
// tests that construct command dependency values without performing I/O.
func NewInertOptions() Options {
	return Options{
		AdminURL: InertURL,
		BaseURL:  InertURL,
		Timeout:  DefaultTimeout,
	}
}

// Client is an admin API client targeting a base URL with a caller-provided
// HTTP client.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds a Client for baseURL backed by httpClient. Any trailing
// slash on baseURL is trimmed. A nil httpClient defaults to
// http.DefaultClient. Construction performs no I/O.
func NewClient(baseURL string, httpClient *http.Client) Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// errInert is returned by the inert transport instead of dialing the network.
var errInert = errors.New("admincli: inert client performs no I/O")

type inertTransport struct{}

func (inertTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errInert
}

// NewInertClient returns a Client backed by a transport that never touches the
// network. Constructing it performs no I/O; any request made through it fails
// fast with an inert-client error instead of dialing.
func NewInertClient() Client {
	return NewClient(InertURL, &http.Client{Transport: inertTransport{}})
}

// BaseURL reports the trimmed base URL the client targets.
func (c Client) BaseURL() string { return c.baseURL }

func (c Client) GetJSON(ctx context.Context, p string, out any) error {
	return c.DoJSON(ctx, http.MethodGet, p, nil, out)
}

func (c Client) PutJSON(ctx context.Context, p string, in, out any) error {
	return c.DoJSON(ctx, http.MethodPut, p, in, out)
}

func (c Client) Delete(ctx context.Context, p string) error {
	return c.DoJSON(ctx, http.MethodDelete, p, nil, nil)
}

func (c Client) DoJSON(ctx context.Context, method, p string, in, out any) error {
	target, err := JoinBaseAndPath(c.baseURL, p)
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
		return &APIError{Method: method, URL: target, Status: resp.Status, Body: string(data)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s %s response: %w: %s", method, target, err, strings.TrimSpace(string(data)))
	}
	return nil
}

// APIError reports a non-2xx response from an admin or provider request.
type APIError struct {
	Method string
	URL    string
	Status string
	Body   string
}

func (e *APIError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("%s %s: %s", e.Method, e.URL, e.Status)
	}
	return fmt.Sprintf("%s %s: %s: %s", e.Method, e.URL, e.Status, body)
}

// JoinBaseAndPath joins a relative path onto a base URL, rejecting absolute or
// host-bearing paths.
func JoinBaseAndPath(base, p string) (string, error) {
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
