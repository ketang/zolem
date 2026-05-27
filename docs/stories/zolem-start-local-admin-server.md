---
schema_version: 1
title: Start zolem local admin server
slug: zolem-start-local-admin-server
status: draft
authority: observed
change_resistance: low
locked_sections: []
---

# Start zolem local admin server

## Intent
A developer can create and reconfigure mock listeners during a test session without restarting zolem.

## Story
A developer wants to create and reconfigure multiple mock listeners during a test session without restarting the server. They run zolem with -local-admin-addr to start the admin control plane. From there, clients can create profiles and listeners, switch backends, inspect state, and clean up—all via HTTP calls to the admin API while the server remains running.

## Expected Behavior
When -local-admin-addr is set to a loopback address, zolem starts the admin HTTP server and logs the address. The admin API serves GET /_zolem/health (returns {status: ok}), GET /_zolem/profiles, and GET /_zolem/listeners. Profiles and listeners created via the API are held in memory. If -local-admin-addr is a non-loopback address, zolem exits with an error. If neither -local-admin-addr nor -local-provider is set, zolem exits with log.Fatal. If both flags are supplied together, zolem uses admin mode (the -local-admin-addr path) and ignores -local-provider. TLS is optional: both -local-tls-cert and -local-tls-key must be supplied together; supplying only one is a startup error.

## Boundaries
Only loopback addresses are accepted for -local-admin-addr. All profiles and listeners are in-memory and are lost on process exit. A single zolem process runs either admin mode or fixed-listener mode; when both -local-admin-addr and -local-provider are supplied, admin mode takes precedence and -local-provider is ignored. TLS requires both cert and key files. If the chosen port is already in use, the OS returns a bind error and zolem exits with a network error.

## Auditable Claims
- zolem exits with log.Fatal if neither -local-admin-addr nor -local-provider is set
- zolem rejects a non-loopback -local-admin-addr with an error
- GET /_zolem/health returns {status: ok}
- GET /_zolem/profiles returns a JSON array of profiles
- GET /_zolem/listeners returns a JSON array of listeners
- TLS requires both cert and key; one alone is a startup error
- supplying both -local-admin-addr and -local-provider uses admin mode; -local-provider is ignored

## Evidence

### Tests
- `cmd/zolem/local_admin_test.go`
- `cmd/zolem/main_e2e_test.go`
- `cmd/zolem/tls_listener_e2e_test.go`

### Surface
- `cli: zolem -local-admin-addr 127.0.0.1:18090`

### Docs
- `README.md#quick-start-local-runtime-mode`

## Drift Notes
- No focused test covering the combined -local-admin-addr plus -local-provider invocation was identified during this update; the precedence claim is currently supported by cmd/zolem/main.go branch order.
