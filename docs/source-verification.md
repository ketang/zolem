# Source Verification

Phase 6 adds a dedicated source-verification suite for contract drift detection.

Its purpose is to fail loudly when Zolem's parser assumptions stop matching the configured source artifacts.

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
env GOCACHE=/tmp/zolem-go-build-cache go test ./internal/specs -run 'TestSourceVerification_'
env GOCACHE=/tmp/zolem-go-build-cache go test ./cmd/zolem -run 'TestSpecSourceMap_CanonicalSourceInvariants'
```

## When To Run It

Run this suite before landing work that changes:

- source URLs
- source parsers
- vendored fallback snapshots
- refresh-loop behavior

It is intended to be the preflight check for the later refresh-loop phase, where upstream fetch success and parser correctness become runtime concerns.
