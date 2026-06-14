#!/usr/bin/env bash
set -euo pipefail

CERT_DIR="${CERT_DIR:-certs}"

if ! command -v mkcert &>/dev/null; then
  echo "mkcert not found. Install it first:"
  echo "  https://github.com/FiloSottile/mkcert#installation"
  exit 1
fi

# Install the local CA into system/browser trust stores (idempotent).
mkcert -install

mkdir -p "$CERT_DIR"

# Generate a single cert covering localhost, IPv4 loopback, and IPv6 loopback.
mkcert -cert-file "$CERT_DIR/localhost.pem" \
       -key-file  "$CERT_DIR/localhost-key.pem" \
       localhost 127.0.0.1 ::1

echo
echo "Certificates written to $CERT_DIR/"
echo "  cert: $CERT_DIR/localhost.pem"
echo "  key:  $CERT_DIR/localhost-key.pem"
echo
echo "Use with local runtime TLS flags:"
echo "  zolem \\"
echo "    -local-admin-addr 127.0.0.1:18443 \\"
echo "    -local-tls-cert $CERT_DIR/localhost.pem \\"
echo "    -local-tls-key $CERT_DIR/localhost-key.pem"
