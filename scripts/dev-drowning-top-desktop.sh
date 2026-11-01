#!/usr/bin/env bash
# VPN HTTPS launcher: does not manage remote services or create SSH tunnels.
set -euo pipefail
readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export ISODO_REMOTE_API_URL="${ISODO_REMOTE_API_URL:-https://api.drowning.top:18443}"
export ISODO_DESKTOP_STATIC=1
export ISODO_DESKTOP_ORIGIN=http://app.isodo.localhost:5734
export VITE_ISODO_PREVIEW_DOMAIN=drowning.top
export VITE_BOUNDLY_WEB_SURFACE=full
export VITE_ISODO_REMOTE_ONLY=false
unset ISODO_REMOTE_API_INSECURE_HOST
curl --noproxy '*' --fail --silent --show-error --connect-timeout 5 --max-time 10 \
  "${ISODO_REMOTE_API_URL}/healthz" >/dev/null
if lsof -nP -iTCP:5734 -sTCP:LISTEN -t >/dev/null 2>&1; then
  echo 'Port 5734 is occupied. Close the existing desktop/dev session before using this launcher.' >&2
  exit 1
fi
cd "$ROOT/app"
pnpm --filter @t3tools/web build
pnpm --filter @t3tools/desktop build
exec pnpm --filter @t3tools/desktop start
