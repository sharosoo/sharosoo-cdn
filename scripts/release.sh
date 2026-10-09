#!/usr/bin/env bash
# Build static sharosoo-cdn binaries for every target and publish them on cdn.sharosoo.com.
# Usage: scripts/release.sh <version>   e.g. scripts/release.sh 0.3.0
#
# Layout: tools/sharosoo-cdn/v<version>/sharosoo-cdn-<os>-<arch>[.exe] + SHA256SUMS (immutable),
#         tools/sharosoo-cdn/latest and tools/sharosoo-cdn/install.sh (short cache, overwritten each release).
set -euo pipefail

version="${1:?usage: scripts/release.sh <version>}"
version="${version#v}"
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

[ -z "$(git status --porcelain)" ] || { echo "working tree not clean" >&2; exit 1; }
git rev-parse -q --verify "refs/tags/v$version" >/dev/null && { echo "tag v$version exists" >&2; exit 1; }

go vet ./...
go test ./...

out="dist/v$version"
rm -rf dist && mkdir -p "$out"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os="${target%/*}" arch="${target#*/}"
  ext=""; [ "$os" = windows ] && ext=".exe"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags "-s -w -X main.version=$version" -o "$out/sharosoo-cdn-$os-$arch$ext" .
done
if command -v sha256sum >/dev/null 2>&1; then sum="sha256sum"; else sum="shasum -a 256"; fi
(cd "$out" && $sum sharosoo-cdn-* > SHA256SUMS)
printf '%s\n' "$version" > dist/latest

# Publish with the binary just built for this machine.
self="$out/sharosoo-cdn-$(go env GOOS)-$(go env GOARCH)"
"$self" put "$out" --prefix "tools/sharosoo-cdn/v$version" -v
"$self" put dist/latest scripts/install.sh --prefix tools/sharosoo-cdn --force --cache-control "public, max-age=60" -v

git tag -a "v$version" -m "sharosoo-cdn v$version"
git push origin "v$version"
echo "released v$version: curl -fsSL https://cdn.sharosoo.com/tools/sharosoo-cdn/install.sh | sh"
