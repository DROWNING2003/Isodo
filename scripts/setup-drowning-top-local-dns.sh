#!/usr/bin/env bash
# Only affects this Mac's drowning.top resolution. Run via sudo.
set -euo pipefail
readonly LABEL=local.isodo.drowning-dns
readonly PLIST=/Library/LaunchDaemons/local.isodo.drowning-dns.plist
readonly DIR='/Library/Application Support/Isodo/DNS'
readonly RESOLVER=/etc/resolver/drowning.top
readonly MARKER='# Managed by Isodo drowning.top local DNS'
[[ "$(uname -s)" == Darwin && $EUID == 0 ]] || { echo "Run: sudo bash $0 [install|remove]" >&2; exit 1; }
case "${1:-install}" in
install)
  readonly DNSMASQ=/opt/homebrew/opt/dnsmasq/sbin/dnsmasq
  [[ -x "$DNSMASQ" ]] || { echo 'Expected installed Homebrew dnsmasq is missing.' >&2; exit 1; }
  if [[ -e "$RESOLVER" ]] && ! grep -Fxq "$MARKER" "$RESOLVER"; then
    echo "Existing $RESOLVER is not owned by this helper; refusing to overwrite." >&2; exit 1
  fi
  if [[ -e "$PLIST" ]] && ! grep -Fq "$MARKER" "$PLIST"; then
    echo 'Existing launchd configuration is not owned by this helper.' >&2; exit 1
  fi
  if [[ ! -e "$PLIST" ]] && lsof -nP -iTCP:15353 -iUDP:15353 -t >/dev/null 2>&1; then
    echo 'Port 15353 is occupied; refusing to replace its listener.' >&2; exit 1
  fi
  install -d -m 755 "$DIR" /etc/resolver
  cat > "$DIR/dnsmasq.conf" <<'CONF'
# Managed by Isodo drowning.top local DNS
port=15353
listen-address=127.0.0.1
bind-interfaces
no-resolv
no-hosts
local=/drowning.top/
address=/drowning.top/10.210.10.32
CONF
  "$DNSMASQ" --test --conf-file="$DIR/dnsmasq.conf"
  cat > "$PLIST" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- # Managed by Isodo drowning.top local DNS -->
<plist version="1.0"><dict>
<key>Label</key><string>local.isodo.drowning-dns</string>
<key>ProgramArguments</key><array>
<string>/opt/homebrew/opt/dnsmasq/sbin/dnsmasq</string>
<string>--keep-in-foreground</string>
<string>--conf-file=/Library/Application Support/Isodo/DNS/dnsmasq.conf</string>
</array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>StandardErrorPath</key><string>/var/log/isodo-drowning-dns.log</string>
</dict></plist>
PLIST
  chmod 644 "$PLIST" "$DIR/dnsmasq.conf"
  plutil -lint "$PLIST"
  launchctl bootout "system/$LABEL" >/dev/null 2>&1 || true
  launchctl bootstrap system "$PLIST"
  ready=0
  for attempt in {1..20}; do
    if [[ "$(dig @127.0.0.1 -p 15353 +time=1 +tries=1 +short web.drowning.top A 2>/dev/null)" == 10.210.10.32 ]]; then ready=1; break; fi
    sleep 0.2
  done
  [[ "$ready" == 1 ]] || { echo 'Local DNS did not start; system resolver was not changed.' >&2; exit 1; }
  printf '%s\nnameserver 127.0.0.1\nport 15353\n' "$MARKER" > "$RESOLVER"
  chmod 644 "$RESOLVER"
  dscacheutil -flushcache
  killall -HUP mDNSResponder || true
  echo 'Local DNS enabled only for drowning.top and its subdomains.'
  echo 'Connect VPN, fully quit/reopen Arc, then open https://web.drowning.top:18443/sandboxes'
  echo 'This resolver persists across reboots. Remove it using this script with remove.'
  ;;
remove)
  if [[ -e "$RESOLVER" ]]; then
    grep -Fxq "$MARKER" "$RESOLVER" || { echo 'Resolver is not owned by this helper.' >&2; exit 1; }
    rm "$RESOLVER"
  fi
  if [[ -e "$PLIST" ]]; then
    grep -Fq "$MARKER" "$PLIST" || { echo 'LaunchDaemon is not owned by this helper.' >&2; exit 1; }
    launchctl bootout "system/$LABEL" >/dev/null 2>&1 || true
    rm "$PLIST"
  fi
  dscacheutil -flushcache
  killall -HUP mDNSResponder || true
  echo 'Isodo local resolver removed; other DNS configuration remains intact.'
  ;;
*) echo 'Usage: install | remove' >&2; exit 1 ;;
esac
