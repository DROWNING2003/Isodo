#!/bin/sh
set -eu
exec /usr/bin/docker run --rm --network host --read-only --tmpfs /tmp -v /opt/isodo/nginx:/opt/isodo/nginx -v /etc/ssl/drowning.top:/etc/ssl/drowning.top:ro --entrypoint /usr/sbin/nginx aslan-spock-register.qiniu.io/las/nginx@sha256:e6b408afd0fcc6786c31e80825f4d2c6ca76934aa3e9280152addb23202ffd5d -t -p /opt/isodo/nginx/ -c /opt/isodo/nginx/nginx.conf
