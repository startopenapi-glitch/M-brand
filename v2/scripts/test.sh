#!/bin/bash
# 只允许本机 zerobrand_test；没有显式 URL 时仅从 .env 读取 DSN 并派生测试库。
set -euo pipefail
PROJECT_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
exec python3 - "$PROJECT_ROOT" "$@" <<'PY'
import os
from pathlib import Path
import re
import shlex
import sys
from urllib.parse import parse_qsl, unquote, urlsplit, urlunsplit

root = Path(sys.argv[1])
explicit = "ZEROBRAND_PG_URL" in os.environ
try:
    dsn = os.environ.get("ZEROBRAND_PG_URL", "")
    if not explicit:
        # Do not source the file: unrelated production keys and shell expressions
        # must never enter the test process or execute while preparing a test URL.
        for line in (root / ".env").read_text().splitlines():
            match = re.match(r"^\s*(?:export\s+)?ZEROBRAND_PG_URL\s*=\s*(.*?)\s*$", line)
            if match:
                parts = shlex.split(match.group(1), comments=True)
                if len(parts) != 1:
                    raise ValueError()
                dsn = parts[0]
    url = urlsplit(dsn)
    if url.scheme not in ("postgres", "postgresql") or url.hostname not in ("127.0.0.1", "localhost") or url.fragment:
        raise ValueError()
    # pgx/libpq query parameters can override the URI database/host. Fail closed.
    overrides = {"dbname", "database", "host", "hostaddr", "port", "service", "servicefile"}
    query = parse_qsl(url.query, strict_parsing=True) if url.query else []
    if any(key.lower() in overrides for key, _ in query):
        raise ValueError()
    if explicit and unquote(url.path) != "/zerobrand_test":
        raise ValueError()
    if not explicit:
        dsn = urlunsplit((url.scheme, url.netloc, "/zerobrand_test", url.query, ""))
except (OSError, ValueError):
    sys.exit("拒绝运行：必须配置本机 zerobrand_test 隔离测试库，且不允许 URL 查询参数覆盖数据库或主机。")

env = {k:v for k,v in os.environ.items() if not k.startswith(("ZEROBRAND_","WX_","WXPAY_","RESEND_","SMTP_"))}
env["ZEROBRAND_PG_URL"] = dsn
os.chdir(root / "server")
os.execvpe("go", ["go", "test", "./...", "-count=1", *sys.argv[2:]], env)
PY
