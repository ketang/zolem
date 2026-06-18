# Installing Zolem

This document covers every supported way to get `zolem` (and optionally
`zolemc`) onto your machine. For usage and quick-start examples see
[README.md](README.md).

---

## Supported platforms

|  | linux/amd64 | linux/arm64 | darwin/arm64 |
|--|:-----------:|:-----------:|:------------:|
| Binary | ✓ | ✓ | ✓ |
| Docker | ✓ | ✓ | — |

---

## Option 1 — Pre-built binary (recommended)

### Download

Go to [github.com/ketang/zolem/releases/latest](https://github.com/ketang/zolem/releases/latest)
and download the archive for your platform:

| Platform | Archive |
|----------|---------|
| Linux amd64 | `zolem-<version>-linux-amd64.tar.gz` |
| Linux arm64 | `zolem-<version>-linux-arm64.tar.gz` |
| macOS arm64 | `zolem-<version>-darwin-arm64.tar.gz` |

Each release also ships:
- `checksums.txt` — SHA-256 for all archives
- `*.bundle` — cosign signature per archive
- `*.sbom` — CycloneDX SBOM per archive

### Verify the checksum

```bash
sha256sum -c checksums.txt
```

### Install

```bash
tar -xzf zolem-<version>-<os>-<arch>.tar.gz
sudo mv zolem zolemc /usr/local/bin/
```

### Verify the cosign signature

Requires [cosign](https://github.com/sigstore/cosign).

```bash
cosign verify-blob \
  --bundle zolem-<version>-<os>-<arch>.tar.gz.bundle \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp "https://github.com/ketang/zolem/.github/workflows/release.yml@refs/tags/.*" \
  zolem-<version>-<os>-<arch>.tar.gz
```

Exit code `0` means the signature is valid and traces to the release workflow.

### Inspect the SBOM

Requires [syft](https://github.com/anchore/syft).

```bash
syft zolem-<version>-<os>-<arch>.tar.gz.sbom
```

---

## Option 2 — Docker

```bash
docker pull ghcr.io/ketang/zolem:<version>  # pinned release
docker pull ghcr.io/ketang/zolem:latest     # latest stable release
```

Available platforms: `linux/amd64`, `linux/arm64`.

The image is based on `gcr.io/distroless/static:nonroot` — no shell,
runs as a non-root user, static binary only.

Basic local runtime server:

```bash
docker run --rm -p 18090:18090 \
  ghcr.io/ketang/zolem:latest \
  -local-admin-addr 0.0.0.0:18090
```

With a fixtures directory mounted:

```bash
docker run --rm -p 18090:18090 \
  -v "$PWD/fixtures:/fixtures" \
  ghcr.io/ketang/zolem:latest \
  -local-admin-addr 0.0.0.0:18090 \
  -local-fixtures-dir /fixtures
```

With TLS certs mounted:

```bash
docker run --rm -p 18443:18443 \
  -v "$PWD/certs:/certs" \
  ghcr.io/ketang/zolem:latest \
  -local-admin-addr 0.0.0.0:18443 \
  -local-tls-cert /certs/localhost.pem \
  -local-tls-key /certs/localhost-key.pem
```

`zolemc` is not included in the image. Run it from the host against the
published port as shown in the quick-start examples in [README.md](README.md).

Fixed-listener mode enforces loopback-only binding, so it cannot listen on
`0.0.0.0` inside a container. Use local runtime mode (the admin server, as
above) for containerized deployments, or run the binary directly on the host
for fixed-listener mode.

---

## Option 3 — From source

Requires Go 1.26 or later.

```bash
git clone https://github.com/ketang/zolem.git
cd zolem
go build -o zolem  ./cmd/zolem
go build -o zolemc ./cmd/zolemc
```

Move the binaries to your `PATH`:

```bash
sudo mv zolem zolemc /usr/local/bin/
```

Or install directly without cloning:

```bash
go install github.com/ketang/zolem/cmd/zolem@latest
go install github.com/ketang/zolem/cmd/zolemc@latest
```

---

## Nightly builds

Nightly builds run on a daily schedule from the tip of `main` and are
published as pre-releases on GitHub Releases (under the moving
`v0.0.0-nightly` tag). The multi-arch Docker image is tagged `:nightly` and
the stable `:latest` image is never updated by a nightly run.

```bash
docker pull ghcr.io/ketang/zolem:nightly
```

Nightly builds are not recommended for production use.

---

## Next steps

See [README.md](README.md) for quick-start examples and response modes, and
[docs/local-runtime.md](docs/local-runtime.md) for the full local runtime and
flag reference.
