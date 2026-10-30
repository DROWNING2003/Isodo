#!/usr/bin/env bash
set -euo pipefail

# Local Electron + remote PR18 test API + same-site Preview Gateway.
# Deliberately pinned to the isolated PR18 test environment; do not read .env.

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly SSH_TARGET="jfcs1024"
readonly TEST_API_HOST="10.210.10.32"
readonly TEST_API_PORT="5004"
readonly TEST_API_URL="http://${TEST_API_HOST}:${TEST_API_PORT}"
readonly TEST_NODE_SERVICE="isodo-node-pr18-test.service"
readonly TEST_NODE_PORT="19192"
readonly TEST_API_ENV="/root/zkp/isodo-pr18-test/api.env"
readonly WEB_PORT="5734"
readonly WEB_ORIGIN="http://app.isodo.localhost:${WEB_PORT}"
readonly PREVIEW_DOMAIN="isodo.localhost"
readonly PREVIEW_PROBE_HOST="8080-00000000-0000-4000-8000-000000000000.${PREVIEW_DOMAIN}:${TEST_API_PORT}"

readonly RUN_LOG="${TMPDIR:-/tmp}/isodo-pr18-test-$(date +%Y%m%d-%H%M%S)-$$.log"
PROBE_BODY=""
SSH_PID=""
WEB_PID=""
ELECTRON_PID=""
TAIL_PID=""
OWN_SSH=0
OWN_WEB=0
OWN_ELECTRON=0

die() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

kill_tree() {
  local pid="$1" child
  while IFS= read -r child; do
    [[ "$child" =~ ^[0-9]+$ ]] && kill_tree "$child"
  done < <(pgrep -P "$pid" 2>/dev/null || true)
  kill -TERM "$pid" 2>/dev/null || true
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  [[ -n "$TAIL_PID" ]] && kill -TERM "$TAIL_PID" 2>/dev/null || true
  [[ "$OWN_ELECTRON" == 1 && -n "$ELECTRON_PID" ]] && kill_tree "$ELECTRON_PID"
  [[ "$OWN_WEB" == 1 && -n "$WEB_PID" ]] && kill_tree "$WEB_PID"
  [[ "$OWN_SSH" == 1 && -n "$SSH_PID" ]] && kill_tree "$SSH_PID"
  [[ -n "$PROBE_BODY" ]] && rm -f "$PROBE_BODY"
  if [[ "$status" == 0 ]]; then
    rm -f "$RUN_LOG"
  else
    printf 'Diagnostic log retained at %s\n' "$RUN_LOG" >&2
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

command -v ssh >/dev/null || die "ssh is required"
command -v curl >/dev/null || die "curl is required"
command -v lsof >/dev/null || die "lsof is required"
command -v pnpm >/dev/null || die "pnpm is required"
command -v make >/dev/null || die "make is required"
cd "$ROOT"
: >"$RUN_LOG"
chmod 600 "$RUN_LOG"

if [[ "$(uname -s)" != "Darwin" ]]; then
  die "This helper is for the Mac Electron development environment."
fi

# Never start or restart a shared Node. Only start the dedicated PR18 test unit
# when it is inactive; wait for its RPC listener before opening Electron.
printf 'Checking the isolated PR18 test API and Node...\n'
api_status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  --connect-timeout 4 --max-time 8 "${TEST_API_URL}/healthz")" || die "test API is unreachable"
[[ "$api_status" == 200 ]] || die "test API health check returned HTTP ${api_status}"

ssh -o BatchMode=yes -o ConnectTimeout=5 "$SSH_TARGET" \
  "set -eu; grep -Fxq 'ISODO_PREVIEW_DOMAIN=${PREVIEW_DOMAIN}' '${TEST_API_ENV}'; grep -Fxq 'ISODO_PREVIEW_SCHEME=http' '${TEST_API_ENV}'; grep -Fxq 'ISODO_NODE_RPC_TARGET=127.0.0.1:${TEST_NODE_PORT}' '${TEST_API_ENV}'; if ! systemctl is-active --quiet '${TEST_NODE_SERVICE}'; then systemctl start '${TEST_NODE_SERVICE}'; fi; for attempt in \$(seq 1 150); do if systemctl is-active --quiet '${TEST_NODE_SERVICE}' && ss -lnt 'sport = :${TEST_NODE_PORT}' | grep -q ':${TEST_NODE_PORT}'; then echo node-ready; exit 0; fi; sleep 2; done; echo node-did-not-become-ready >&2; exit 1" \
  | grep -qx 'node-ready' || die "isolated test Node did not become ready, or test API config is not pinned to it"

PROBE_BODY="$(mktemp "${TMPDIR:-/tmp}/isodo-pr18-probe.XXXXXX")"
preview_route_ready() {
  local status body
  status="$(curl --silent --show-error --output "$PROBE_BODY" --write-out '%{http_code}' \
    --connect-timeout 2 --max-time 4 \
    -H "Host: ${PREVIEW_PROBE_HOST}" \
    "http://127.0.0.1:${TEST_API_PORT}/__isodo/ready" 2>/dev/null)" || return 1
  body="$(cat "$PROBE_BODY")"
  [[ "$status" == 401 && "$body" == "preview authorization required" ]]
}

# Reuse only a listener that demonstrably reaches the remote Preview Gateway.
if lsof -nP -iTCP:"$TEST_API_PORT" -sTCP:LISTEN -t >/dev/null 2>&1; then
  preview_route_ready || die "127.0.0.1:${TEST_API_PORT} is occupied by something other than the PR18 Preview tunnel"
  printf 'Reusing the verified Preview SSH tunnel on 127.0.0.1:%s.\n' "$TEST_API_PORT"
else
  ssh -N -o BatchMode=yes -o ConnectTimeout=5 -o ExitOnForwardFailure=yes \
    -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
    -L "127.0.0.1:${TEST_API_PORT}:${TEST_API_HOST}:${TEST_API_PORT}" \
    "$SSH_TARGET" >/dev/null 2>&1 &
  SSH_PID=$!
  OWN_SSH=1
  for attempt in $(seq 1 30); do
    kill -0 "$SSH_PID" 2>/dev/null || die "SSH Preview tunnel exited during startup"
    preview_route_ready && break
    sleep 1
  done
  preview_route_ready || die "SSH tunnel started but Preview Host routing did not reach the remote API"
  printf 'Preview tunnel ready: *.%s:%s → %s:%s via %s.\n' \
    "$PREVIEW_DOMAIN" "$TEST_API_PORT" "$TEST_API_HOST" "$TEST_API_PORT" "$SSH_TARGET"
fi

if lsof -nP -iTCP:"$WEB_PORT" -sTCP:LISTEN -t >/dev/null 2>&1; then
  module="$(curl --silent --show-error --fail --connect-timeout 2 --max-time 5 \
    -H 'Host: app.isodo.localhost' "http://127.0.0.1:${WEB_PORT}/src/api/preview.ts")" \
    || die "port ${WEB_PORT} is occupied and does not serve the Isodo Vite preview module"
  grep -Fq '"VITE_ISODO_PREVIEW_DOMAIN": "isodo.localhost"' <<<"$module" \
    || die "port ${WEB_PORT} serves a Vite app with the wrong Preview domain"
  auth_status="$(curl --silent --show-error --output "$PROBE_BODY" --write-out '%{http_code}' \
    --connect-timeout 2 --max-time 5 -H 'Host: app.isodo.localhost' \
    "http://127.0.0.1:${WEB_PORT}/auth/me")" \
    || die "existing Vite server cannot reach the Isodo auth API"
  [[ "$auth_status" == 401 ]] || die "existing Vite server /auth/me returned HTTP ${auth_status}; expected the unauthenticated 401 probe"
  printf 'Reusing the verified Isodo Vite server on 127.0.0.1:%s.\n' "$WEB_PORT"
else
  export ISODO_API_URL="$TEST_API_URL"
  export APP_PREVIEW_DOMAIN="$PREVIEW_DOMAIN"
  export T3CODE_DEV_ALLOWED_HOSTS="app.isodo.localhost"
  make web APP_PREVIEW_DOMAIN="$PREVIEW_DOMAIN" >>"$RUN_LOG" 2>&1 &
  WEB_PID=$!
  OWN_WEB=1

  for attempt in $(seq 1 90); do
    kill -0 "$WEB_PID" 2>/dev/null || { tail -n 80 "$RUN_LOG" >&2; die "Vite exited during startup"; }
    if curl --silent --show-error --fail --connect-timeout 2 --max-time 3 \
      -H 'Host: app.isodo.localhost' "http://127.0.0.1:${WEB_PORT}/" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  curl --silent --show-error --fail --connect-timeout 2 --max-time 5 \
    -H 'Host: app.isodo.localhost' "http://127.0.0.1:${WEB_PORT}/" >/dev/null \
    || { tail -n 80 "$RUN_LOG" >&2; die "Vite did not become ready on port ${WEB_PORT}"; }
  module="$(curl --silent --show-error --fail -H 'Host: app.isodo.localhost' \
    "http://127.0.0.1:${WEB_PORT}/src/api/preview.ts")"
  grep -Fq '"VITE_ISODO_PREVIEW_DOMAIN": "isodo.localhost"' <<<"$module" \
    || die "Vite did not compile the same-site Preview domain"
  auth_status="$(curl --silent --show-error --output "$PROBE_BODY" --write-out '%{http_code}' \
    --connect-timeout 2 --max-time 5 -H 'Host: app.isodo.localhost' \
    "http://127.0.0.1:${WEB_PORT}/auth/me")" \
    || { tail -n 80 "$RUN_LOG" >&2; die "Vite could not reach the test API"; }
  [[ "$auth_status" == 401 ]] || die "Vite /auth/me returned HTTP ${auth_status}; expected the unauthenticated 401 probe"
fi

printf 'Starting Isodo Electron at %s (API: %s). Press Ctrl+C to stop this session.\n' \
  "$WEB_ORIGIN" "$TEST_API_URL"
if [[ ! -f "$ROOT/app/apps/desktop/dist-electron/main.cjs" ]]; then
  printf 'Building the desktop main process once because its bundle is missing...\n'
  (cd "$ROOT/app" && pnpm --filter @t3tools/desktop build) >>"$RUN_LOG" 2>&1 \
    || { tail -n 80 "$RUN_LOG" >&2; die "Electron desktop bundle build failed"; }
fi
tail -n 0 -F "$RUN_LOG" &
TAIL_PID=$!
export ISODO_WEB_URL="$WEB_ORIGIN"
(cd "$ROOT/app" && pnpm --filter @t3tools/desktop start) >>"$RUN_LOG" 2>&1 &
ELECTRON_PID=$!
OWN_ELECTRON=1
wait "$ELECTRON_PID"
