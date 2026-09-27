---
schema_version: 1
title: Send a provider request via zolemc
slug: zolemc-send-provider-request
status: draft
authority: observed
change_resistance: low
locked_sections: []
---

# Send a provider request via zolemc

## Intent
A developer probes a running mock listener with provider-compatible requests to verify its behavior without writing a separate test script.

## Story
A developer wants to quickly probe a running mock listener with a provider-compatible request without writing a separate test script. They run `zolemc -base-url <listener_url> request -method POST -path /v1/chat/completions -json-body '{...}'`. zolemc constructs the request, sends it, and prints the response body.

## Expected Behavior
On success, zolemc prints the response body and exits 0. With -json it wraps the response in {"status": N, "body": ...}. If -base-url is omitted, zolemc exits with an error. If -path is omitted, zolemc exits with an error. If both -json-body and -body-file are supplied, zolemc exits with an error before sending. If the response status is 4xx or 5xx, zolemc returns an apiError with method, URL, status, and body. -H may be supplied multiple times to add headers; each must be in 'Name: value' form. -json-body implicitly sets Content-Type: application/json when no Content-Type header is provided.

With -json, the response body is not assumed to be JSON: a successful (2xx) response body that is not valid JSON (for example an SSE stream, NDJSON, plain text, or an empty body) no longer fails the command. zolemc classifies the body in this order: (1) if the body is not valid UTF-8, it is base64-encoded and the envelope includes "body_encoding":"base64" (this check runs before the JSON-validity check, because a syntactically valid JSON document can still contain invalid UTF-8 bytes inside a quoted string, and re-encoding those bytes as raw JSON would silently corrupt them); (2) otherwise if the body is valid JSON, it is embedded unchanged with no "body_encoding" key, preserving the original {"status": N, "body": ...} shape; (3) otherwise the body is embedded as a JSON string and the envelope includes "body_encoding":"text" (an empty body yields "body":"").

## Boundaries
-path must be a relative path; absolute URLs or paths with scheme/host are rejected. Only one of -json-body or -body-file may be used. -base-url must include a scheme and host. Non-2xx responses are treated as errors.

## Auditable Claims
- zolemc request exits error if -base-url is omitted
- zolemc request exits error if -path is omitted
- zolemc request exits error if both -json-body and -body-file are supplied
- non-2xx response is returned as an apiError
- -json-body sets Content-Type: application/json when no Content-Type header is present
- -H can be supplied multiple times; invalid format (missing colon) is an error
- -json on a successful non-JSON response body (e.g. an SSE stream) exits 0 with "body_encoding":"text" instead of failing with a marshal error
- -json on a successful response body that is not valid UTF-8 exits 0 with the body base64-encoded and "body_encoding":"base64"
- -json on a successful valid-JSON response body keeps the unchanged {"status": N, "body": ...} shape with no "body_encoding" key

## Evidence

### Tests
- `cmd/zolemc/main_e2e_test.go`
- `cmd/zolemc/main_test.go`

### Surface
- `cli: zolemc -base-url http://127.0.0.1:19001 request -method POST -path /v1/chat/completions -json-body '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}'`

### Docs
- `README.md#quick-start-local-runtime-mode`
