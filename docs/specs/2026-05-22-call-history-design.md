# Call History Design

**Date:** 2026-05-22  
**Status:** Approved

## Summary

Zolem gains the ability to record every request/response it serves and expose
that history for post-hoc inspection by tests. The mental model is the
post-hoc assertion idiom used in Jest, sinon, and `unittest.mock`: the mock
records everything; tests query the record after the SUT runs.

## Decisions

| Dimension | Choice |
|-----------|--------|
| Style | Post-hoc inspection (no pre-declared expectations) |
| Scope | Per listener |
| Query model | Flat list returned by the API; filtering done client-side |
| Endpoints | Admin server (local runtime mode) |
| Recording | Always-on; no opt-in required |
| Lifecycle | Unbounded until explicit reset or listener deletion |
| Call IDs | Monotonic per-listener integer, resets to 1 on explicit clear |
| Sensitive headers | Stored verbatim (local mock; callers use throwaway tokens) |
| Streaming | Event list only; no aggregated body for SSE responses |
| Fixed-listener | JSONL file via opt-in `--local-calls-file` flag |

## Consumers

**Primary:** Any-language tests pointing at zolem, via HTTP API and `zolemc`.

**Secondary:** Zolem's own E2E test suite verifying its own behavior.

---

## Record Schema

One JSON object per call. The same schema is used by the HTTP API and the
JSONL file.

```json
{
  "call_id": 7,
  "listener": "openai-demo",
  "received_at": "2026-05-22T16:34:12.481Z",
  "completed_at": "2026-05-22T16:34:12.493Z",
  "latency_ms": 12,
  "request": {
    "method": "POST",
    "path": "/v1/chat/completions",
    "query": "",
    "headers": {
      "Authorization": ["Bearer sk-test"],
      "Content-Type": ["application/json"]
    },
    "remote_addr": "127.0.0.1:54812",
    "body": "{\"model\":\"gpt-4o\",\"messages\":[...]}",
    "body_truncated_bytes": 0
  },
  "response": {
    "status": 200,
    "headers": {
      "Content-Type": ["application/json"]
    },
    "body": "{\"id\":\"...\",\"choices\":[...]}",
    "body_truncated_bytes": 0,
    "stream": null
  }
}
```

For a streaming response `response.body` is empty and `response.stream` is
populated:

```json
"stream": {
  "event_count": 14,
  "events": [
    {
      "received_at": "2026-05-22T16:34:12.486Z",
      "event": "message_start",
      "data": "{...}"
    },
    {
      "received_at": "2026-05-22T16:34:12.488Z",
      "event": "content_block_delta",
      "data": "{...}"
    }
  ],
  "events_truncated": 0
}
```

### Schema notes

- `headers` is `map[string][]string`, matching `net/http` — duplicates are
  preserved.
- Bodies are UTF-8 strings. If a body is not valid UTF-8, it is base64-encoded
  into a `body_base64` field instead; `body` is omitted.
- `body_truncated_bytes` is the number of bytes dropped past the cap. `0` means
  the body is complete.
- `events_truncated` is the number of SSE events dropped past the event cap.
- `call_id` is scoped to the listener and resets to `1` on `DELETE /calls`.

---

## Architecture

### Recorder interface

A `Recorder` interface is the sole storage abstraction:

```go
type Recorder interface {
    Record(call Call)
    List() []Call
    Clear() int // returns count cleared
    Close()
}
```

V1 ships an `inMemoryRecorder`. Fixed-listener mode (see below) uses a
`jsonlRecorder`. The capture middleware and admin handler operate against the
interface.

### managedLocalListener

Each `managedLocalListener` (in `cmd/zolem/local_admin.go`) gains a `recorder
Recorder` field. It is wired at listener-creation time and dropped when the
listener is deleted.

### Capture middleware

A `recordingMiddleware(recorder Recorder, caps RecordCaps) func(http.Handler) http.Handler`
wraps the provider handler. It:

1. Stamps `received_at` and atomically assigns `call_id` before delegating.
2. Wraps the `http.ResponseWriter` in a `recordingResponseWriter` that intercepts
   `WriteHeader`, `Write`, and (for SSE) `Flush`.
3. For SSE responses, parses each `Write` chunk as SSE frames and appends events
   to the in-progress record.
4. After the handler returns, stamps `completed_at`, computes `latency_ms`, and
   calls `recorder.Record(call)`.

No changes to provider handler internals. The middleware is injected at the
`http.Handler` construction site inside `managedLocalListener`.

### Concurrency

`call_id` is assigned from a `sync/atomic` counter at request arrival, so IDs
reflect arrival order under concurrency. Buffer append uses a `sync.Mutex`.

### Body / event caps

Caps are per-listener and stored in the listener's runtime config:

| Field | Default |
|-------|---------|
| `record_request_body_cap_bytes` | 262144 (256 KiB) |
| `record_response_body_cap_bytes` | 262144 (256 KiB) |
| `record_stream_event_cap` | 1024 |

All three are accepted as optional fields in the `POST /v1/listeners` (listener
create) payload and returned in the listener view.

---

## HTTP API (Admin Server)

Both endpoints follow the existing `/v1/listeners/...` naming and error conventions.

### `GET /v1/listeners/<name>/calls`

Returns the listener's full call history.

**Response 200:**
```json
{ "calls": [ <Call>, ... ] }
```

Object wrapper (not bare array) reserves room for future top-level fields
without a breaking change.

**Response 404:** listener does not exist (same shape as other listener 404s).

### `DELETE /v1/listeners/<name>/calls`

Drops the call buffer and resets `call_id` to `1`.

**Response 200:**
```json
{ "cleared": 12 }
```

Idempotent: clearing an empty history returns `{ "cleared": 0 }`.

**Response 404:** listener does not exist.

### Listener lifecycle interactions

- **Listener deletion** — drops the recorder with the listener. No orphaned data.
- **Listener upsert (profile swap)** — history is **preserved**. A test that
  swaps a profile mid-run retains the calls made before the swap.

---

## zolemc Surface

Two new subcommands nested under the existing `listeners` group.

```
zolemc -admin-url <url> listeners calls list <name>
zolemc -admin-url <url> listeners calls clear <name>
```

### `listeners calls list <name>`

Default (human) output: compact table with columns `call_id`, `method`, `path`,
`status`, `latency_ms`, `received_at`. Streaming calls prefix status with `~`.

`-json`: writes the full `{"calls":[...]}` response verbatim. Use this in test
scripts.

`-since <call_id>`: filters client-side to records with `call_id > N`.

### `listeners calls clear <name>`

Prints: `Cleared N calls from listener <name>.`

`-json`: prints `{"cleared": N}`.

Both subcommands follow the existing `-admin-url` / env-var convention and exit
non-zero with a `zolem: <message>` error on 4xx/5xx.

---

## Fixed-Listener Mode: JSONL File

When zolem is started in fixed-listener mode (`-local-provider`), an optional
flag enables call recording to a file:

```
zolem -local-provider openai -local-addr 127.0.0.1:19001 \
      -local-calls-file /tmp/zolem-calls.jsonl
```

- When the flag is absent, no recording occurs.
- The file is opened with `O_APPEND | O_CREATE` and flushed after each
  completed call. Each line is a complete, valid JSON `Call` object.
- Body and stream caps from `record_request_body_cap_bytes`,
  `record_response_body_cap_bytes`, and `record_stream_event_cap` apply; they
  are accepted as flags in fixed-listener mode.

**Consuming the file from tests:**

```bash
# shell
jq '.call_id, .request.path' /tmp/zolem-calls.jsonl

# Python
calls = [json.loads(line) for line in open("/tmp/zolem-calls.jsonl")]
```

**Reset:** the test truncates or deletes the file between test cases. Zolem
does not expose a reset mechanism for fixed-listener mode.

The `Recorder` interface makes this a matter of wiring a `jsonlRecorder`
implementation at fixed-listener startup — no changes to the capture middleware
or schema.

There is no HTTP call-history API in fixed-listener mode (no admin server
runs). A future `--local-calls-addr` control port could replicate the HTTP
endpoints if needed.

---

## Tests

New E2E tests in `cmd/zolem/` follow the patterns in `local_runtime_e2e_test.go`.

### Admin server (local runtime)

- **Basic recording:** create a listener, send one request, `GET /calls`,
  assert the record contains correct method / path / status / body.
- **Body cap:** send a request exceeding the cap; assert `body_truncated_bytes > 0`
  and body is capped.
- **Streaming:** send a request to an SSE endpoint; assert
  `response.stream.events` is populated and `response.body` is empty.
- **Reset:** send two requests, `DELETE /calls`, assert `cleared: 2`, then
  `GET /calls` returns `calls: []` and next `call_id` restarts at `1`.
- **Listener deletion:** send requests, delete the listener, re-create it; assert
  fresh history with no carryover.
- **Listener mutation:** send requests across an upsert; assert history is
  preserved.
- **Concurrency:** fire N requests in parallel; assert N records with distinct
  `call_id`s.

### Fixed-listener (filesystem JSONL)

- Start zolem with `--local-calls-file <tmpfile>`, send two requests, read the
  JSONL file, assert two valid records.
- Body cap is respected in JSONL output.
- Second batch of requests appends to the file (records are not overwritten).

### zolemc integration

- `listeners calls list` prints the expected table for a listener with known calls.
- `listeners calls list -json` matches the HTTP response body.
- `listeners calls clear` returns the expected cleared-count message.
