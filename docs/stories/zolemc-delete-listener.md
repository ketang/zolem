---
schema_version: 1
title: Delete a mock listener via zolemc
slug: zolemc-delete-listener
status: draft
authority: observed
change_resistance: low
locked_sections: []
---

# Delete a mock listener via zolemc

## Intent
zolemc stops and removes a named listener from the running admin server.

## Story
After testing, a developer wants to tear down a listener that is no longer needed. They run `zolemc listeners delete <name>`. The admin server closes the TCP listener and removes the entry.

## Expected Behavior
On success, zolemc prints 'listener <name> deleted' and exits 0. With -json it prints {"deleted": "<name>"}. If the listener does not exist, the admin API returns 404 and zolemc exits with an error. If no name is supplied, zolemc exits with a usage error. Deleting a listener does not delete the profile it was using.

## Boundaries
Exactly one listener name must be supplied. Deleting a listener frees the TCP port but does not affect the profile. There is no confirmation prompt.

## Auditable Claims
- zolemc listeners delete <name> exits 0 on success
- admin API returns 404 when listener is not found
- zolemc exits error if no name is supplied
- deleting a listener does not delete the profile it used

## Evidence

### Tests
- `cmd/zolemc/main_e2e_test.go`
- `cmd/zolem/local_admin_test.go`

### Surface
- `cli: zolemc listeners delete openai-demo`

### Docs
- `README.md`
