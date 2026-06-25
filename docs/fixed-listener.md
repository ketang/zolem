# Fixed-Listener Mode

Fixed-listener mode starts a single loopback listener pinned to one provider
and profile at startup. There is no admin server and no runtime API; the
listener is configured entirely through flags. Good for simple scripted tests
where the backend does not need to change between runs.

## Basic Usage

```bash
zolem \
  -local-addr 127.0.0.1:18080 \
  -local-provider anthropic \
  -local-profile demo \
  -local-backend lorem
```

Key flags:

| Flag | Purpose |
| --- | --- |
| `-local-addr <addr>` | Loopback address and port for the listener |
| `-local-provider <provider>` | Provider: `openai`, `anthropic`, or `gemini` |
| `-local-profile <name>` | Profile name (used in introspection output) |
| `-local-backend <backend>` | Backend: `lorem`, `faker`, `fixture`, `ollama`, `wasm`, or `error` |

For fixture-backed listeners, also pass `-local-fixtures-dir <path>`. See
[fixture-authoring.md](fixture-authoring.md) for how to write fixtures.

## Error Backend

Supply `-local-backend error` together with `-local-error-type` to return a
provider-native error for every request:

```bash
zolem \
  -local-addr 127.0.0.1:18080 \
  -local-provider anthropic \
  -local-backend error \
  -local-error-type rate_limit
```

Supported `error_type` values: `authentication`, `permission`, `invalid_request`,
`rate_limit`, `server_error`.

`-local-backend error` without `-local-error-type` is rejected at startup with
a clear message.

## Fixture Backend

Pass `-local-backend fixture` and `-local-fixtures-dir` to serve static or
templated responses from a fixture directory:

```bash
zolem \
  -local-addr 127.0.0.1:18080 \
  -local-provider openai \
  -local-backend fixture \
  -local-fixtures-dir ./testdata/fixtures
```

See [fixture-authoring.md](fixture-authoring.md) for namespace layout,
`fixtures.yaml` selectors, WebSocket fixtures, and templated responses.

## Ollama Backend

Fixed-listener mode supports the `ollama` backend:

```bash
go run ./cmd/zolem \
  -local-addr 127.0.0.1:18080 \
  -local-provider anthropic \
  -local-profile demo \
  -local-backend ollama
```

## Call Recording

In fixed-listener mode the runtime can append every captured request/response
pair to a JSONL file. One JSON object per line; the file is opened with
`O_APPEND|O_CREATE`, fsynced after each record, and re-opening an existing
file appends rather than truncates.

| Flag | Default | Purpose |
| --- | --- | --- |
| `-local-calls-file <path>` | `""` (disabled) | Path to the JSONL file. Empty disables recording. |
| `-local-record-request-body-cap-bytes <n>` | `262144` | Maximum bytes of request body recorded per call. Excess is counted in `body_truncated_bytes`. |
| `-local-record-response-body-cap-bytes <n>` | `262144` | Maximum bytes of response body recorded per call. Same truncation semantics. |
| `-local-record-stream-event-cap <n>` | `1024` | Maximum SSE events recorded per streamed response. Excess is counted in `events_truncated`. |

Example:

```bash
go run ./cmd/zolem \
  -local-provider anthropic \
  -local-addr 127.0.0.1:8080 \
  -local-calls-file ./zolem-calls.jsonl
```

HTTP lines are `RecordedCall` objects (see `cmd/zolem/recording.go`) with
monotonic `call_id`, listener identity, timing, request, and response. OpenAI
Responses WebSocket connections are recorded once per connection with a compact
shape: `call_id`, `method`, `path`, `status`, `frames_sent`, and
`frames_received`. Caps only bound what is recorded — the full
request/response is still served to the caller.

## TLS

Pass `-local-tls-cert` and `-local-tls-key` to enable HTTPS on the fixed listener:

```bash
./scripts/generate-certs.sh

zolem \
  -local-addr 127.0.0.1:18443 \
  -local-provider openai \
  -local-backend lorem \
  -local-tls-cert certs/localhost.pem \
  -local-tls-key certs/localhost-key.pem
```

Use [scripts/generate-certs.sh](../scripts/generate-certs.sh) to generate
locally-trusted certs with `mkcert`. It writes `certs/localhost.pem` and
`certs/localhost-key.pem`.
