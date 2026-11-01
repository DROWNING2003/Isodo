#!/bin/sh
set -eu
/usr/bin/docker exec isodo-nginx nginx -t -p /opt/isodo/nginx/ -c /opt/isodo/nginx/nginx.conf
exec /usr/bin/docker exec isodo-nginx nginx -s reload -p /opt/isodo/nginx/ -c /opt/isodo/nginx/nginx.conf
