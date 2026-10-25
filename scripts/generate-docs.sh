#!/bin/sh
set -eu

repo_dir="$(pwd)"
site_dir="$repo_dir/docs-site"
node_tool_dir="$repo_dir/.tools/node/$(uname -s)-$(uname -m)/docs-site"
node_tool_root="$(dirname "$node_tool_dir")"
mkdir -p "$node_tool_dir"

python3 - "$site_dir" "$node_tool_dir" <<'PY'
import shutil
import sys
from pathlib import Path

source = Path(sys.argv[1])
destination = Path(sys.argv[2])
ignored = {"node_modules", ".next", ".source"}
for path in source.rglob("*"):
    relative = path.relative_to(source)
    if any(part in ignored for part in relative.parts):
        continue
    if relative.as_posix() in {"package.json", "package-lock.json"}:
        continue
    target = destination / relative
    if path.is_dir():
        target.mkdir(parents=True, exist_ok=True)
    elif path.is_file():
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, target)
PY

if [ ! -x "$node_tool_dir/node_modules/.bin/fumadocs-mdx" ] || \
   ! cmp -s "$site_dir/package.json" "$node_tool_dir/package.json" || \
   ! cmp -s "$site_dir/package-lock.json" "$node_tool_dir/package-lock.json"; then
	cp "$site_dir/package.json" "$site_dir/package-lock.json" "$node_tool_dir/"
	npm ci --prefix "$node_tool_dir"
fi

mkdir -p "$node_tool_root/api/openapi"
cp "$repo_dir/api/openapi/openapi-public.yaml" "$node_tool_root/api/openapi/openapi-public.yaml"
node "$node_tool_dir/scripts/generate-openapi.mjs"
python3 - "$node_tool_dir/content/docs/api-reference/endpoints" \
	"$site_dir/content/docs/api-reference/endpoints" <<'PY'
import shutil
import sys
from pathlib import Path

source = Path(sys.argv[1])
destination = Path(sys.argv[2])
destination.mkdir(parents=True, exist_ok=True)
for path in source.glob("*.mdx"):
    shutil.copy2(path, destination / path.name)
shutil.copy2(source / "meta.json", destination / "meta.json")
PY
