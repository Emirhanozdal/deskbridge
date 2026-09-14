#!/usr/bin/env bash
set -euo pipefail

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT
if ! curl -fsSL --retry 5 --connect-timeout 15 --max-time 90 https://emirhanozdal.github.io/deskbridge/i -o "$tmpdir/install.sh"; then
  curl -fsSL --retry 5 --connect-timeout 15 --max-time 90 https://raw.githubusercontent.com/Emirhanozdal/deskbridge/main/docs/i -o "$tmpdir/install.sh"
fi
bash "$tmpdir/install.sh"
