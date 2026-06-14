---
schema_version: 1
title: List call history for a listener
slug: zolemc-call-history-list
status: draft
authority: observed
change_resistance: low
locked_sections: []
---

# List call history for a listener

## Intent
A developer verifies that a mock listener received the expected requests and returned the expected responses during a test run.

## Story
After sending requests to a mock listener, a developer wants to verify what the listener received. They run `zolemc listeners calls list <name>` to see a tabular summary of captured calls. Each row shows call ID, method, path, status code, latency, and timestamp. Streaming calls show a ~ prefix on the status code. A -since flag filters to calls after a given call_id.

## Expected Behavior
On success, zolemc prints a table of calls and exits 0. With -json it prints the raw API JSON {"calls": [...]}. If no calls have been recorded, it prints 'no calls'. If the listener does not exist, the admin API returns 404 and zolemc exits with an error. If no listener name is supplied, zolemc exits with a usage error. The -since flag is client-side filtering applied to the API response. Streaming responses show a ~ prefix on the status column. When -since and -json are both supplied, -since filtering is still applied client-side before the JSON output is produced.

## Boundaries
Call history is in-memory; it disappears when the listener or admin server stops. The -since filter is client-side only; it does not affect the API payload. Fixed-listener mode records calls to a JSONL file (not retrievable via this command). Only admin-mode in-memory listeners are queryable via this command. If the supplied -since call_id is higher than all recorded call IDs, the result is an empty list (0 calls shown), not an error.

## Auditable Claims
- zolemc listeners calls list exits 0 and prints a table of calls
- zolemc prints 'no calls' when the listener has no recorded calls
- admin API returns 404 if the listener is not found
- -since filters calls by call_id on the client side
- streaming calls are indicated by a ~ prefix on the status code in table output

## Evidence

### Tests
- `cmd/zolem/local_calls_e2e_test.go`
- `cmd/zolemc/main_e2e_test.go`

### Surface
- `cli: zolemc listeners calls list openai-demo`
- `cli: zolemc listeners calls list openai-demo -since 5`

### Docs
- `README.md`
- `docs/local-runtime.md`
