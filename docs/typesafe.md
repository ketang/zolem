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

- **`ollama-logprob`**: answers with a real local model, using its next-token
  log probabilities as the probability distribution. Valid only for this
  provider; see [the section below](#the-ollama-logprob-backend).

### Not supported for this provider

- **`ollama` backend** (the chat-completion forwarder): meaningless here, since
  Jev answers are typed judgments rather than free text. Selecting
  `backend: ollama` for a `typesafe` listener is rejected when the listener is
  created. Use `ollama-logprob` instead.
- **`wasm` backend**: the generic profile-supplied WASM content-generator
  backend is untested against this provider's answer shape. Selecting it is
  also rejected at listener creation.

## The `ollama-logprob` backend

An offline, roughly compatible stand-in for the real Jev model, for developing
a consumer against live-model behavior without a TypeSafe key. Unlike
`lorem`/`faker`/`fixture` it needs a running Ollama (0.12.11 or newer, the
first release that returns log probabilities) with the model pulled.

### How a question is answered

Each question is one call to the upstream's native `POST /api/generate` with
`stream: false`, `logprobs: true`, `top_logprobs: 20`, and
`options: {num_predict: 1, temperature: 0}`. The prompt contains the question's
instructions, the serialized `state`, and the options rendered as short labels
(with the option's description when there is one), and asks for the label only:

```
Which department should handle this ticket?

State:
{"ticket": "I was charged twice ..."}

Options:
1. billing: payments, charges, refunds
2. sales: new purchases and pricing
3. support: technical problems

Reply with only the label of the best option.
Label:
```

The first generated token's alternatives (`logprobs[0].top_logprobs`) are then
matched to labels: each token is trimmed and matched to a label (case-insensitively,
ignoring a trailing `.`, `)` or `:`), tokens that map to the same label are summed,
each weight is `exp(logprob / calibration_temperature)`, and the weights are
renormalized over the request's options. Options that did not appear get
probability 0. The OpenAI-compatible `/v1` endpoint is not used because it drops
logprobs.

- **`choice`**: options are the labels. `choice` is the highest-probability
  option, and `confidence` is that probability.
- **`score`**: levels are the labels, ordered lowest to highest. `score` is the
  probability-weighted 0-based position; `confidence` is the largest level
  probability.
- **`noul`**: `1. yes` / `2. no` (with the question's optional `true`/`false`
  criteria as descriptions). `noul` is the renormalized `yes` probability.

Every answer passes the same validator as the other backends before it is sent.

### Labels

Up to nine options use the digits `1`-`9`, which are a single token on every
tokenizer. Ten or more options (or a 10-level score) use the letters `A`-`Z`,
then `AA`, `AB`, ..., because a label like `10` commonly tokenizes as `1` then
`0` and would be indistinguishable from option 1 at the first token. Labels
past 26 options can split the same way, so a `choice` with more than 26 options
attributes such mass to the single-letter label.

### Profile options

- `backend: "ollama-logprob"` (`-local-backend ollama-logprob` in fixed-listener
  mode).
- `backend_model` (required; `-local-backend-model`): the Ollama model to ask.
  This is the same field the `ollama` backend uses, so
  `response_model_policy: force_backend` reports it as the response `model`.
- `ollama_upstream` (`-local-ollama-upstream`): defaults to
  `http://localhost:11434` and is subject to the same loopback/private-host
  policy as the `ollama` backend (`allow_external_ollama_upstream` opts out;
  link-local addresses are never allowed).
- `calibration_temperature` (`-calibration-temperature` in `zolemc`,
  `-local-calibration-temperature` in fixed-listener mode): a positive, finite
  divisor applied to each log probability before exponentiating. Default `1.0`;
  above 1 flattens the distribution, below 1 sharpens it. Zero, negative, NaN
  and infinite values are rejected when the profile is created.

```bash
zolem -local-provider typesafe -local-addr 127.0.0.1:19010 \
  -local-backend ollama-logprob -local-backend-model gemma3:1b
```

### Errors and limitations

- If the upstream is unreachable, returns a non-200, or returns no `logprobs`
  field (an Ollama older than 0.12.11), the request fails with `502`; the
  message says the upstream did not return logprobs in the last case.
- If no returned token maps to any label (the model answered in prose), the
  distribution is uniform and zolem logs a warning.
- Ollama caps `top_logprobs` at 20, so a `choice` with more than 20 options
  gets probability mass on at most the top 20 labels; the rest are 0.
- Raw logprobs are not calibrated. Small models are often extremely
  confident, so treat `confidence` as indicative only; `calibration_temperature`
  is the only knob.
- Each question is a separate upstream call, made one after another.
- Quality depends on the model. In a manual check against `gemma3:1b`, a
  clear billing ticket answered `billing` at 0.99, while a 12-option
  categorization of a glass vase split its mass between two labels.

To check it against a real model, start Ollama, pull a model, run the command
above, and post a request like the one in the example below.

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
