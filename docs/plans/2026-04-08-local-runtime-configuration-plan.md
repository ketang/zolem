# Zolem Local Runtime Configuration Plan

> Goal: add a local-only runtime configuration model that keeps provider request paths and payloads unchanged while letting a local setup step choose behavior such as `lorem`, `fixture`, `faker`, or a specific backend model. The first version is intentionally in-memory only and does not include persistence, TTLs, or authentication.

## Scope

This plan covers the first implementation of runtime configuration for local use:

- setup happens out of band through a local control plane
- provider-compatible traffic uses a base URL change only
- listener ports select the active `(provider, profile)` pair
- every data-plane listener exposes a reserved `/_zolem/*` introspection surface
- TLS is supported for local listeners, but certificate issuance stays outside the repo

This plan does not include:

- remote multi-user controls
- auth or TTL enforcement
- proxy-based interception
- persistent storage

## Design Principles

- Keep interactive provider traffic protocol-faithful except for base URL authority.
- Treat runtime configuration as an in-memory control-plane concern, not a request-body concern.
- Make local profile selection explicit and debuggable.
- Keep the runtime engine independent from any future host-based or proxy-based selector.
- Preserve the current static startup path while the new local runtime path is added.

## Architecture Target

Introduce a local runtime engine with three core concepts:

```go
type RuntimeProfile struct {
	Name                string
	Backend             string // lorem, fixture, faker, model
	BackendModel        string
	ResponseModelPolicy string // echo_request, force_literal
	ResponseModel       string
	FixtureNamespace    string
	Seed                *int64
}

type ListenerSpec struct {
	Name     string
	Addr     string // 127.0.0.1:12001
	Provider string // openai, anthropic, gemini
	Profile  string // profile name
	TLS      bool
}

type ListenerRuntime struct {
	Spec    ListenerSpec
	Profile RuntimeProfile
}
```

Control plane responsibilities:

- store profiles in memory
- store listener specs in memory
- create and stop loopback-only data-plane listeners

Data plane responsibilities:

- intercept `/_zolem/*` before provider dispatch
- attach fixed listener runtime to request context
- dispatch directly to the selected provider handler

## Certificates

Local TLS certificates should come from a local development CA, not from checked-in cert files.

Recommended source:

- `mkcert`

Expected local flow:

1. Install `mkcert`.
2. Run `mkcert -install`.
3. Generate a local cert covering the first supported access names:
   - `localhost`
   - `127.0.0.1`
   - `::1`
4. Store the resulting cert and key outside version control, for example in a local ignored directory such as `.devcerts/`.
5. Pass the cert and key paths to Zolem via the new local runtime startup configuration.

Future note:

- if a proxy later terminates TLS, Zolem can accept plain HTTP on loopback and stop owning local cert handling directly

## Vertical Slices

### Slice 1: Fixed Local Listener With Introspection

#### Objective

Prove the new operating model with one loopback listener whose provider and profile are fixed at startup.

#### Deliverables

- `internal/runtime` package skeleton
- `ListenerRuntime` context helpers
- top-level data-plane handler that intercepts `/_zolem/*`
- `GET /_zolem/state`
- one fixed local listener path that serves provider traffic using `lorem`

#### Work Items

1. Add `internal/runtime` with the core runtime types and context accessors.
2. Add a top-level local-listener handler that:
   - serves `/_zolem/state`
   - attaches `ListenerRuntime` to context
   - dispatches directly to the configured provider handler
3. Add a startup path for one fixed local listener.
4. Read runtime context in provider handlers without changing provider request parsing.
5. Keep backend behavior in this slice limited to `lorem`.

#### Acceptance Criteria

- a loopback listener can serve one provider on one port
- `GET /_zolem/state` returns provider/profile/backend details for that port
- normal provider-compatible requests on that port still work
- no Host-based profile selection is required

#### Likely Files

- `internal/runtime/*.go`
- `cmd/zolem/startup.go`
- `cmd/zolem/main.go`
- `internal/provider/openai/handler.go`
- `internal/provider/anthropic/handler.go`
- `internal/provider/gemini/handler.go`

### Slice 2: Local Control Plane And Dynamic Listener Registration

#### Objective

Allow local setup to create profiles and create or remove data-plane listeners without restart.

#### Deliverables

- in-memory profile registry
- in-memory listener registry
- localhost-only admin API

#### Work Items

1. Add profile CRUD in the runtime package.
2. Add listener CRUD in the runtime package.
3. Add localhost-only admin endpoints:
   - `PUT /_zolem/profiles/{name}`
   - `GET /_zolem/profiles`
   - `DELETE /_zolem/profiles/{name}`
   - `PUT /_zolem/listeners/{name}`
   - `GET /_zolem/listeners`
   - `DELETE /_zolem/listeners/{name}`
4. Return a usable base URL when a listener is created.
5. Make listener replacement and shutdown deterministic.

#### Acceptance Criteria

- a client can create a profile and bind it to a new listener port
- `/_zolem/state` on that new port reflects the created profile
- listener removal cleanly stops serving traffic

#### Likely Files

- `internal/runtime/registry.go`
- `internal/runtime/admin*.go`
- `cmd/zolem/startup.go`

### Slice 3: TLS For Local Admin And Data-Plane Listeners

#### Objective

Serve both the admin API and data-plane listeners over HTTPS using locally generated certificates.

#### Deliverables

- TLS config path in the local runtime startup path
- listener startup support for cert and key paths
- operator-facing docs or plan references pointing to `mkcert`

#### Work Items

1. Add cert/key path handling to the local runtime startup mode.
2. Support TLS on both the control plane and data plane.
3. Return `https://...` base URLs when TLS is enabled.
4. Keep certificate material outside version control.
5. Fail fast with clear startup errors on missing or invalid cert files.

#### Acceptance Criteria

- HTTPS listeners start with supplied local cert and key files
- `/_zolem/health` and provider endpoints work over TLS
- certificate handling is documented as an external local setup step using `mkcert`

#### Likely Files

- `cmd/zolem/startup.go`
- `cmd/zolem/main.go`
- `internal/runtime/*.go`

### Slice 4: Explicit Runtime Backend Selection

#### Objective

Move provider behavior from implicit fallback logic to explicit runtime-driven execution.

#### Deliverables

- runtime execution resolver
- support for `lorem` and `fixture` backends
- provider handlers that consult runtime context before generating output

#### Work Items

1. Add a runtime execution selection layer.
2. Replace the current implicit `fixture else lorem` flow with:
   - `lorem`
   - `fixture`
3. Keep provider request validation and response serialization unchanged.
4. Preserve provider-specific SSE wire formats.

#### Acceptance Criteria

- one listener can be pinned to `lorem`
- another listener can be pinned to `fixture`
- the same provider endpoint behaves differently on those ports according to runtime profile

#### Likely Files

- `internal/runtime/execute.go`
- `internal/provider/openai/handler.go`
- `internal/provider/anthropic/handler.go`
- `internal/provider/gemini/handler.go`

### Slice 5: Fixture-Aware Runtime Constraints

#### Objective

Allow runtime profiles to narrow or shape fixture mode without changing the interactive protocol.

#### Deliverables

- fixture namespace handling in runtime profile execution
- optional matcher input extension if the first pass needs runtime metadata

#### Work Items

1. Decide whether `FixtureNamespace` alone is enough for the first pass.
2. If needed, extend fixture matching input with a runtime block.
3. Keep fixture filtering explicit and deterministic.
4. Avoid coupling the fixture matcher to transport details.

#### Acceptance Criteria

- two listeners for the same provider can use different fixture namespaces and produce different fixture results
- non-fixture listeners remain unaffected

#### Likely Files

- `internal/fixture/matcher.go`
- `internal/runtime/*.go`

### Slice 6: First Generated Backend Beyond Lorem

#### Objective

Add a backend that demonstrates the value of runtime configuration beyond the existing lorem/fixture modes.

#### Deliverables

- first generated backend, likely `faker`
- explicit response model policy handling

#### Work Items

1. Add `faker` behind the runtime execution interface.
2. Keep internal backend model choice separate from provider-visible response model policy.
3. Make the backend selectable by runtime profile only.

#### Acceptance Criteria

- two listeners for the same provider can return `lorem` and `faker` respectively
- provider request contracts and SSE framing remain unchanged

#### Likely Files

- `internal/runtime/*.go`
- `internal/response/*.go` or a new generator package
- provider handlers as needed

### Slice 7: Reconcile With The Existing Static Startup Path

#### Objective

Define the long-term relationship between the new local runtime mode and the existing YAML/Host-routed mode.

#### Deliverables

- explicit startup-mode boundary
- preserved compatibility or a documented migration path

#### Work Items

1. Decide whether the current Host-routed static mode remains supported.
2. If yes, keep it behind a separate startup mode.
3. If no, document the migration and update tests accordingly.

#### Acceptance Criteria

- startup behavior is explicit and understandable
- tests clearly reflect the supported modes

#### Likely Files

- `cmd/zolem/main.go`
- `cmd/zolem/startup.go`
- current startup tests under `cmd/zolem`

## Recommended Execution Order

1. Slice 1
2. Slice 2
3. Slice 3
4. Slice 4
5. Slice 5
6. Slice 6
7. Slice 7

## Test Strategy

- Unit tests for runtime profile validation, listener lifecycle, and context plumbing
- HTTP tests for `/_zolem/state`, `/_zolem/health`, and listener CRUD
- End-to-end tests that create profiles and listeners through the admin API and then call provider-compatible endpoints over the returned base URLs
- TLS tests that verify startup behavior with locally generated certs from `mkcert`

## Open Questions

- Whether the old YAML route config should coexist indefinitely with the local runtime mode
- Whether fixture filtering should be done before or inside WASM matching
- Whether the first generated backend should be `faker` or a model-backed passthrough stub
