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
| `-local-provider <provider>` | Provider: `anthropic`, `gemini`, `ollama`, or `openai` |
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

## Host Header Guard

Zolem listeners have no authentication, so they only accept requests whose
`Host` header is `localhost`, a loopback IP literal (`127.0.0.1`, `[::1]`), or a
name passed with `-allowed-host`. This blocks DNS-rebinding, where a web page
resolves an attacker-controlled hostname to `127.0.0.1` and drives the listener
from a browser. Any other `Host` gets `403` with
`{"error":"host \"evil.example\" not allowed; this listener serves loopback clients only"}`,
including on the OpenAI Responses WebSocket upgrade.

If you legitimately reach zolem through an alias (an `/etc/hosts` entry, or a TLS
certificate issued for a custom hostname that points at `127.0.0.1`), allow it
with the repeatable `-allowed-host` flag. The list is additive to
`localhost`/loopback, and any port on an entry is ignored:

```bash
zolem -local-provider openai -local-addr 127.0.0.1:18080 -allowed-host zolem.test
```

Host values other than `localhost`, `127.0.0.1`, `[::1]` and `-allowed-host`
entries are rejected, including `0.0.0.0:<port>`, a trailing-dot `localhost.`,
and IPv6 zone forms such as `[::1%25lo]`; add them with `-allowed-host` if you
need them. Rejected requests never reach the handler, so they are not written
to the `-local-calls-file` recording. `-allowed-host` takes a bare hostname
(a port is ignored); empty values and URLs are a usage error (exit 2).

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
