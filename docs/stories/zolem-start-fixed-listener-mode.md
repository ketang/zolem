---
schema_version: 1
title: Start zolem in fixed-listener mode
slug: zolem-start-fixed-listener-mode
status: draft
authority: observed
change_resistance: low
locked_sections: []
---

# Start zolem in fixed-listener mode

## Intent
A developer gets a local mock endpoint for any supported LLM provider without config files or a separate control-plane process.

## Story
A developer wants to point an AI SDK or test harness at a local mock without writing config files or running a second control-plane process. They run zolem with -local-provider, -local-addr, and optionally -local-backend to start a fixed listener. All requests to that loopback address are handled by the chosen backend and validated against the provider's OpenAPI spec. The listener exits when the process stops.

## Expected Behavior
When -local-provider is set to anthropic, openai, or gemini, zolem starts one loopback listener and logs the address. Requests matching the provider's API are validated and synthetic responses returned. If -local-provider is omitted or invalid, zolem exits with a fatal error. If -local-addr is omitted it defaults to 127.0.0.1:8080. If -local-backend is omitted it defaults to lorem. Specifying -local-backend fixture also requires -local-fixtures-dir; omitting it is a startup error. Specifying only one of -local-tls-cert / -local-tls-key (without the other) is a startup error.

## Boundaries
Only loopback addresses are accepted; non-loopback addresses are rejected. Only anthropic, openai, and gemini are supported providers. The fixture backend requires -local-fixtures-dir; omitting it while backend=fixture is a startup error. The listener is in-memory and disappears on process exit. TLS requires both cert and key files.

## Auditable Claims
- zolem exits with log.Fatal if -local-provider is empty or not one of anthropic/openai/gemini
- zolem defaults -local-addr to 127.0.0.1:8080 when unset
- zolem defaults -local-backend to lorem when unset
- TLS requires both -local-tls-cert and -local-tls-key; supplying only one is an error
- fixture backend without -local-fixtures-dir returns a startup error

## Evidence

### Tests
- `cmd/zolem/local_runtime_e2e_test.go`
- `cmd/zolem/main_e2e_test.go`
- `cmd/zolem/startup_local_test.go`

### Surface
- `cli: zolem -local-provider anthropic -local-addr 127.0.0.1:18080 -local-backend lorem`

### Docs
- `README.md#quick-start-fixed-listener-mode`
