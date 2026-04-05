# Zolem Spec And Smoke Execution Plan

> Goal: close the remaining MVP gaps around manual smoke testing and contract-source correctness without changing the product scope. This plan separates contract ingestion from compatibility testing so Zolem can validate requests from canonical provider references while also proving that real SDK clients can talk to the running service.

## Scope

This plan covers four provider families in the first batch:

- Anthropic
- OpenAI
- OpenRouter
- Gemini

It addresses two distinct problems:

1. Contract ingestion:
   load canonical provider API references into Zolem so request validation is based on authoritative upstream contracts.
2. Compatibility verification:
   prove that a running Zolem instance is usable through real client behaviors, including raw HTTP smoke tests and vendor SDK self-tests.

## Design Principles

- Treat canonical provider API references as the source of truth for validation.
- Do not use vendor SDKs as the contract source.
- Use vendor SDKs as compatibility probes against a running Zolem instance.
- Keep provider-specific source handling explicit instead of hiding everything behind a raw URL map.
- Normalize upstream contract formats into one internal validation representation before the request validator runs.
- Never disable validation merely because a remote refresh source is temporarily unavailable if a bundled normalized fallback exists.

## First-Batch Contract Sources

### OpenAI

- Canonical human-readable reference:
  - `https://platform.openai.com/docs/api-reference`
- Canonical machine-readable source:
  - official OpenAPI publication from `openai/openai-openapi`
- Ingestion format:
  - OpenAPI

### OpenRouter

- Canonical human-readable reference:
  - `https://openrouter.ai/docs/api-reference/overview`
- Canonical machine-readable sources:
  - `https://openrouter.ai/openapi.yaml`
  - `https://openrouter.ai/openapi.json`
- Ingestion format:
  - OpenAPI

### Gemini

- Canonical human-readable reference:
  - `https://ai.google.dev/api/rest/generativelanguage`
- Canonical machine-readable sources:
  - `https://generativelanguage.googleapis.com/$discovery/rest?version=v1`
  - `https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta`
- Ingestion format:
  - Google Discovery

### Anthropic

- Canonical human-readable reference:
  - `https://docs.anthropic.com/en/api/overview`
- Public canonical machine-readable source:
  - none verified
- Ingestion format for Zolem:
  - vendored normalized schema snapshot derived from Anthropic docs

## Internal Architecture Target

Replace the current `specSourceMap()` string map with a typed source registry.

Suggested source kinds:

- `openapi`
- `discovery`
- `vendored_docs_snapshot`

Suggested registry shape:

```go
type SourceKind string

const (
	SourceKindOpenAPI            SourceKind = "openapi"
	SourceKindDiscovery          SourceKind = "discovery"
	SourceKindVendoredDocSnapshot SourceKind = "vendored_docs_snapshot"
)

type ContractSource struct {
	Provider      string
	Version       string
	Kind          SourceKind
	RemoteURL     string
	FallbackPath  string
	ContentType   string
	Enabled       bool
}
```

Suggested pipeline:

1. Lookup provider/version in the contract registry.
2. Load vendored normalized fallback first.
3. Attempt remote fetch if the source kind has a remote endpoint.
4. Parse and normalize the upstream artifact into Zolem's internal validation schema representation.
5. Atomically update the validator state for that provider/version only on successful normalization and compile.

## Phase 1: Smoke Test Runner

### Objective

Turn the planned manual smoke test into a repeatable executable workflow that proves a real Zolem process behaves correctly at the HTTP layer.

### Deliverables

- `scripts/smoke.sh` or equivalent checked into the repo
- optional `Makefile` target such as `make smoke`
- cleanup-safe temp config/fixture/spec-cache provisioning
- deterministic assertions for non-streaming JSON and SSE behavior

### Work Items

1. Build or run Zolem from `cmd/zolem`.
2. Provision a temporary config file with routes for first-batch providers.
3. Provision temporary fixture directories and normalized contract cache where needed.
4. Start the server on a dynamically selected local port.
5. Run real `curl` probes for:
   - Anthropic JSON response
   - Anthropic streaming SSE
   - OpenAI streaming SSE
   - OpenRouter non-streaming or streaming, depending on first supported endpoint
   - Gemini JSON or SSE
   - unmatched-host error behavior
6. Fail on mismatched status, headers, payload shape, or missing SSE termination markers.
7. Shut down the process and remove temp artifacts via `trap`.

### Acceptance Criteria

- The smoke script exits zero on success and nonzero on any mismatch.
- The script can be run locally without manual cleanup.
- The script verifies both standard JSON responses and SSE framing.
- The script becomes the reference workflow for manual and CI smoke verification.

## Phase 2: Contract Registry Refactor

### Objective

Replace brittle hard-coded source URLs with a provider-aware registry and normalization pipeline.

### Deliverables

- contract registry package or module
- parser/normalizer interface
- startup wiring updated to load sources through the registry
- removal of direct `specSourceMap()` reliance

### Work Items

1. Introduce a typed contract registry for provider/version source metadata.
2. Split source retrieval from source parsing.
3. Add an internal normalized schema representation boundary.
4. Update startup to:
   - load fallback normalized contracts first
   - then attempt remote refresh
   - log warnings without dropping the last known-good schema
5. Add provider/version-level tests around registry lookup and startup loading behavior.

### Acceptance Criteria

- Startup no longer depends on a raw `map[string]string` URL table.
- Each provider/version is explicitly tied to a source kind.
- Validation can continue from a bundled fallback even when upstream fetch fails.

## Phase 3: OpenAPI Ingestion

### Objective

Support canonical OpenAPI inputs for OpenAI and OpenRouter.

### Deliverables

- OpenAPI loader
- operation-to-request-schema extraction logic
- normalization of extracted request-body schemas into Zolem's internal validation schema form

### Work Items

1. Add OpenAPI document parsing for YAML and JSON.
2. Define the exact operations Zolem supports for:
   - OpenAI
   - OpenRouter
3. Extract request-body schemas only for those supported operations.
4. Normalize OpenAPI schema objects into internal JSON Schema form suitable for validation.
5. Add fixtures or golden tests for:
   - valid OpenAPI parse
   - missing operation
   - malformed schema object
   - representative valid and invalid request bodies

### Acceptance Criteria

- OpenAI contracts are loaded from the official OpenAPI source.
- OpenRouter contracts are loaded from the official OpenAPI source.
- Supported request payloads validate correctly through the normalized validator path.

## Phase 4: Discovery Ingestion

### Objective

Support canonical Google Discovery inputs for Gemini.

### Deliverables

- Discovery document loader
- extraction logic for the Gemini methods Zolem emulates
- normalization into internal validation schema form

### Work Items

1. Parse Google Discovery documents for `v1` and `v1beta`.
2. Identify the exact Gemini methods Zolem supports in the first batch.
3. Extract request schemas for those methods.
4. Normalize Discovery schema descriptions into internal JSON Schema form.
5. Add extraction and validation tests using real or pinned discovery fixtures.

### Acceptance Criteria

- Gemini `v1` and `v1beta` can be loaded from official discovery documents.
- Supported Gemini request payloads validate through the same normalized validator path used by other providers.
- Extraction failures are surfaced clearly and do not corrupt existing validator state.

## Phase 5: Anthropic Docs-Derived Normalized Snapshot

### Objective

Provide a canonical-enough validation source for Anthropic despite the absence of a verified public machine-readable spec.

### Deliverables

- vendored normalized schema artifact for the supported Anthropic endpoints
- documentation describing the derivation source and update workflow
- tests proving the normalized snapshot matches current supported request shapes

### Work Items

1. Define the first supported Anthropic endpoints and versions.
2. Derive a normalized request schema from Anthropic's official docs for those endpoints.
3. Store the result as a vendored generated artifact in the repo.
4. Document the derivation/update process clearly so it is reviewable and repeatable.
5. Add regression tests for representative valid and invalid Anthropic requests.

### Acceptance Criteria

- Anthropic validation is backed by a vendored normalized snapshot derived from official docs.
- The derivation process is documented and repeatable.
- Anthropic request validation no longer depends on dead or guessed remote URLs.

## Phase 6: Source Verification And Drift Tests

### Objective

Fail loudly when upstream contract sources move, break, or stop matching the parser assumptions.

### Deliverables

- dedicated source-verification test or command
- representative contract extraction fixtures
- drift tests for each supported provider/version

### Work Items

1. Add a verification command or targeted test suite that:
   - fetches or loads the configured canonical source
   - parses it
   - extracts the supported request schemas
   - validates representative sample requests
2. Keep these tests separate from basic unit tests if network access or source churn would make normal test runs flaky.
3. Record provider/version-specific invariants:
   - required operations
   - required fields
   - expected schema extraction targets

### Acceptance Criteria

- Broken upstream source URLs or incompatible format changes are detected explicitly.
- The project has a repeatable way to re-check source validity before refresh-loop work lands.

## Phase 7: SDK Compatibility Self-Tests

### Objective

Use vendor SDKs to prove that real client code can talk to a running Zolem instance with acceptable API compatibility.

### Deliverables

- provider-specific SDK compatibility tests
- test harness for starting Zolem and pointing SDKs at it
- coverage for auth, standard requests, streaming, and representative error handling

### Work Items

1. Start a local Zolem instance inside the compatibility test harness.
2. Configure vendor SDKs to target the local base URL or override endpoint configuration.
3. Add first-batch SDK probes for:
   - Anthropic SDK
   - OpenAI SDK
   - OpenRouter client or SDK if officially supported and configurable
   - Gemini client library if endpoint override is feasible
4. Cover:
   - auth headers
   - non-streaming success path
   - streaming success path
   - representative error path
5. Keep these tests focused on compatibility, not on replacing protocol-level assertions.

### Acceptance Criteria

- At least Anthropic and OpenAI SDK compatibility tests pass against local Zolem.
- SDK tests confirm real client behavior beyond raw HTTP `curl` smoke probes.
- Compatibility failures are surfaced separately from contract ingestion failures.

## Phase 8: Refresh Loop Integration

### Objective

Add safe periodic contract refresh on top of the new source registry.

### Deliverables

- periodic refresh loop tied to `cfg.Specs.RefreshInterval`
- provider/version scoped updates
- last-known-good preservation
- shutdown-safe lifecycle integration

### Work Items

1. Add a refresh goroutine in startup.
2. Refresh each provider/version independently.
3. Preserve the last known-good normalized schema when refresh fails.
4. Emit logs for refresh success, failure, and recovery.
5. Add tests for:
   - successful refresh
   - failed refresh with preserved validator state
   - malformed upstream payload
   - shutdown behavior

### Acceptance Criteria

- Contract refresh is periodic and safe.
- A bad refresh does not wipe working validation state.
- The refresh loop builds on the new registry instead of reintroducing raw URL coupling.

## Recommended File-Level Changes

### New Files

- `docs/plans/2026-04-05-zolem-spec-and-smoke-execution-plan.md`
- `scripts/smoke.sh`
- `internal/specs/registry.go`
- `internal/specs/openapi.go`
- `internal/specs/discovery.go`
- `internal/specs/normalize.go`
- `internal/specs/registry_test.go`
- `internal/specs/openapi_test.go`
- `internal/specs/discovery_test.go`
- `internal/specs/source_verify_test.go` or an equivalent verification command
- provider compatibility test files as needed

### Updated Files

- `cmd/zolem/startup.go`
- `internal/specs/fetcher.go`
- `internal/specs/validator.go`
- test helpers under `cmd/zolem/` as needed for smoke and compatibility support

### Vendored Artifacts

- normalized Anthropic schema snapshot(s)
- pinned extraction fixtures for OpenAPI and Discovery inputs if needed for deterministic tests

## Execution Order

1. Phase 1: smoke test runner
2. Phase 2: contract registry refactor
3. Phase 3: OpenAPI ingestion
4. Phase 4: Discovery ingestion
5. Phase 5: Anthropic docs-derived snapshot
6. Phase 6: source verification and drift tests
7. Phase 7: SDK compatibility self-tests
8. Phase 8: refresh loop integration

## Risks And Watchpoints

- OpenAPI-to-JSON-Schema normalization may be lossy if not scoped tightly to the request shapes Zolem actually supports.
- Discovery documents may not map cleanly onto the current validator assumptions without a provider-specific normalization step.
- Anthropic docs-derived normalization needs a documented update process to avoid turning into a silent hand-maintained fork.
- SDK compatibility tests may require provider-specific endpoint override mechanics that are not symmetrical across vendors.
- Remote source verification should not make ordinary local test runs flaky; keep network-dependent checks isolated.

## Definition Of Done

This plan is complete when:

- Zolem has a repeatable smoke test runner for real HTTP behavior.
- OpenAI and OpenRouter ingest official OpenAPI sources.
- Gemini ingests official Discovery sources.
- Anthropic validates against a documented vendored normalized snapshot derived from official docs.
- Vendor SDK compatibility tests exist for the supported providers where endpoint override is practical.
- Startup and refresh logic preserve last known-good validation state instead of silently degrading to no validation.
