#!/bin/sh
# Builds the Go binaries twice from different directories and compares them byte for byte.
# A reproducible build lets anyone confirm a release really comes from the published source.
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
VERSION=${VERSION:-repro-check}
A=$(mktemp -d); B=$(mktemp -d); CACHE=$(mktemp -d)
trap 'chmod -R u+w "$CACHE" 2>/dev/null; rm -rf "$A" "$B" "$CACHE"' EXIT
build() {
  out=$1
  cd "$ROOT"
  CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags "-s -w -buildid= -X github.com/anand34577/gorget/internal/core.Version=$VERSION" \
    -o "$out/gorget-server" ./cmd/gorget-server
  CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags "-s -w -buildid= -X github.com/anand34577/gorget/client.Version=$VERSION" \
    -o "$out/gorget" ./cmd/gorget
}
# Different output directories and a different build cache prove the result does not depend on them.
build "$A"
GOCACHE=$CACHE; export GOCACHE
build "$B"
for f in gorget-server gorget; do
  if cmp -s "$A/$f" "$B/$f"; then echo "reproducible: $f  $(sha256sum "$A/$f" | cut -d" " -f1)"; else echo "NOT reproducible: $f"; exit 1; fi
done
