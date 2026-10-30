#!/usr/bin/env bash
# Development-only unsigned macOS arm64 Electron package; does not publish/deploy.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
[[ "$(uname -s)" = Darwin && "$(uname -m)" = arm64 ]] || { echo "Requires macOS arm64 runner"; exit 2; }
mkdir -p dist/desktop
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o dist/desktop/isodo-node ./cmd/isodo-node
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o dist/desktop/isodo-guest-linux ./cmd/isodo-guest
cd app
corepack pnpm@11.10.0 --filter @t3tools/web build
corepack pnpm@11.10.0 --filter @t3tools/desktop build
# Use the installed Electron runtime, stage packaged renderer and preload in Resources/app.
ELECTRON_APP="$(node -p "require('path').dirname(require.resolve('electron/package.json'))")/dist/Electron.app"
[[ -d "$ELECTRON_APP" ]] || { echo "Electron runtime not installed"; exit 1; }
STAGE="$ROOT/dist/desktop/staging"
rm -rf "$STAGE"
mkdir -p "$STAGE"
ditto "$ELECTRON_APP" "$STAGE/Isodo.app"
RES="$STAGE/Isodo.app/Contents/Resources"
mkdir -p "$RES/app/apps/desktop" "$RES/app/apps/web"
cp -R apps/desktop/dist-electron "$RES/app/apps/desktop/"
cp -R apps/web/dist "$RES/app/apps/web/"
printf '{"name":"isodo","version":"0.0.0","main":"apps/desktop/dist-electron/main.cjs"}\n' > "$RES/app/package.json"
cp "$ROOT/dist/desktop/isodo-node" "$RES/isodo-node"
cp "$ROOT/dist/desktop/isodo-guest-linux" "$RES/isodo-guest-linux"
chmod 755 "$RES/isodo-node" "$RES/isodo-guest-linux"
# No signing/notarization. Local Lima Agent workspace bundle is not packaged here.
cd "$STAGE"
ditto -c -k --keepParent Isodo.app "$ROOT/dist/desktop/Isodo-macos-arm64-unsigned.zip"
echo "Created unsigned test artifact: dist/desktop/Isodo-macos-arm64-unsigned.zip"
