#!/usr/bin/env bash
# Precompress public text at build time; no request-time gzip CPU work.
set -euo pipefail
root="${1:-server/webdist}"
command -v gzip >/dev/null
write_compressed() (
  dest="$1"; shift
  temp=$(mktemp "$dest.tmp.XXXXXX")
  trap 'rm -f -- "$temp"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  "$@" > "$temp"
  mv -f -- "$temp" "$dest"
)
find "$root" -type f \( -name '*.js' -o -name '*.css' -o -name '*.svg' \) -print0 |
while IFS= read -r -d '' file; do
  if [ ! -f "$file.gz" ] || [ "$file" -nt "$file.gz" ]; then write_compressed "$file.gz" gzip -9 -n -c "$file"; fi
  if command -v zstd >/dev/null 2>&1 && { [ ! -f "$file.zst" ] || [ "$file" -nt "$file.zst" ]; }; then
    write_compressed "$file.zst" zstd -19 --quiet --stdout "$file"
  fi
done
