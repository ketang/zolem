---
schema_version: 1
title: Inspect live listener state via zolemc
slug: zolemc-inspect-listener-state
status: draft
authority: observed
change_resistance: low
locked_sections: []
---

# Inspect live listener state via zolemc

## Intent
A developer checks whether a running mock listener is serving the expected provider and profile before running tests against it.

## Story
A developer wants to confirm what provider, profile, backend, and address a running fixed or admin-mode listener is currently serving. They run `zolemc -base-url <listener_url> listener state`. The listener replies with its current runtime configuration in a human-readable or JSON format.

## Expected Behavior
On success, zolemc prints 'provider=<p> profile=<q> backend=<b> listener=<addr> tls=<bool>' and exits 0. With -json it prints a JSON object with those same fields. If -base-url is not provided, zolemc exits with an error before making a request. If the listener is unreachable or returns an unexpected payload, zolemc exits with an error.

## Boundaries
-base-url must be provided; this command targets the listener's own endpoint, not the admin server. State reflects the listener's fixed runtime configuration at startup and does not change while the listener is alive. This command covers listener state only; health checks use the separate 'zolemc listener health' command.

## Auditable Claims
- GET /_zolem/state returns provider, profile, backend, listener, and tls fields
- zolemc exits error if -base-url is not supplied
- zolemc listener state prints all five fields in the human-readable output line

## Evidence

### Tests
- `cmd/zolemc/main_e2e_test.go`
- `cmd/zolem/local_runtime_e2e_test.go`

### Surface
- `cli: zolemc -base-url http://127.0.0.1:19001 listener state`

### Docs
- `README.md#quick-start-local-runtime-mode`
