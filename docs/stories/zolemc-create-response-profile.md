---
schema_version: 1
title: Create a response profile via zolemc
slug: zolemc-create-response-profile
status: draft
authority: observed
change_resistance: low
locked_sections: []
---

# Create a response profile via zolemc

## Intent
zolemc creates a named response profile in the running admin server with a chosen backend.

## Story
A developer wants to define mock behavior (lorem, faker, fixture, ollama, wasm, or error) before wiring a listener to it. They run `zolemc profiles create <name> [-backend ...]` against a running admin server. The profile is stored in memory and can later be assigned to one or more listeners.

## Expected Behavior

On success, zolemc prints 'profile <name> created' and exits 0. With -json it prints the profile JSON. If -backend is omitted it defaults to lorem. If the backend value is not one of lorem, faker, fixture, ollama, wasm, or error, the admin API returns 400 and zolemc exits with an error. If -wasm-module-file is set without -backend, it implies -backend wasm automatically. If -wasm-module-file is paired with an explicitly non-wasm -backend, zolemc exits with an error before making the request. If -wasm-timeout-ms is set without wasm backend (neither -backend wasm nor -wasm-module-file), zolemc exits with an error. If -response-model-policy is force_literal but -response-model is not set, the admin API returns 400. If a profile with the same name already exists it is replaced (upsert semantics). If the admin server is unreachable, zolemc exits with a network error.

## Boundaries
Profile names must be non-empty; the admin API enforces this. The wasm backend requires -wasm-module-file or a base64 payload. Only one profile name per create call. Profiles are in-memory; they disappear when the admin server stops. Creating with an existing name replaces the profile rather than returning an error.

## Auditable Claims

- zolemc profiles create <name> -backend lorem exits 0 and prints 'profile <name> created'
- zolemc profiles create without a name exits with an error
- zolemc with -wasm-module-file and no -backend implies backend wasm and succeeds
- zolemc exits error before API call if -wasm-module-file is paired with an explicitly non-wasm -backend
- zolemc exits error before API call if -wasm-timeout-ms set without wasm backend (neither -backend wasm nor -wasm-module-file)
- admin API returns 400 for unknown backend value
- admin API returns 400 when response_model_policy=force_literal without response_model
- creating with an existing profile name replaces it (upsert)

## Evidence

### Tests
- `cmd/zolemc/main_e2e_test.go`
- `cmd/zolem/local_admin_test.go`

### Surface
- `cli: zolemc profiles create demo -backend lorem`

### Docs
- `README.md#quick-start-local-runtime-mode`
