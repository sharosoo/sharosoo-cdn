#!/bin/sh
# Install the cdn CLI from cdn.sharosoo.com.
#   curl -fsSL https://cdn.sharosoo.com/tools/cdn/install.sh | sh
# Env: CDN_VERSION (default: latest), CDN_INSTALL_DIR (default: ~/.local/bin).
set -eu

base="https://cdn.sharosoo.com/tools/cdn"
dir="${CDN_INSTALL_DIR:-$HOME/.local/bin}"

fetch() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL -A sharosoo-cdn-install "$1"
  else wget -qO- -U sharosoo-cdn-install "$1"; fi
}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "unsupported OS $(uname -s); download a binary from $base/v<version>/" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

version="${CDN_VERSION:-$(fetch "$base/latest")}"
version="${version#v}"
name="cdn-$os-$arch"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fetch "$base/v$version/$name" > "$tmp/$name"
fetch "$base/v$version/SHA256SUMS" > "$tmp/SHA256SUMS"
(
  cd "$tmp"
  grep " $name\$" SHA256SUMS > want
  if command -v sha256sum >/dev/null 2>&1; then sha256sum -c want >/dev/null
  else shasum -a 256 -c want >/dev/null; fi
) || { echo "checksum mismatch for $name" >&2; exit 1; }

mkdir -p "$dir"
install -m 0755 "$tmp/$name" "$dir/cdn"
echo "installed cdn $version to $dir/cdn"
case ":$PATH:" in *":$dir:"*) ;; *) echo "note: $dir is not on PATH" >&2 ;; esac
