# Anthropic Spec Snapshot

Zolem currently supports Anthropic request validation for:

- `POST /v1/messages`
- API version: `v1`

The normalized snapshot lives at
[`internal/specs/vendored/anthropic-v1.json`](../internal/specs/vendored/anthropic-v1.json)
and is bundled through
[`internal/specs/vendored.go`](../internal/specs/vendored.go).

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
- `content`: a string, or an array of content blocks (`$defs/message_content_block`: text, image, document, tool_use, tool_result, thinking, redacted_thinking)

## Update Workflow

When Zolem expands Anthropic request support or Anthropic changes the documented request shape:

1. Re-read the official Messages API docs.
2. Update
   [`internal/specs/vendored/anthropic-v1.json`](../internal/specs/vendored/anthropic-v1.json)
   to match the documented request fields Zolem actually supports.
3. Keep the snapshot normalized to JSON Schema draft 2020-12 so it can be compiled directly by the existing validator.
4. Run the targeted validation tests:
   - `go test ./internal/specs -run 'TestVendoredFallbacks_AnthropicSnapshotValidatesMessagesRequests'`
   - `go test ./internal/specs -run 'TestSourceVerification_AnthropicV1SnapshotInvariants'`
   - `go test ./internal/provider/anthropic -run 'TestMessages_ValidationFailure'`
   - `go test ./cmd/zolem -run 'TestSpecValidation_'`

## Notes

- Zolem no longer depends on an Anthropic remote machine-readable URL at startup.
- At startup Zolem reads `$TMPDIR/zolem-specs/anthropic-v1.json` if that file exists and otherwise uses the embedded snapshot (`internal/specs/fetcher.go`: disk cache precedes the embedded fallback). Delete that file to force the snapshot. If loading fails, Zolem logs a warning and serves without validation for that schema; `/_zolem/state` `schemas_loaded` shows what loaded. Here `$TMPDIR` is `os.TempDir()`, per `cmd/zolem/startup.go`.
