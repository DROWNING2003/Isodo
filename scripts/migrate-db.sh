#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
. ./.env
isodo_migrate_db="${1:-$POSTGRES_DB}"
python3 scripts/render-migrations.py | docker compose -f compose.yaml exec -T db \
    psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$isodo_migrate_db"
