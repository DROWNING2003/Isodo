#!/usr/bin/env bash
# Linux/KVM only. Starts a new RAM-root VM with no NIC, production state or DB.
set -euo pipefail
readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
: "${ISODO_CHV_BINARY:?Path to Cloud Hypervisor required}"
: "${ISODO_GUEST_KERNEL:?Path to matching Linux kernel required}"
: "${ISODO_GUEST_MODULES:?Directory containing matching Vsock .ko modules required}"
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && -w /dev/kvm ]] || {
  echo 'Requires Linux amd64 and access to /dev/kvm.' >&2; exit 1;
}
for tool in go busybox cpio gzip; do command -v "$tool" >/dev/null; done
readonly WORK="$(mktemp -d /tmp/isodo-commands-build.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/root"/{bin,proc,sys,dev,modules,workspace,tmp,root}
cp "$(command -v busybox)" "$WORK/root/bin/busybox"
ln -s busybox "$WORK/root/bin/sh"
for module in vsock vmw_vsock_virtio_transport_common vmw_vsock_virtio_transport; do
  cp "$ISODO_GUEST_MODULES/$module.ko" "$WORK/root/modules/"
done
cp "$ROOT/scripts/fixtures/runtime-commands-init.sh" "$WORK/root/init"
chmod 755 "$WORK/root/init"
cd "$ROOT"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$WORK/root/bin/isodo-guest" ./cmd/isodo-guest
(cd "$WORK/root" && find . -print0 | cpio --null -o -H newc --quiet | gzip -1 > "$WORK/initramfs.cpio.gz")
ISODO_CHV_INTEGRATION=1 ISODO_GUEST_INITRAMFS="$WORK/initramfs.cpio.gz" \
  CGO_ENABLED=0 go test ./internal/node/orchestrator -run '^TestRealCHVRuntimeCommands$' -count=1 -timeout 100s -v
