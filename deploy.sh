#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
cmd="${1:-help}"
node="${ISODO_NOMAD_NODE_NAME:-}"
image="${ISODO_API_IMAGE:-}"
version="${ISODO_NODE_VERSION:-}"
registry="aslan-spock-register.qiniu.io/las"
case "$cmd" in
 init)
   nomad version
   docker version
   [[ -e /dev/kvm ]] || echo "WARN: no /dev/kvm"
   for f in /etc/isodo/api.env /etc/isodo/node.env /var/lib/isodo/node/journal.json; do [[ -e "$f" ]] && echo "EXISTS $f" || echo "MISSING $f"; done
   ;;
 build-api)
   [[ "${ISODO_RELEASE:-}" =~ ^[A-Za-z0-9._-]+$ ]] || exit 2
   docker build --platform linux/amd64 -f deploy/docker/Dockerfile.api -t "$registry/isodo-api:$ISODO_RELEASE" .
   ;;
 update-api|rollback-api)
   [[ -n "$node" ]] || { echo "Set ISODO_NOMAD_NODE_NAME"; exit 2; }
   [[ "$cmd" != rollback-api ]] || image="${2:-}"
   [[ "$image" == "$registry/"* && "$image" != *:latest ]] || exit 2
   nomad job validate -var "node_name=$node" -var "image=$image" deploy/nomad/isodo-api.nomad.hcl
   nomad job plan -var "node_name=$node" -var "image=$image" deploy/nomad/isodo-api.nomad.hcl || true
   [[ "${ISODO_CONFIRM_UPDATE_API:-}" == yes ]] || { echo "Dry run: set ISODO_CONFIRM_UPDATE_API=yes to apply"; exit 0; }
   nomad job run -var "node_name=$node" -var "image=$image" deploy/nomad/isodo-api.nomad.hcl
   ;;
 update-node)
   [[ -n "$node" && "$version" =~ ^[A-Za-z0-9._-]+$ ]] || exit 2
   [[ "${ISODO_NODE_MAINTENANCE:-}" == yes ]] || { echo "BLOCK: maintenance required"; exit 2; }
   if pgrep -x cloud-hypervisor >/dev/null || pgrep -x isodo-node >/dev/null; then echo "BLOCK: runtime active"; exit 2; fi
   python3 deploy/scripts/check-node-safe.py "${ISODO_NODE_STATE_DIR:-/var/lib/isodo/node}/journal.json"
   [[ -x "/opt/isodo/releases/$version/isodo-node" ]] || exit 2
   nomad job validate -var "enabled=true" -var "node_name=$node" -var "version=$version" deploy/nomad/isodo-node.nomad.hcl
   [[ "${ISODO_CONFIRM_UPDATE_NODE:-}" == yes ]] || { echo "Dry run: set ISODO_CONFIRM_UPDATE_NODE=yes"; exit 0; }
   nomad job run -var "enabled=true" -var "node_name=$node" -var "version=$version" deploy/nomad/isodo-node.nomad.hcl
   ;;
 status) nomad job status isodo-api || true; nomad job status isodo-node || true ;;
 logs) nomad alloc logs -job "isodo-${2:-api}" ;;
 *) echo "Usage: ./deploy.sh init|build-api|update-api|update-node|status|logs [api|node]|rollback-api IMAGE" ;;
esac
