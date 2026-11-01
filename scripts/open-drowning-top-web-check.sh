#!/usr/bin/env bash
# Mac browser verification with temporary process-local DNS mapping.
# Does not change company DNS, /etc/hosts or TLS verification.
set -euo pipefail
[[ "$(uname -s)" == Darwin ]] || { echo 'This helper requires macOS.' >&2; exit 1; }
readonly PROFILE="${TMPDIR:-/tmp}/isodo-web-drowning-top-check"
if [[ -d '/Applications/Google Chrome.app' ]]; then
  browser='Google Chrome'
else
  echo 'Chrome is required for this temporary mapping helper. Arc did not apply these flags; use Safari with working DNS or setup-drowning-top-local-dns.sh.' >&2
  exit 1
fi
exec open -na "$browser" --args --user-data-dir="$PROFILE" \
  --host-resolver-rules='MAP *.drowning.top 10.210.10.32' --no-proxy-server \
  'https://web.drowning.top:18443/sandboxes'
