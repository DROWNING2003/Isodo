#!/usr/bin/env python3
"""Compare generated API and docs output before/after the unified generator."""
import hashlib
import subprocess
from pathlib import Path

def snapshot():
    files = [
        Path('internal/api/admin/api.gen.go'),
        Path('internal/api/public/api.gen.go'),
        Path('app/apps/web/src/api/admin.generated.ts'),
        Path('app/apps/web/src/api/public.generated.ts'),
    ]
    files.extend(Path('internal/control/postgres/dbsqlc').glob('*.go'))
    files.extend(Path('internal/api/guest').rglob('*.pb.go'))
    files.extend(Path('internal/api/node').rglob('*.pb.go'))
    files.extend(
        path
        for path in Path('docs-site/content/docs/api-reference/endpoints').rglob('*')
        if path.is_file()
    )
    return {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in files if p.exists()}

before = snapshot()
subprocess.run(['make', 'generate'], check=True)
after = snapshot()
changed = sorted(p for p in before.keys() | after.keys() if before.get(p) != after.get(p))
if changed:
    raise SystemExit('Generated API/docs drift (run make generate):\n' + '\n'.join(changed))
print('Generated API bindings and Fumadocs pages are consistent.')
