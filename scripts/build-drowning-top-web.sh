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
../../node_modules/.bin/vp build "$@"
# Mark only a successful build made with this fixed deployment configuration.
python3 "$ROOT/scripts/check-drowning-top-web-build.py" --record "$PWD/dist"
python3 "$ROOT/scripts/check-drowning-top-web-build.py" "$PWD/dist"
