#!/usr/bin/env python3
"""Reject mixed checkouts and out-of-tree output paths before build or run."""
from pathlib import Path
import json
import re
import subprocess
import sys

MODULE = 'github.com/startopenapi-glitch/M-brand/v2/server'

def check(root, running=False):
    root = Path(root).resolve()
    if json.loads((root / 'PROJECT.json').read_text()).get('project') != 'm-brand':
        raise RuntimeError('M-brand checkout project identity mismatch')
    module = re.findall(r'^module\s+(\S+)\s*$', (root / 'v2/server/go.mod').read_text(), re.MULTILINE)
    if module != [MODULE]:
        raise RuntimeError('M-brand source identity mismatch')
    for legacy in ('v2/deploy/runtime.json', 'v2/deploy/release-guard/release-policy.json'):
        if (root / legacy).exists():
            raise RuntimeError('Mixed deployment checkout; use an independent M-brand installation')
    for relative in ('.local', '.local/bin/m-brand', '.local/generated', 'v2/server/webdist', 'v2/web/dist', 'v2/.env'):
        resolved = (root / relative).resolve()
        if not resolved.is_relative_to(root):
            raise RuntimeError('Deployment files and build outputs must stay inside this installation')
    if running:
        output = subprocess.check_output(['go', 'version', '-m', str(root / '.local/bin/m-brand')], text=True)
        modules = re.findall(r'^\s*mod\s+(\S+)\s+', output, re.MULTILINE)
        packages = re.findall(r'^\s*path\s+(\S+)\s*$', output, re.MULTILINE)
        if modules != [MODULE] or packages != [MODULE + '/cmd/0brand']:
            raise RuntimeError('M-brand binary identity mismatch; rebuild from this project')

if __name__ == '__main__':
    try:
        check(sys.argv[1], len(sys.argv) > 2 and sys.argv[2] == 'run')
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        sys.exit('Deployment refused: ' + str(error))
