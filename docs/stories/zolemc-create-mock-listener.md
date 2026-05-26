---
schema_version: 1
title: Create a mock listener via zolemc
slug: zolemc-create-mock-listener
status: draft
authority: observed
change_resistance: low
locked_sections: []
---

# Create a mock listener via zolemc

## Intent
A developer can run AI SDK code locally without real provider API calls by pointing the SDK at a zolem mock endpoint.

## Story
After creating a profile, a developer wants to expose a local URL that mimics a specific provider's API. They run `zolemc listeners create <name> -provider openai -profile demo`. The admin server starts a loopback listener, validates the spec, and returns a base_url. The developer then points their SDK or test client at that URL.

## Expected Behavior
On success, zolemc prints 'listener <name> created: <base_url>' and exits 0. With -json it prints the listener JSON including base_url. If the profile does not exist, the admin API returns 404 and zolemc exits with an error. If provider is not anthropic, openai, or gemini, the admin API returns 400. If the address is not a loopback, the admin API returns 400. If -addr is omitted it defaults to 127.0.0.1:0 (OS-assigned port). Spec-load warnings are returned in the X-Zolem-Warnings response header and printed by zolemc to stderr. If a listener with the same name already exists, it is replaced (upsert semantics).

## Boundaries
Only loopback addresses are accepted. Provider must be one of anthropic, openai, or gemini. Profile must already exist. Listener names must be non-empty and must not contain slash characters. Listeners are in-memory and disappear when the admin server stops. Creating with an existing name replaces the listener. If the admin server is unreachable, zolemc exits with a network error before the listener is created.

## Auditable Claims
- zolemc listeners create exits 0 and prints base_url on success
- admin API returns 404 if referenced profile does not exist
- admin API returns 400 if provider is not anthropic/openai/gemini
- admin API returns 400 for non-loopback addr
- -addr defaults to 127.0.0.1:0 (OS-assigned port)
- creating a listener with an existing name replaces it (upsert)
- spec-load warnings are printed to stderr by zolemc

## Evidence

### Tests
- `cmd/zolemc/main_e2e_test.go`
- `cmd/zolem/local_admin_test.go`
- `cmd/zolem/local_runtime_e2e_test.go`

### Surface
- `cli: zolemc listeners create openai-demo -provider openai -profile demo`

### Docs
- `README.md#quick-start-local-runtime-mode`
