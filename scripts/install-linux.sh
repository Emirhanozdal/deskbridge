#!/usr/bin/env bash
set -euo pipefail

repo="${DESKBRIDGE_REPO:-Emirhanozdal/deskbridge}"
version="${DESKBRIDGE_VERSION:-v0.1.2}"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

if ! command -v gh >/dev/null 2>&1; then
  echo "gh is required for the private DeskBridge release. Install GitHub CLI and run: gh auth login" >&2
  exit 1
fi

gh release download "$version" \
  --repo "$repo" \
  --pattern 'deskbridge_*.deb' \
  --dir "$tmpdir"

sudo apt install "$tmpdir"/deskbridge_*.deb
