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
sha256sum -c --ignore-missing checksums.txt
```

`checksums.txt` lists every archive and SBOM in the release; `--ignore-missing` skips the ones you did not download.

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

`checksums.txt` has its own signature, `checksums.txt.bundle`, which you can
verify the same way (substituting `checksums.txt` for the archive).

### Inspect the SBOM

Requires [syft](https://github.com/anchore/syft).

```bash
syft zolem-<version>-<os>-<arch>.tar.gz.sbom
```

---

## Option 2 — Docker

```bash
docker pull ghcr.io/ketang/zolem:0.1.0      # pinned release (no v prefix)
docker pull ghcr.io/ketang/zolem:latest     # latest stable release
```

Available platforms: `linux/amd64`, `linux/arm64`.

The image is based on `gcr.io/distroless/static:nonroot` — no shell,
runs as a non-root user, static binary only.

Zolem binds loopback only by default, and inside a container loopback is
unreachable through `docker run -p`. Containers therefore opt in with
`-allow-non-loopback-bind`, which additionally accepts the wildcard hosts
`0.0.0.0` and `::` (specific non-loopback IPs stay rejected). It requires at
least one `-allowed-host` naming something other than `localhost` or a
loopback IP, and in control-plane mode also `-listener-port-range LOW-HIGH`.
The allowlist is additive: `localhost` and loopback IPs always pass the Host
check, and any other Host gets `403`.

**Security:** the admin API has no authentication, and the Host check only
blocks DNS rebinding; any client that can reach the port and sends
`Host: localhost` is accepted. Always publish with `-p 127.0.0.1:...` as in
the recipes below, never a bare `-p 18090:18090`, which exposes the port on
every host interface.

Basic control-plane mode server:

```bash
docker run --rm \
  -p 127.0.0.1:18090:18090 \
  -p 127.0.0.1:18100-18109:18100-18109 \
  ghcr.io/ketang/zolem:latest \
  -local-admin-addr 0.0.0.0:18090 \
  -allow-non-loopback-bind \
  -allowed-host zolem.test \
  -listener-port-range 18100-18109
```

Create listeners on `0.0.0.0:<port>` with a port inside the range. The
reported `base_url` uses `localhost`, so publish the listener ports with
identical host and container numbers (as above) for that URL to work from the
Docker host. With a port range set, clients must ask for an explicit in-range
port (for example `zolemc listeners create ... -addr 0.0.0.0:18100`), not the
default `127.0.0.1:0`. Add `-allowed-host <name>` for any other name clients use.

With a fixtures directory mounted:

```bash
docker run --rm \
  -p 127.0.0.1:18090:18090 -p 127.0.0.1:18100-18109:18100-18109 \
  -v "$PWD/fixtures:/fixtures" \
  ghcr.io/ketang/zolem:latest \
  -local-admin-addr 0.0.0.0:18090 \
  -allow-non-loopback-bind -allowed-host zolem.test \
  -listener-port-range 18100-18109 \
  -local-fixtures-dir /fixtures
```

With TLS certs mounted:

```bash
docker run --rm \
  -p 127.0.0.1:18443:18443 -p 127.0.0.1:18100-18109:18100-18109 \
  -v "$PWD/certs:/certs" \
  ghcr.io/ketang/zolem:latest \
  -local-admin-addr 0.0.0.0:18443 \
  -allow-non-loopback-bind -allowed-host zolem.test \
  -listener-port-range 18100-18109 \
  -local-tls-cert /certs/localhost.pem \
  -local-tls-key /certs/localhost-key.pem
```

Fixed-listener mode serves one provider on its single `-local-addr` port, which
you publish directly; it needs no port range:

```bash
docker run --rm -p 127.0.0.1:8080:8080 \
  ghcr.io/ketang/zolem:latest \
  -local-provider openai -local-addr 0.0.0.0:8080 \
  -allow-non-loopback-bind -allowed-host zolem.test
```

`zolemc` is not included in the image. Run it from the host against the
published port as shown in the quick-start examples in [README.md](README.md).

Image tags have no `v` prefix: `:0.1.0` (pinned release), `:latest` (latest
stable), `:nightly`.

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

Nightly builds run daily from the tip of `main`, only after the same
verification gate as CI passes, and are published as a pre-release under a
single moving, non-semver `nightly` tag. Go tooling ignores non-semver tags, so
moving it never affects module resolution.

```bash
gh release download nightly -R ketang/zolem
docker pull ghcr.io/ketang/zolem:nightly
go install github.com/ketang/zolem/cmd/zolem@main   # resolves to a pseudo-version
```

The release notes name the built commit and UTC date. Nightly binaries report
version `nightly`; use the release notes (or the image's
`org.opencontainers.image.revision` label) to identify the commit. The stable
`:latest` image is never updated by a nightly run.

Publication is staged: assets and images are uploaded and verified under
internal staging names first, then promoted. If a nightly download 404s, retry
in a few minutes. The `:nightly-candidate`, `:nightly-candidate-amd64`, and
`:nightly-candidate-arm64` image tags are internal staging tags that share
manifests with `:nightly`; do not use or delete them. They are overwritten by
the next run.

Nightly archives are signed by the nightly workflow, so verify them with its
certificate identity rather than the release one:

```bash
cosign verify-blob \
  --bundle zolem-nightly-<os>-<arch>.tar.gz.bundle \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity "https://github.com/ketang/zolem/.github/workflows/nightly.yml@refs/heads/main" \
  zolem-nightly-<os>-<arch>.tar.gz
```

Verify downloads with `sha256sum -c --ignore-missing checksums.txt`;
`checksums.txt` has its own `checksums.txt.bundle`, signed by the same
identity. The workflow signs every archive, every SBOM, and `checksums.txt`.

Nightly builds are not recommended for production use.

---

## Next steps

See [README.md](README.md) for quick-start examples and backends, and
[docs/](docs/README.md) for the full local runtime guides and flag reference.
