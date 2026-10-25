#!/bin/sh
# Build the Linux/amd64 base image and package a CHV template candidate.
# The resulting `base` artifact includes the Guest Agent and the T3 workspace.
# Operator-only: run as root on the Linux/amd64 build host. Requires docker,
# cpio, e2fsprogs, python3 and the guest kernel modules used by
# scripts/build-base-template.sh.
set -eu

if [ "$(id -u)" != 0 ]; then
    echo 'Run the base packaging script as root on the Linux build host.' >&2
    exit 1
fi
if [ "$(uname -m)" != "x86_64" ] && [ "$(uname -m)" != "amd64" ]; then
    echo 'base currently requires a Linux/amd64 build host.' >&2
    exit 1
fi
if [ "$#" != 1 ]; then
    echo 'usage: build-base-template-with-t3.sh NEW_OUTPUT_DIRECTORY' >&2
    exit 1
fi

repository=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output=$1
mkdir -p "$(dirname "$output")"
bundle=${ISODO_T3_BUNDLE:-$repository/app/artifacts/sandbox/t3-server-dist.bundle}
if [ ! -s "$bundle" ]; then
    echo "T3 server bundle not found: $bundle" >&2
    echo 'Set ISODO_T3_BUNDLE to a linux/amd64 bundle that includes dist/client.' >&2
    exit 1
fi
if ! tar -tzf "$bundle" | grep -q 'dist/node_modules/@yuuang/ffi-rs-linux-x64-gnu'; then
    echo 'T3 server bundle is not a linux/x64 bundle (missing @yuuang/ffi-rs-linux-x64-gnu).' >&2
    exit 1
fi
if ! tar -tzf "$bundle" | grep -q 'dist/client/index.html'; then
    echo 'T3 server bundle has no dist/client; T3 would answer 503 No static directory.' >&2
    exit 1
fi

cd "$repository"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -o images/linux-base/isodo-guest ./cmd/isodo-guest
docker build --platform linux/amd64 -t isodo-linux-base:0.1.0-dev images/linux-base
bundle_destination=$repository/images/base/t3-server-dist.bundle
bundle_copied=false
if [ "$(realpath "$bundle")" != "$(realpath "$bundle_destination")" ]; then
    cp "$bundle" "$bundle_destination"
    bundle_copied=true
fi
trap 'if [ "$bundle_copied" = true ]; then rm -f "$bundle_destination"; fi' EXIT
docker build --platform linux/amd64 \
    --build-arg BASE_IMAGE=isodo-linux-base:0.1.0-dev \
    -t isodo-base:0.1.0-dev images/base

ISODO_BASE_IMAGE=isodo-base:0.1.0-dev \
ISODO_BASE_NAME=${ISODO_BASE_NAME:-base} \
ISODO_ROOTFS_SIZE=${ISODO_ROOTFS_SIZE:-4G} \
    sh scripts/build-base-template.sh "$output"

echo "Base candidate with T3 packaged at $output"
echo "Next: publish-t3-template.sh (defaults to base; see images/base/README.md)."
