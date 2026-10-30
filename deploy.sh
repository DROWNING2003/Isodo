#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
cmd="${1:-help}"
node="${ISODO_NOMAD_NODE_NAME:-}"
image="${ISODO_API_IMAGE:-}"
version="${ISODO_NODE_VERSION:-}"
release="${ISODO_RELEASE:-}"
bind_addr="${ISODO_API_BIND_ADDR:-}"
api_port="${ISODO_API_PORT:-5004}"
config_dir="${ISODO_API_CONFIG_DIR:-/root/zkp/isodo-pr18-test}"
registry="aslan-spock-register.qiniu.io/las"
api_job="isodo-api-pr18-test"

validate_api_args() {
  [[ -n "$node" && -n "$image" && -n "$bind_addr" ]] || {
    echo "Set ISODO_NOMAD_NODE_NAME, ISODO_API_IMAGE, and ISODO_API_BIND_ADDR" >&2
    exit 2
  }
  [[ "$image" =~ ^aslan-spock-register\.qiniu\.io/las/isodo-api:[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || {
    echo "ISODO_API_IMAGE must be a version-tagged image in $registry/isodo-api; latest and untagged images are not allowed" >&2
    exit 2
  }
  [[ "${image##*:}" != latest ]] || { echo "latest is not allowed" >&2; exit 2; }
  [[ "$api_port" =~ ^[0-9]{1,5}$ ]] && ((10#$api_port >= 1024 && 10#$api_port <= 65535)) || {
    echo "ISODO_API_PORT must be an integer from 1024 to 65535" >&2
    exit 2
  }
  [[ "$config_dir" == /root/zkp/isodo-pr18-test ]] || {
    echo "ISODO_API_CONFIG_DIR must be /root/zkp/isodo-pr18-test for this isolated test job" >&2
    exit 2
  }
  python3 -c 'import ipaddress,sys; ipaddress.IPv4Address(sys.argv[1])' "$bind_addr" || {
    echo "ISODO_API_BIND_ADDR must be a valid IPv4 address" >&2
    exit 2
  }
  command -v ip >/dev/null 2>&1 && ip -o -4 addr show | awk '{split($4, a, "/"); print a[1]}' | grep -Fxq "$bind_addr" || {
    echo "ISODO_API_BIND_ADDR must be assigned to this Nomad client" >&2
    exit 2
  }
}

set_api_nomad_vars() {
  api_nomad_vars=(
    -var "node_name=$node"
    -var "image=$image"
    -var "bind_addr=$bind_addr"
    -var "api_port=$api_port"
    -var "config_dir=$config_dir"
  )
}

validate_and_plan_api() {
  validate_api_args
  set_api_nomad_vars
  nomad job validate "${api_nomad_vars[@]}" deploy/nomad/isodo-api.nomad.hcl

  local plan_output plan_status
  if plan_output="$(nomad job plan -no-color "${api_nomad_vars[@]}" deploy/nomad/isodo-api.nomad.hcl 2>&1)"; then
    plan_status=0
  else
    plan_status=$?
  fi
  printf '%s\n' "$plan_output"
  case "$plan_status" in
    0) ;;
    1)
      [[ "$plan_output" == *"All tasks successfully allocated."* ]] || {
        echo "BLOCK: Nomad plan detected a change but did not confirm successful placement" >&2
        exit 1
      }
      ;;
    *)
      echo "BLOCK: nomad job plan failed with exit code $plan_status" >&2
      exit "$plan_status"
      ;;
  esac
  if [[ "$plan_output" == *"+ Job: \"$api_job\""* ]]; then
    command -v ss >/dev/null 2>&1 || { echo "BLOCK: ss is required to check the first-deploy host port" >&2; exit 2; }
    if ss -H -ltn "sport = :$api_port" | grep -q .; then
      echo "BLOCK: host port $api_port is already listening" >&2
      exit 2
    fi
  fi
}

require_api_config() {
  local env_file="$config_dir/api.env"
  [[ -r "$env_file" && -r "$config_dir/certs/injection.key" ]] || {
    echo "BLOCK: expected isolated API config and injection key under $config_dir" >&2
    exit 2
  }
  local key
  for key in ISODO_DATABASE_URL ISODO_CASDOOR_ISSUER ISODO_CASDOOR_CLIENT_ID ISODO_CASDOOR_CLIENT_SECRET ISODO_WEB_ORIGIN; do
    grep -Eq "^[[:space:]]*(export[[:space:]]+)?${key}=.+$" "$env_file" || {
      echo "BLOCK: $key is missing or empty in $env_file" >&2
      exit 2
    }
  done
}

case "$cmd" in
 init)
   nomad version
   docker version
   for f in "$config_dir/api.env" "$config_dir/certs/injection.key"; do [[ -r "$f" ]] && echo "READABLE $f" || echo "MISSING OR UNREADABLE $f"; done
   ;;
 build-api)
   [[ "$release" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ && "$release" != latest ]] || exit 2
   docker build --platform linux/amd64 -f deploy/docker/Dockerfile.api -t "$registry/isodo-api:$release" .
   ;;
 plan-api|update-api|rollback-api)
   [[ "$cmd" != rollback-api ]] || image="${2:-}"
   validate_and_plan_api
   [[ "$cmd" != plan-api ]] || exit 0
   [[ "${ISODO_CONFIRM_UPDATE_API:-}" == yes ]] || { echo "Dry run: set ISODO_CONFIRM_UPDATE_API=yes to apply"; exit 0; }
   require_api_config
   nomad job run "${api_nomad_vars[@]}" deploy/nomad/isodo-api.nomad.hcl
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
 status) nomad job status "$api_job" || true; nomad job status isodo-node || true ;;
 logs)
   case "${2:-api}" in
     api) job="$api_job" ;;
     node) job="isodo-node" ;;
     *) echo "Usage: ./deploy.sh logs api|node" >&2; exit 2 ;;
   esac
   nomad alloc logs -job "$job"
   ;;
 *) echo "Usage: ./deploy.sh init|build-api|plan-api|update-api|update-node|status|logs [api|node]|rollback-api VERSIONED_IMAGE" ;;
esac
