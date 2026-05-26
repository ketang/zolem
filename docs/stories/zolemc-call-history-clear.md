---
schema_version: 1
title: Clear call history for a listener
slug: zolemc-call-history-clear
status: draft
authority: observed
change_resistance: low
locked_sections: []
---

# Clear call history for a listener

## Intent
A developer wipes the captured call log for a listener so the next test run starts from a clean slate.

## Story
Between test runs, a developer wants to flush the captured call log for a listener so the next run starts from a clean slate. They run `zolemc listeners calls clear <name>`. The admin server drops all stored calls.

## Expected Behavior
On success, zolemc prints 'Cleared N calls from listener <name>.' and exits 0. With -json it prints {"cleared": N}. If the listener does not exist, the admin API returns 404 and zolemc exits with an error. If the listener has no recorded calls, the response reports 0 cleared and exits 0. If no name is supplied, zolemc exits with a usage error.

## Boundaries
Clearing drops calls permanently within the session; there is no undo. Fixed-listener JSONL recorders cannot be cleared via this command. Only one listener name may be supplied per invocation.

## Auditable Claims
- zolemc listeners calls clear exits 0 and prints the cleared count
- clearing when no calls exist returns 0 and exits 0
- admin API returns 404 if the listener is not found
- zolemc exits error if no name is supplied

## Evidence

### Tests
- `cmd/zolem/local_calls_e2e_test.go`
- `cmd/zolemc/main_e2e_test.go`

### Surface
- `cli: zolemc listeners calls clear openai-demo`

### Docs
- `README.md`
