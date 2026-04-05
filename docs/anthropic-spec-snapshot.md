# Anthropic Spec Snapshot

Zolem currently supports Anthropic request validation for:

- `POST /v1/messages`
- API version: `v1`

The normalized snapshot lives at [`internal/specs/vendored/anthropic-v1.json`](/tmp/zolem-phase4-discovery-ingestion/internal/specs/vendored/anthropic-v1.json) and is bundled through [`internal/specs/vendored.go`](/tmp/zolem-phase4-discovery-ingestion/internal/specs/vendored.go).

## Derivation Source

This snapshot is derived from Anthropic's public API docs for the Messages API:

- `https://docs.anthropic.com/en/api/messages`
- `https://docs.anthropic.com/en/api/overview`

The snapshot intentionally covers only the request fields Zolem currently implements:

- `model`
- `max_tokens`
- `messages`
- optional `system`
- optional `stream`

Message items are currently normalized as:

- `role`: `"user"` or `"assistant"`
- `content`: string

## Update Workflow

When Zolem expands Anthropic request support or Anthropic changes the documented request shape:

1. Re-read the official Messages API docs.
2. Update [`internal/specs/vendored/anthropic-v1.json`](/tmp/zolem-phase4-discovery-ingestion/internal/specs/vendored/anthropic-v1.json) to match the documented request fields Zolem actually supports.
3. Keep the snapshot normalized to JSON Schema draft 2020-12 so it can be compiled directly by the existing validator.
4. Run the targeted validation tests:
   - `env GOCACHE=/tmp/zolem-go-build-cache go test ./internal/specs -run 'TestVendoredFallbacks_AnthropicSnapshotValidatesMessagesRequests'`
   - `env GOCACHE=/tmp/zolem-go-build-cache go test ./internal/provider/anthropic -run 'TestMessages_ValidationFailure'`
   - `env GOCACHE=/tmp/zolem-go-build-cache go test ./cmd/zolem -run 'TestBuildStartupApp_LoadsVendoredAnthropicSnapshot'`

## Notes

- Zolem no longer depends on an Anthropic remote machine-readable URL at startup.
- If a cache file exists for `anthropic:v1`, the fetcher still prefers that cache entry. Otherwise it falls back to the bundled snapshot.
