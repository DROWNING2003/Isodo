#!/usr/bin/env python3
"""Create local, bucket-scoped SeaweedFS credentials without printing them."""
import json
import os
import re
import secrets
import shlex
from pathlib import Path

path = Path('.env')
lines = path.read_text().splitlines()
values = {}
for line in lines:
    name, sep, value = line.partition('=')
    if sep and not name.lstrip().startswith('#'):
        parts = shlex.split(value, comments=True)
        values[name.strip()] = parts[0] if parts else ''

changed = {}
for name in ('SEAWEEDFS_ACCESS_KEY', 'SEAWEEDFS_SECRET_KEY', 'SEAWEEDFS_READ_ACCESS_KEY', 'SEAWEEDFS_READ_SECRET_KEY'):
    value = values.get(name, '')
    if not value or value.startswith('replace-'):
        value = secrets.token_hex(24)
        values[name] = value
        changed[name] = value

for name, default in {
    'SEAWEEDFS_PORT': '8333',
    'ISODO_TEMPLATE_S3_ENDPOINT': 'http://127.0.0.1:8333',
    'ISODO_TEMPLATE_S3_REGION': 'us-east-1',
    'ISODO_TEMPLATE_S3_BUCKET': 'isodo-templates',
}.items():
    if not values.get(name):
        values[name] = default
        changed[name] = default

bucket = values['ISODO_TEMPLATE_S3_BUCKET']
if not re.fullmatch(r'[a-z0-9][a-z0-9-]{1,61}[a-z0-9]', bucket):
    raise SystemExit('ISODO_TEMPLATE_S3_BUCKET must be a DNS-compatible bucket name.')
if changed:
    existing = set()
    for i, line in enumerate(lines):
        name = line.partition('=')[0].strip()
        if name in changed:
            lines[i] = f'{name}={changed[name]}'
            existing.add(name)
    lines.extend(f'{name}={value}' for name, value in changed.items() if name not in existing)
    temporary = path.with_suffix('.seaweed-tmp')
    with os.fdopen(os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), 'w') as output:
        os.fchmod(output.fileno(), 0o600)
        output.write('\n'.join(lines) + '\n')
    os.replace(temporary, path)

identities = []
for name, access, secret, actions in [
    ('isodo-template-worker', 'SEAWEEDFS_ACCESS_KEY', 'SEAWEEDFS_SECRET_KEY', ['Admin', 'Read', 'Write', 'List', 'Tagging']),
    ('isodo-node-reader', 'SEAWEEDFS_READ_ACCESS_KEY', 'SEAWEEDFS_READ_SECRET_KEY', ['Read', 'List']),
]:
    identities.append({'name': name, 'credentials': [{'accessKey': values[access], 'secretKey': values[secret]}], 'actions': [f'{action}:{bucket}' for action in actions]})
config = Path('deploy/seaweedfs/s3.runtime.json')
config.parent.mkdir(parents=True, exist_ok=True)
with os.fdopen(os.open(config, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), 'w') as output:
    os.fchmod(output.fileno(), 0o600)
    output.write(json.dumps({'identities': identities}, indent=2) + '\n')
print('SeaweedFS bucket credentials configured; secrets remain in .env and the ignored runtime config.')
