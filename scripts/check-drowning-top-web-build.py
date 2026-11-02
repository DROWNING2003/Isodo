#!/usr/bin/env python3
"""Verify the fixed drowning.top Web build before publishing or switching it."""
import hashlib
import json
import os
from pathlib import Path
import sys

EXPECTED = {
    'VITE_ISODO_REMOTE_ONLY': 'true',
    'VITE_ISODO_PREVIEW_DOMAIN': 'drowning.top',
    'VITE_BOUNDLY_WEB_SURFACE': 'full',
    'VITE_ISODO_DOCS_URL': 'https://docs.drowning.top:18443/docs',
}
record = len(sys.argv) == 3 and sys.argv[1] == '--record'
root = Path(sys.argv[-1])
manifest = root / 'isodo-build.json'
try:
    if not (root / 'index.html').is_file() or not list((root / 'assets').glob('*.js')):
        raise ValueError('缺少 index.html 或 JavaScript 产物')
    files = {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
             for p in sorted(root.rglob('*')) if p.is_file() and p != manifest}
    if record:
        if any(os.environ.get(k) != v for k, v in EXPECTED.items()):
            raise ValueError('构建环境不符合 drowning.top 发布要求')
        manifest.write_text(json.dumps({'config': EXPECTED, 'sha256': files}, indent=2) + '\n')
    saved = json.loads(manifest.read_text())
    if saved.get('config') != EXPECTED or saved.get('sha256') != files:
        raise ValueError('配置或文件摘要不匹配，禁止发布混合/过期产物')
except (OSError, ValueError) as error:
    raise SystemExit(f'BLOCK: {error}；请从仓库根目录运行 bash scripts/build-drowning-top-web.sh')
print('PASS: drowning.top Web 构建配置与产物一致')
