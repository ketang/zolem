# End-to-End Testing Standard

This repo uses end-to-end testing to verify real client-visible behavior, not
just internal control flow.

## When E2E Is Required

Add or extend E2E coverage when a change affects observable runtime behavior,
including:

- local runtime profiles or backends
- admin API and listener lifecycle flows
- provider-visible request validation or response envelopes
- streaming behavior
- TLS behavior
- any HTTP-layer behavior a real client would depend on

Unit and handler tests are not a substitute for E2E coverage in those cases.

## Accepted E2E Layers

This repo uses two complementary E2E layers:

- cross-process Go tests under `cmd/zolem`
- executable smoke scripts such as `scripts/test-local-runtime.sh` and `scripts/smoke.sh`

Prefer extending an existing harness before creating a new one.

## Cross-Process Go Tests

Cross-process Go E2E tests should:

- start a real `go run ./cmd/zolem` process
- configure it through real config files or real admin API calls
- exercise real HTTP endpoints
- assert status, headers, and provider-specific body shape
- use temp dirs and isolated caches
- clean up any runtime-created resources

For local runtime features, the expected flow is:

1. start the admin server
2. create profiles through `/_zolem/profiles/...`
3. create listeners through `/_zolem/listeners/...`
4. call the returned `base_url`
5. verify `/_zolem/state` when relevant
6. clean up listeners and profiles

## Smoke Scripts

Smoke scripts are the operator-facing verification workflow. They should:

- run against a real server process
- be repeatable without external network dependencies
- fail fast on mismatches
- cover the same client-visible behavior a developer would manually verify

If a runtime feature is important enough to document for manual use, it usually
deserves a smoke-path update too.

## Assertion Standard

E2E tests should assert the behavior clients actually consume:

- status code
- protocol-specific headers
- provider-specific body schema
- stream framing when applicable
- listener or runtime state when relevant

Avoid weak assertions like:

- process started
- response was JSON
- one field existed
- request returned 200

## Isolation And Stability

Keep E2E tests deterministic:

- use temp dirs for config, fixtures, and caches
- avoid dependence on remote services or mutable network state
- avoid hidden ordering dependencies between tests
- ensure cleanup happens even on failure

Do not make ordinary local test runs flaky.

## Agent Expectations

Agents working in this repo should assume:

- if a change alters observable runtime behavior, E2E coverage is expected in the same change unless there is a documented reason not to
- extending an existing E2E harness is preferred over adding a new ad hoc runner
- local runtime features should usually be tested through admin API plus listener API together
- documentation for a new runtime workflow should normally be backed by an executable verification path
