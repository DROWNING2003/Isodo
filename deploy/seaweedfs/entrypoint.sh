#!/bin/sh
set -eu
# The official entrypoint drops root to seaweed. Keep the host secret 0600;
# prepare a container-local copy owned by the service user before that drop.
cp /run/isodo-s3-source.json /tmp/isodo-s3.json
chown seaweed:seaweed /tmp/isodo-s3.json
chmod 0400 /tmp/isodo-s3.json
exec /entrypoint.sh "$@"
