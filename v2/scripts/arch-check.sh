#!/usr/bin/env bash
# Dependency-direction guard. Extend as internal packages are extracted.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
# platform must stay infrastructure-free.
if grep -RIn --include='*.go' 'gin-gonic' "$root/server/internal/platform"; then
  echo "arch: platform must not import gin" >&2; exit 1
fi
# Domain and application services must not import transport.
for pkg in domain orders entitlements support analytics; do
  dir="$root/server/internal/$pkg"
  if [ -d "$dir" ] && grep -RIn --include='*.go' -E 'gin-gonic|net/http' "$dir"; then
    echo "arch: internal/$pkg must not import transport" >&2; exit 1
  fi
done
# Only cmd may be package main. Business code belongs to internal packages.
if grep -RIn --include='*.go' '^package main$' "$root/server" | grep -v '/cmd/' >/dev/null; then
  echo "arch: package main is only allowed under cmd/" >&2; exit 1
fi
echo "arch: ok"
