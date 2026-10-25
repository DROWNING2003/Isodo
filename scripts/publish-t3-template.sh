#!/bin/sh
# Publish a packaged T3 candidate as the system template `base`.
# The published system template is always named `base`.
# Operator-only. Usage: publish-t3-template.sh DIR DIGEST CHV_BINARY
set -eu
if [ "$#" -lt 3 ]; then
    echo 'usage: publish-t3-template.sh CANDIDATE_DIR MANIFEST_DIGEST CLOUD_HYPERVISOR' >&2
    exit 1
fi
if [ "$(id -u)" != 0 ]; then
    echo 'Run publish-t3-template.sh as root on the deployment host.' >&2
    exit 1
fi
repository=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
candidate=$1
digest=$2
hypervisor=$3
env_file=${ISODO_ENV_FILE:-/root/zkp/.env}
if [ ! -f "$env_file" ]; then
    echo "Environment file not found: $env_file" >&2
    exit 1
fi
set -a
. "$env_file"
set +a
# The system base uses runtime restore by default. A cold publication must be
# an explicit operator choice, never an unnoticed fallback from a failed build.
python3 - "$candidate/manifest.json" <<'PY'
import json
import os
import sys

manifest = json.load(open(sys.argv[1]))
if not manifest.get("runtime_snapshot") and os.environ.get("ISODO_ALLOW_COLD_TEMPLATE") != "true":
    raise SystemExit("System base requires a runtime snapshot; run build-warm first, or explicitly set ISODO_ALLOW_COLD_TEMPLATE=true.")
PY
if [ -z "${ISODO_DATABASE_URL:-}" ]; then
    ISODO_DATABASE_URL=$(python3 - <<'PY'
import os, urllib.parse
user = urllib.parse.quote(os.environ.get("POSTGRES_USER", "isodo"), safe="")
password = urllib.parse.quote(os.environ.get("POSTGRES_PASSWORD", ""), safe="")
database = urllib.parse.quote(os.environ.get("POSTGRES_DB", "isodo"), safe="")
port = os.environ.get("POSTGRES_PORT", "5432")
print(f"postgres://{user}:{password}@127.0.0.1:{port}/{database}?sslmode=disable")
PY
)
fi
export ISODO_DATABASE_URL
export ISODO_TEMPLATE_S3_ENDPOINT=${ISODO_TEMPLATE_S3_ENDPOINT:-http://127.0.0.1:${SEAWEEDFS_PORT:-8333}}
export ISODO_TEMPLATE_S3_REGION=${ISODO_TEMPLATE_S3_REGION:-us-east-1}
export ISODO_TEMPLATE_S3_BUCKET=${ISODO_TEMPLATE_S3_BUCKET:-isodo-templates}
export SEAWEEDFS_ACCESS_KEY SEAWEEDFS_SECRET_KEY
exec "$repository/.tools/isodo-template" publish-system \
    --name "${ISODO_TEMPLATE_NAME:-base}" \
    --description 'Isodo base workspace with Guest Agent, T3, terminal and files' \
    --dir "$candidate" \
    --digest "$digest" \
    --vmm-binary "$hypervisor"
