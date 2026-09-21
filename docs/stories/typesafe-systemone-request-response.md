---
schema_version: 1
title: Mock TypeSafe Jev System One requests
slug: typesafe-systemone-request-response
status: draft
authority: observed
change_resistance: low
locked_sections: []
---

# Mock TypeSafe Jev System One requests

## Intent
A developer integrating TypeSafe's Jev System One API gets typed, contract-valid probabilistic answers (noul/choice/score) from a local mock, with no TypeSafe key and reproducible results in CI.

## Story
A developer building against TypeSafe's Jev API wants to develop and test choice/score/noul question handling without a real TypeSafe key or nondeterministic model output. They start a zolem listener with -local-provider typesafe and point their client at it. Requests to POST /v1/systemone carrying a state blob and one or more typed questions get back typed answers with probabilities; GET /v1/models lists the available model catalogue. Every answer, from every backend (lorem, faker, or a fixture), is checked against the request's own questions before it is returned, so a malformed answer (a choice naming an option that doesn't exist, a score whose weighted position doesn't match its own probabilities) never reaches the client silently.

## Expected Behavior
POST /v1/systemone requires an Authorization: Bearer header to be present (its contents are accepted and ignored, like zolem's other providers) or returns 401. A valid request answers every question: noul returns a probability in [0,1]; choice returns one of the request's option keys plus a probability distribution over exactly those options, summing to 1, with the returned choice equal to the argmax; score returns a probability-weighted position over the request's 2-10 levels, a probability distribution over the level indices summing to 1, and a legend mapping each index to its description. The request schema rejects an unknown question type, an empty questions map, a choice with fewer than 2 or more than 255 options, and a score with fewer than 2 or more than 10 levels, each with a 400 in the provider's error shape. GET /v1/models lists at least jev-latest and also requires the Authorization header. The lorem backend is deterministic (first option/level favored 0.9/0.1); the faker backend is seeded from the request body, so an identical request always answers identically and a different state answers differently. A fixture-backend response is validated the same way as a synthetic answer; a fixture whose answer violates the contract (e.g. a choice naming an absent option) is a 500 that names the offending question key and the specific rule violated, not a silently wrong answer.

## Boundaries
Jev System One has no streaming and no batching beyond POST /v1/systemone; zolem does not implement either. Only the lorem, faker, fixture, and error backends are implemented for this provider; selecting backend=ollama (a real-model backend answering from logprobs, tracked separately as zolem-w0i) or backend=wasm returns a clear 500 rather than silently falling back to another backend. The GET /v1/models response shape and the error-body shape are not documented by TypeSafe's live docs and are implemented as a reasonable invented shape (see docs/typesafe.md for the provenance note); a real TypeSafe deployment's shapes may differ. Zolem never proxies to the real TypeSafe API.

## Auditable Claims
- POST /v1/systemone without an Authorization header returns 401
- a valid three-question request (noul, choice, score) returns 200 with all three answer types populated and contract-valid
- GET /v1/models returns 200 and lists jev-latest
- the request schema rejects an unknown question type, an empty questions map, and a score with fewer than 2 levels, each with 400
- the vendored schema rejects a choice with 256 options and accepts a choice with 2 options
- the faker backend returns byte-identical answers for two identical requests and different answers when state differs
- a fixture answer that violates the response contract (e.g. a choice naming an absent option) returns 500 naming the question key
- selecting backend=ollama or backend=wasm for a typesafe listener returns 500 with an explicit not-supported message rather than silently falling back to another backend

## Evidence

### Tests
- `internal/provider/typesafe/handler_test.go`
- `internal/provider/typesafe/validate_test.go`
- `internal/provider/typesafe/synth_test.go`
- `internal/provider/typesafe/fixture_test.go`
- `internal/specs/typesafe_schema_test.go`
- `cmd/zolem/typesafe_provider_e2e_test.go`

### Surface
- `cli: zolem -local-provider typesafe -local-addr 127.0.0.1:19010 -local-backend lorem`
- `http: POST /v1/systemone`
- `http: GET /v1/models`

### Docs
- `docs/typesafe.md`
- `docs/fixture-authoring.md#typesafe`
