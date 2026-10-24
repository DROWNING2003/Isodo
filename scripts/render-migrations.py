#!/usr/bin/env python3
"""Render forward SQL migrations for psql with transactional version tracking."""
import hashlib
import re
from pathlib import Path

print("\\set ON_ERROR_STOP on")
print("BEGIN; SELECT pg_advisory_xact_lock(72413092);")
print("CREATE TABLE IF NOT EXISTS isodo_schema_migrations (version text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now());")
for path in sorted(Path('migrations').glob('*.sql')):
    if not re.fullmatch(r'[0-9]+_[A-Za-z0-9_-]+\.sql', path.name):
        raise SystemExit(f'Invalid migration filename: {path.name}')
    raw = path.read_text()
    up = raw.split('-- +goose Up', 1)[1].split('-- +goose Down', 1)[0]
    checksum = hashlib.sha256(up.encode()).hexdigest()
    print(f"DO $$ BEGIN IF EXISTS (SELECT 1 FROM isodo_schema_migrations WHERE version = '{path.name}' AND checksum <> '{checksum}') THEN RAISE EXCEPTION 'Migration checksum mismatch: {path.name}'; END IF; END $$;")
    print(f"SELECT NOT EXISTS (SELECT 1 FROM isodo_schema_migrations WHERE version = '{path.name}') AS apply_migration \\gset")
    print('\\if :apply_migration')
    print(up)
    print(f"INSERT INTO isodo_schema_migrations(version, checksum) VALUES ('{path.name}', '{checksum}');")
    print('\\endif')
print('COMMIT;')
