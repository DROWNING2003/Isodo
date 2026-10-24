#!/usr/bin/env python3
"""Render local Casdoor provisioning; credentials stay in ignored files."""
import json
import os
from pathlib import Path
import secrets

root = Path(__file__).resolve().parent.parent
path = root / '.env'
lines = path.read_text().splitlines()
config = {}
for line in lines:
    if '=' in line and not line.lstrip().startswith('#'):
        key, value = line.split('=', 1)
        config[key.strip()] = value.strip().strip('"').strip("'")
updates = {}
if not config.get('ISODO_CASDOOR_CLIENT_SECRET') or config.get('ISODO_CASDOOR_CLIENT_SECRET', '').startswith('replace-'):
    updates['ISODO_CASDOOR_CLIENT_SECRET'] = secrets.token_urlsafe(48)
if not config.get('ISODO_WEB_ORIGIN'):
    updates['ISODO_WEB_ORIGIN'] = 'http://127.0.0.1:5734'
if updates:
    lines = [line for line in lines if line.split('=', 1)[0] not in updates]
    lines.extend(f'{key}={value}' for key, value in updates.items())
    path.write_text('\n'.join(lines) + '\n')
    path.chmod(0o600)
    config.update(updates)

github_id = config.get('GITHUB_CLIENT_ID', '')
github_secret = config.get('GITHUB_CLIENT_SECRET', '')
origin = config['ISODO_WEB_ORIGIN'].rstrip('/')
provisioning = {
    'organizations': [{'owner': 'admin', 'name': 'isodo', 'displayName': 'Isodo', 'passwordType': 'bcrypt', 'defaultApplication': 'isodo'}],
    'providers': [{'owner': 'admin', 'name': 'isodo-github', 'displayName': 'GitHub', 'category': 'OAuth', 'type': 'GitHub', 'clientId': github_id, 'clientSecret': github_secret}],
    'applications': [{
        'owner': 'admin', 'name': 'isodo', 'displayName': 'Isodo', 'organization': 'isodo',
        'cert': 'cert-built-in', 'clientId': config['ISODO_CASDOOR_CLIENT_ID'], 'clientSecret': config['ISODO_CASDOOR_CLIENT_SECRET'],
        'enablePassword': False, 'enableSignUp': True, 'disableSelfSignup': True,
        'signinMethods': [{'name': name, 'displayName': name, 'rule': 'None'} for name in ['Password', 'Verification code', 'WebAuthn', 'Face ID']],
        'signinItems': [{'name': name, 'visible': True, 'rule': 'None'} for name in ['Logo', 'Providers']],
        'providers': [{'name': 'isodo-github', 'canSignUp': True, 'canSignIn': True, 'canUnlink': False, 'rule': 'None'}],
        'redirectUris': [origin + '/auth/callback'], 'grantTypes': ['authorization_code'],
        'tokenFormat': 'JWT', 'expireInHours': 12,
    }],
}
runtime = root / 'deploy/casdoor/init_data.runtime.json'
runtime.parent.mkdir(parents=True, exist_ok=True)
# Write in place: Casdoor's bind mount and watcher must see the same inode.
fd = os.open(runtime, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, 'w') as output:
    json.dump(provisioning, output, indent=2)
runtime.chmod(0o600)
print('Casdoor Isodo application configured; GitHub credentials ' + ('present' if github_id and github_secret else 'missing'))
print('Isodo callback: ' + origin + '/auth/callback')
