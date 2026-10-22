#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
[[ $# -eq 0 ]] || { echo 'build.sh accepts no deployment target; output stays in this project.' >&2; exit 2; }
python3 "$root/scripts/project_guard.py" "$root"
command -v zstd >/dev/null || { echo "Install zstd before building." >&2; exit 1; }
(cd "$root/v2/web" && npm ci --no-audit --no-fund && npm run build)
python3 - "$root" <<'PY'
from pathlib import Path
import shutil,sys
r=Path(sys.argv[1]); target=r/'v2/server/webdist'
if target.exists(): shutil.rmtree(target)
shutil.copytree(r/'v2/web/dist', target)
(target/'README.txt').write_text('Build the frontend and embed it with bash scripts/build.sh from the repository root.\n')
PY
bash "$root/v2/scripts/precompress-webdist.sh" "$root/v2/server/webdist"
mkdir -p "$root/.local/bin"
(cd "$root/v2/server" && go build -trimpath -o "$root/.local/bin/m-brand" ./cmd/0brand)
echo 'Built .local/bin/m-brand; no service was restarted.'
