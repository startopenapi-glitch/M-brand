#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
[[ -x "$root/.local/bin/m-brand" ]] || { echo 'Run bash scripts/build.sh first.' >&2; exit 1; }
[[ -f "$root/v2/.env" ]] || { echo 'Copy v2/.env.example to v2/.env and configure it first.' >&2; exit 1; }
set -a
source "$root/v2/.env"
set +a
[[ -n "${ZEROBRAND_PG_URL:-}" && -n "${ZEROBRAND_ADMIN_PASS:-}" ]] || { echo 'Database URL and initial admin password are required.' >&2; exit 1; }
if [[ -n "${ZEROBRAND_SMTP_FROM:-}${RESEND_FROM:-}${SMTP_FROM:-}" ]]; then
 python3 "$root/v2/scripts/mail_config_guard.py" "$root/v2/.env"
fi
mkdir -p "$root/.local/generated"
export ZEROBRAND_GEN_DIR="${ZEROBRAND_GEN_DIR:-$root/.local/generated}"
cd "$root"
exec "$root/.local/bin/m-brand" "${1:-all}"
