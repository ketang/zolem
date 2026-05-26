---
schema_version: 1
title: Delete a response profile via zolemc
slug: zolemc-delete-profile
status: draft
authority: observed
change_resistance: low
locked_sections: []
---

# Delete a response profile via zolemc

## Intent
zolemc removes a named profile from the running admin server.

## Story
After a test session, a developer wants to clean up a profile that is no longer needed. They run `zolemc profiles delete <name>`. If no listener is currently using the profile, the admin server removes it immediately.

## Expected Behavior
On success, zolemc prints 'profile <name> deleted' and exits 0. With -json it prints {"deleted": "<name>"}. If the profile does not exist, the admin API returns 404 and zolemc exits with an error. If a listener is still using the profile, the admin API returns 409 (profile in use) and zolemc exits with an error. If no name is supplied, zolemc exits with a usage error before contacting the server.

## Boundaries
Exactly one profile name must be supplied. Profiles cannot be deleted while a listener holds a reference to them. Deletion is permanent within the session; there is no undo.

## Auditable Claims
- zolemc profiles delete <name> exits 0 on success
- admin API returns 404 when profile is not found
- admin API returns 409 (ErrProfileInUse) when a listener references the profile
- zolemc exits error if no name is supplied

## Evidence

### Tests
- `cmd/zolemc/main_e2e_test.go`
- `cmd/zolem/local_admin_test.go`

### Surface
- `cli: zolemc profiles delete demo`

### Docs
- `README.md`
