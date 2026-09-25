# TypeSafe (Jev System One) provider

Zolem mocks TypeSafe's Jev System One API: `POST /v1/systemone` and
`GET /v1/models`. Unlike the chat-completion providers zolem otherwise mocks
(Anthropic, Gemini, Ollama, OpenAI), this is a request/response, non-streaming
surface. Instead of free text, the client sends a `state` blob plus a map of
typed `questions`, and gets back typed judgments with probabilities — the
model cannot produce anything outside the shape the question declares.

## Provenance: confirmed vs. invented

The request/response wire shape below is **confirmed** against the live docs
at docs.typesafe.ai (`api.md`, `models.md`, `primitives/choice.md`,
`primitives/noul.md`, `primitives/score.md`, `migrating-to-v1.md`) and the
[official JavaScript SDK](https://github.com/typesafe-ai/typesafe-sdk-js),
checked 2026-09-23. In
particular, the response field is `probabilities` (the v1 rename of the
predecessor API's `distribution`), not `distribution` — an earlier
third-party-sourced draft of this feature used the wrong name and was
corrected before implementation.

`GET /v1/models` and its response are documented in the
[TypeSafe models reference](https://docs.typesafe.ai/models.md) and the
[official SDK's models resource](https://github.com/typesafe-ai/typesafe-sdk-js/blob/main/src/resources/models.ts).
Zolem serves `{"models": [{"name": "jev-latest", "description": "...",
"release_date": "..."}]}`. The list is synthetic; TypeSafe says its live
endpoint lists models available to the authenticated account and currently
lists aliases. See `internal/provider/typesafe/models.go`.

Error bodies are **partly invented**. The
[API reference](https://docs.typesafe.ai/api.md) gives status codes but no
JSON schema, and it documents `422` as the request-validation failure status.

- **Request validation failures** (schema violations, malformed or empty JSON,
  a missing `model`, an unreadable body) return `422` with a FastAPI-style body,
  `{"detail": [{"loc": ["body"], "msg": "..."}]}`. The status is documented; the
  `detail` shape is **inferred** from the
  [official SDK's error parser](https://github.com/typesafe-ai/typesafe-sdk-js/blob/main/src/errors.ts),
  which reads `detail[].loc` and `detail[].msg`. `loc` is always `["body"]`
  because the shared schema validator returns message strings only; each `msg`
  already embeds the JSON pointer of the offending field.
- **Every other error** (401, 404, 500, and the `error` backend's forced
  errors) uses the invented `{"error": {"type": ..., "message": ...}}`
  envelope, which the SDK's parser also accepts. The forced
  `invalid_request` error stays `400`, since the SDK treats 400
  (`BadRequestError`) and 422 (`UnprocessableEntityError`) as distinct classes.

See `internal/provider/typesafe/errors.go` for zolem's behavior.

## Request shape

```json
{
  "model": "jev-latest",
  "state": "<string | object | array>",
  "questions": {
    "<id>": {
      "type": "noul" | "choice" | "score",
      "instructions": "<string | object | array>",
      "criteria": { }
    }
  }
}
```

`state` and each question's `instructions` may be a string, object, or array.
`questions` must contain at least one entry.

### The three question primitives

- **`noul`**: a yes/no statement. `criteria` is optional, an object with
  optional `"true"`/`"false"` description keys. Response: `{"type": "noul",
  "noul": <0-1>}` — a single probability, no `confidence` field.
- **`choice`**: pick one option from a caller-defined set. `criteria` is
  required: an object mapping option name to a string description, with 2 to
  255 options. Response: `{"type": "choice", "choice": "<key>",
  "probabilities": {"<key>": <0-1>, ...}, "confidence": <0-1>}`.
- **`score`**: rate the state against 2 to 10 ordered levels. `criteria` is
  required: an array of 2 to 10 level descriptions, each either a string or a
  `{"what": ..., "examples": ...}` object; the level's 0-based array index is
  its identity. Response: `{"type": "score", "score": <float>, "legend":
  {"<idx>": "<desc>", ...}, "probabilities": {"<idx>": <0-1>, ...},
  "confidence": <0-1>}`. `score` may fall between levels — it is the
  probability-weighted position, not necessarily an integer.

### Authentication

`Authorization: Bearer <key>` is required to be **present**; its contents are
accepted and ignored, matching how zolem treats every other provider's key.
Requests without the header get a 401.

## Answer validation (every backend)

Before any response is sent — synthetic or fixture-backed — zolem checks it
against the request with `internal/provider/typesafe.ValidateAnswers`:

- every question key has exactly one answer, and there are no extra answers;
- an answer's `type` matches its question's `type`;
- `noul`: the value is in `[0, 1]`;
- `choice`: `choice` names one of the request's options, `probabilities` has
  exactly those options as keys, the values sum to 1 within `1e-6`, and
  `choice` is the argmax of `probabilities`;
- `score`: `probabilities` keys are exactly the request's level indices (as
  strings), the values sum to 1 within `1e-6`, and `score` equals the
  probability-weighted 0-based position within `1e-6`.

A violation — from any backend, including a hand-written fixture — is a `500`
naming the question key and the broken rule, for example:

```json
{"error": {"type": "contract_violation_error", "message": "question \"category\": choice \"nonexistent\" is not one of the request's options"}}
```

This is the point of mocking a model that structurally cannot hallucinate: the
mock enforces the same contract, so a fixture author's mistake surfaces
immediately instead of shipping a client that trusts an invalid shape.

## Synthetic backends

- **`lorem`** (default): deterministic. `choice` and `score` put 0.9 on the
  first option/level (by request JSON order) and spread the remaining 0.1
  evenly over the rest; `noul` is always `0.5`. Useful for smoke tests where
  only response shape matters.
- **`faker`**: seeded pseudo-random. The seed is derived from a hash of the
  raw request body plus the question id, so the same request always produces
  the same answer, and a different request (a different `state`, in
  particular) produces a different one. Useful for exercising a consumer's
  threshold logic against varied, still-valid answers.
- **`fixture`**: `response.json` (or `response.json.tmpl`) holds the full
  response envelope: `model`, `answers`, and `usage`. Its answers are validated
  against the request before being served. Templated TypeSafe fixtures also
  receive the parsed request as `.Request`, including `.Request.state` and
  `.Request.questions` (see
  [docs/fixture-authoring.md](fixture-authoring.md#typesafe)); a full example
  fixture is there too. Sequences work unchanged.
  An unmatched fixture falls back to deterministic `lorem` answers, as with
  the other providers. A fixture with a non-2xx status serves its error body
  and status directly; answer validation applies to successful responses.
- **`error`**: always returns the profile's pinned forced error.

### Not yet supported for this provider

- **`ollama` backend** (a real local model answering questions from logprobs):
  tracked separately as zolem-w0i, blocked on this issue. Selecting
  `backend: ollama` for a `typesafe` listener is rejected when the listener is
  created.
- **`wasm` backend**: the generic profile-supplied WASM content-generator
  backend is untested against this provider's answer shape and is out of
  scope for this issue. Selecting it is also rejected at listener creation.

## Example

```bash
zolem -local-provider typesafe -local-addr 127.0.0.1:19010 -local-backend lorem
```

```bash
curl -s http://127.0.0.1:19010/v1/systemone \
  -H 'Authorization: Bearer sk-test' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "jev-latest",
    "state": {"item": "vintage lamp", "condition": "used"},
    "questions": {
      "is_fragile": {"type": "noul", "instructions": "Is this item fragile?"},
      "category": {"type": "choice", "instructions": "Pick a category",
        "criteria": {"electronics": "electronic devices", "furniture": "furniture and decor"}},
      "quality": {"type": "score", "instructions": "Rate the condition",
        "criteria": ["poor", "fair", "good", "excellent"]}
    }
  }'
```
