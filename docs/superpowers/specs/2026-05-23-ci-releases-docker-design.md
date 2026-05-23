# CI, Releases, and Docker Design

**Date:** 2026-05-23  
**Status:** Approved

## Summary

Replace the existing hand-rolled release workflow with GoReleaser. Expand CI to run on pull requests and include the smoke test. Add a multi-arch Docker image published to ghcr.io. Cut the first stable release tag (`v0.1.0`) once the pipeline is verified.

## Decisions

| Topic | Decision |
|-------|----------|
| Build tool | GoReleaser |
| Binary platforms | linux/amd64, linux/arm64, darwin/arm64 |
| Docker registry | ghcr.io only |
| Docker base image | `gcr.io/distroless/static:nonroot` |
| Docker platforms | linux/amd64, linux/arm64 |
| Docker tags (release) | `vX.Y.Z`, `latest` |
| Docker tags (nightly) | `nightly` |
| Integrity artifacts | SHA256 checksums, CycloneDX SBOM, cosign keyless signatures |
| CI triggers | push to main + pull_request targeting main |
| CI smoke test | Yes — `./scripts/smoke.sh` runs in CI |
| First release tag | `v0.1.0`, cut after pipeline lands |

## GoReleaser Config (`.goreleaser.yaml`)

Single config at the repo root. Invoked two ways:

- **Tagged release** (`release.yml`, on `v*` tag): `goreleaser release --clean` — full mode, publishes to GitHub Releases, pushes Docker image, signs artifacts, generates SBOM.
- **Nightly** (`nightly.yml`, on push to main): `goreleaser release --snapshot --clean` — snapshot mode, version derived from `git describe`, publishes to the `nightly` prerelease, tags Docker image as `nightly`.

### Binary builds

All targets use `CGO_ENABLED=0`. Archives as `.tar.gz`. No Windows targets.

```
linux/amd64   → zolem-<version>-linux-amd64.tar.gz
linux/arm64   → zolem-<version>-linux-arm64.tar.gz
darwin/arm64  → zolem-<version>-darwin-arm64.tar.gz
```

### Artifacts per tagged release

- Three `.tar.gz` archives (one per platform)
- `checksums.txt` (SHA256 for all archives)
- CycloneDX SBOM per archive (via goreleaser's syft integration)
- cosign `.sig` + `.bundle` files for each archive
- Docker multi-arch image manifest (`vX.Y.Z` + `latest` tags on ghcr.io)
- cosign signature on the Docker image manifest

### Permissions required

Both `release.yml` and `nightly.yml` need:
```yaml
permissions:
  contents: write    # publish GitHub release assets
  packages: write    # push to ghcr.io
  id-token: write    # cosign keyless OIDC signing
```

## Docker Image

### Dockerfile

Two-stage build for local `docker build` use:

```
Stage 1 (builder): golang:1.26-bookworm
  - CGO_ENABLED=0 go build -o zolem ./cmd/zolem

Stage 2 (final): gcr.io/distroless/static:nonroot
  - COPY --from=builder zolem /zolem
  - ENTRYPOINT ["/zolem"]
```

In the goreleaser release path, goreleaser uses the pre-compiled binary and only the final stage is needed (via goreleaser's `dockerfile` field with `use: buildx`). The full two-stage Dockerfile supports `docker build .` locally.

### Multi-arch manifest

linux/amd64 and linux/arm64 only. darwin/arm64 is a release binary only — Docker on macOS runs linux images.

### Tag scheme

| Event | Tags applied |
|-------|-------------|
| Tagged release (`v*`) | `vX.Y.Z`, `latest` |
| Push to main | `nightly` |

### Cosign

Image manifest is signed with cosign keyless using GitHub Actions OIDC. No secrets required. Signature is stored as a referrer in ghcr.io alongside the image.

## CI Workflow (`ci.yml`)

### Triggers

```yaml
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
```

### Job steps

1. Check out repository
2. Set up Go (from `go.mod`)
3. `go test ./...`
4. `go build ./cmd/zolem`
5. `./scripts/smoke.sh`

Single runner (`ubuntu-latest`). No matrix — cross-compilation correctness is validated by the release path, not CI.

## Cutting v0.1.0

After workflow changes land on main:

1. Tag `v0.1.0` on main.
2. `release.yml` fires automatically.
3. Verify the GitHub Release has all three archives, `checksums.txt`, SBOM files, cosign signatures, and that the Docker image is available at `ghcr.io/ketang/zolem:v0.1.0` and `ghcr.io/ketang/zolem:latest`.

No changes to zolem source code required for the tag.

## Out of Scope

- Homebrew tap, Scoop bucket, aqua registry entry, npm/PyPI packages
- Windows binaries
- Intel Mac (darwin/amd64) binaries
- Docker Hub
- KMS-backed signing, TUF roots, SLSA Level 3+
- Shatter CI wiring (separate concern)
