#!/bin/sh
set -eu
chmod 600 /etc/ssl/drowning.top/privkey.pem
if systemctl is-active --quiet isodo-nginx.service; then
    systemctl reload isodo-nginx.service
fi
