#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
(cd "$root/v2/web" && npm test)
bash "$root/v2/scripts/test.sh" "$@"
python3 -m unittest discover -s "$root/v2/scripts" -p 'test_*.py'
python3 -m unittest discover -s "$root/scripts" -p 'test_*.py'
bash "$root/v2/scripts/arch-check.sh"
