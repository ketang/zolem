#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export GOCACHE="${GOCACHE:-/tmp/zolem-local-runtime-go-cache}"
mkdir -p "$GOCACHE"

echo "Using repo root: $ROOT"
echo "Using GOCACHE: $GOCACHE"

echo
echo "==> Running full cmd/zolem test suite"
go test ./cmd/zolem

echo
echo "==> Running provider/response/runtime package tests"
go test ./internal/provider/... ./internal/response/... ./internal/runtime/...

echo
echo "Slice 1 local runtime verification passed."
