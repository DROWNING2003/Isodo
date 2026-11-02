#!/usr/bin/env bash
# Build the remote-only Web with the same preview policy and workspace surface
# used by the drowning.top deployment. No credentials are embedded.
set -euo pipefail
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT/app/apps/web"
export VITE_ISODO_DOCS_URL=https://docs.drowning.top:18443/docs
export VITE_ISODO_REMOTE_ONLY=true
export VITE_ISODO_PREVIEW_DOMAIN=drowning.top
export VITE_BOUNDLY_WEB_SURFACE=full
exec ../../node_modules/.bin/vp build "$@"
