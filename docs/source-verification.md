# Source Verification

This suite detects contract drift: it fails loudly when Zolem's parser assumptions stop matching its source inputs. Those inputs are the embedded Anthropic snapshot (`specs.VendoredFallbacks()["anthropic:v1"]`, checked by `TestSourceVerification_AnthropicV1SnapshotInvariants`) and the Gemini discovery fixtures in `testdata/specs/` (`gemini-discovery-v1.json`, `gemini-discovery-v1beta.json`, read via `readDiscoveryFixture` in `internal/specs/discovery_test.go`).

## What It Verifies

- Anthropic `v1`
  - uses a vendored normalized snapshot instead of a remote URL
  - still requires `model`, `max_tokens`, and `messages`
  - still constrains message roles to the request shape Zolem currently supports
- Gemini `v1` and `v1beta`
  - discovery fixtures still contain `models.generateContent`
  - discovery fixtures still contain `models.streamGenerateContent`
  - both methods still resolve to the same request schema target
  - extracted schemas still validate representative request bodies

## How To Run

Run the source verification suite directly:

```bash
go test ./internal/specs -run 'TestSourceVerification_'
go test ./cmd/zolem -run 'TestSpecValidation_'
```

## When To Run It

Run this suite before landing work that changes:

- source URLs
- source parsers
- vendored fallback snapshots
- schema normalization (`internal/specs/openapi.go`, `discovery.go`)
