#!/bin/bash
# Run interactively on jfcs1024 as root; never pass credentials as arguments.
set -euo pipefail
set +x
umask 077
[[ $EUID == 0 && -t 0 ]] || { echo 'Run as root in an interactive server terminal.' >&2; exit 1; }
readonly ACME=/opt/isodo/acme/acme.sh
readonly CERT=/etc/ssl/drowning.top
[[ -x "$ACME" ]] || { echo 'Pinned acme.sh installation missing.' >&2; exit 1; }
if [[ -f "$CERT/fullchain.pem" ]]; then
 echo 'Certificate already exists. Inspect SAN/expiry; do not reissue blindly.' >&2
 exit 1
fi
read -rsp 'Aliyun RAM AccessKey ID: ' Ali_Key; printf '\n'
read -rsp 'Aliyun RAM AccessKey Secret: ' Ali_Secret; printf '\n'
[[ -n $Ali_Key && -n $Ali_Secret ]] || exit 1
export Ali_Key Ali_Secret
trap 'unset Ali_Key Ali_Secret' EXIT
install -d -m 700 "$CERT"
# acme.sh persists DNS credentials in its root-only account.conf for renewal.
# No debug/log flags: provider signed request URLs must not enter logs.
"$ACME" --home /opt/isodo/acme --issue --server letsencrypt --dns dns_ali -d drowning.top -d '*.drowning.top' --keylength ec-256
unset Ali_Key Ali_Secret
"$ACME" --home /opt/isodo/acme --install-cert -d drowning.top --ecc --key-file "$CERT/privkey.pem" --fullchain-file "$CERT/fullchain.pem" --reloadcmd '/opt/isodo/nginx/renew-reload.sh'
chmod 600 "$CERT/privkey.pem" /opt/isodo/acme/account.conf
openssl x509 -in "$CERT/fullchain.pem" -noout -dates -ext subjectAltName
systemctl enable --now isodo-nginx.service
systemctl enable --now isodo-acme-renew.timer
