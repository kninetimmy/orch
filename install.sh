#!/bin/sh
# install.sh — download the released orch binary for this OS and
# architecture from GitHub Releases, verify its SHA-256 against the
# release's published SHA256SUMS, and install it. Fails closed: nothing
# lands on PATH unless the checksum matches.
#
# Environment:
#   ORCH_VERSION      release tag to install (default: latest), e.g. v0.1.0
#   ORCH_INSTALL_DIR  install directory (default: $HOME/.local/bin)
#
# Exit codes: 0 ok, 2 unsupported platform or bad ORCH_VERSION,
# 3 download/tooling failure, 4 checksum failure, 5 install failure.

set -eu

repo="kninetimmy/orch"

fail() {
  code="$1"
  shift
  echo "install.sh: $*" >&2
  exit "$code"
}

version="${ORCH_VERSION:-latest}"
install_dir="${ORCH_INSTALL_DIR:-$HOME/.local/bin}"

# Validate the version before it reaches a URL: a leading v, then only
# tag charset. Anything else fails closed.
if [ "$version" != "latest" ]; then
  case "$version" in
    v[0-9]*) ;;
    *) fail 2 "ORCH_VERSION must be a release tag like v0.1.0 (got: $version)" ;;
  esac
  case "$version" in
    *[!0-9A-Za-z.+-]*) fail 2 "ORCH_VERSION contains characters outside the tag charset (got: $version)" ;;
  esac
fi

# OS/arch resolve through closed tables to fixed literals only; raw
# uname output is never interpolated into a URL or path.
case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  MINGW* | MSYS* | CYGWIN*) fail 2 "this is Windows: use install.ps1 instead" ;;
  *) fail 2 "unsupported OS: $(uname -s) (releases cover linux and darwin; on Windows use install.ps1)" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail 2 "unsupported architecture: $(uname -m) (releases cover amd64 and arm64)" ;;
esac

asset="orch_${os}_${arch}"
if [ "$version" = "latest" ]; then
  base="https://github.com/$repo/releases/latest/download"
else
  base="https://github.com/$repo/releases/download/$version"
fi

tmp="$(mktemp -d)" || fail 3 "mktemp failed"
trap 'rm -rf "$tmp"' EXIT

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  fail 3 "neither curl nor wget found; install one and re-run"
fi

fetch "$base/$asset" "$tmp/$asset" || fail 3 "download failed: $base/$asset"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || fail 3 "download failed: $base/SHA256SUMS"

# Verify before anything touches the install dir. No checksum tool is a
# failure, never a skip.
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
else
  fail 4 "neither sha256sum nor shasum found; refusing to install an unverified binary"
fi

expected="$(awk -v name="$asset" '$2 == name { print $1 }' "$tmp/SHA256SUMS")"
[ -n "$expected" ] || fail 4 "SHA256SUMS has no entry for $asset"
if [ "$actual" != "$expected" ]; then
  fail 4 "checksum mismatch for $asset: expected $expected, got $actual — refusing to install"
fi

mkdir -p "$install_dir" || fail 5 "cannot create $install_dir (set ORCH_INSTALL_DIR to a writable directory)"
chmod 0755 "$tmp/$asset"
mv -f "$tmp/$asset" "$install_dir/orch" || fail 5 "cannot write $install_dir/orch"

echo "installed: $install_dir/orch"
"$install_dir/orch" status 2>/dev/null | head -n 1 || true

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    echo ""
    echo "$install_dir is not on your PATH; add it, e.g.:"
    echo "  export PATH=\"$install_dir:\$PATH\""
    ;;
esac

echo "next: run 'orch doctor'"
