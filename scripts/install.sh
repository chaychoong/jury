#!/bin/sh
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
#
# Install the latest `jury` release binary for your platform:
#
#   curl -fsSL https://raw.githubusercontent.com/chaychoong/jury/main/scripts/install.sh | sh
#
# Environment overrides:
#   JURY_VERSION  tag to install (default: the latest release, e.g. v0.1.0)
#   BINDIR        install directory (default: $HOME/.local/bin)
set -eu

REPO="chaychoong/jury"
BINDIR="${BINDIR:-$HOME/.local/bin}"

# --- detect platform -------------------------------------------------------
os=$(uname -s)
case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "jury: unsupported OS '$os' — jury supports Linux and macOS only" >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "jury: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

# --- resolve version -------------------------------------------------------
version="${JURY_VERSION:-}"
if [ -z "$version" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
    sed -n 's/.*"tag_name" *: *"\([^"]*\)".*/\1/p' | head -n1)
fi
if [ -z "$version" ]; then
  echo "jury: could not determine the latest version (set JURY_VERSION to a tag like v0.1.0)" >&2
  exit 1
fi

# GoReleaser archive names use the version without the leading 'v'.
v_noprefix=${version#v}
asset="jury_${v_noprefix}_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/${version}/${asset}"

# --- download + install ----------------------------------------------------
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "jury: downloading $asset ($version)..."
if ! curl -fSL "$url" -o "$tmp/$asset"; then
  echo "jury: download failed: $url" >&2
  exit 1
fi

tar -xzf "$tmp/$asset" -C "$tmp"
mkdir -p "$BINDIR"
if ! install -m 0755 "$tmp/jury" "$BINDIR/jury" 2>/dev/null; then
  cp "$tmp/jury" "$BINDIR/jury"
  chmod 0755 "$BINDIR/jury"
fi

echo "jury: installed to $BINDIR/jury"
echo

case ":$PATH:" in
*":$BINDIR:"*) : ;;
*)
  echo "NOTE: $BINDIR is not on your PATH. Add it, e.g.:"
  echo "  export PATH=\"$BINDIR:\$PATH\""
  echo
  ;;
esac

echo "Next steps:"
echo "  1. Allow the binary in ~/.claude/settings.json:"
echo '       { "permissions": { "allow": ["Bash(jury:*)"] } }'
echo "  2. Seed the roster:  jury list"
echo "  3. Install the /jury plugin:"
echo "       /plugin marketplace add $REPO"
echo "       /plugin install jury@jury"
