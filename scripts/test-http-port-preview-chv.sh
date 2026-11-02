#!/usr/bin/env bash
# New RAM-root VM only; no existing Node, VM, network or state directory.
set -euo pipefail
readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
: "${ISODO_CHV_BINARY:?Path to Cloud Hypervisor required}"
: "${ISODO_GUEST_KERNEL:?Path to matching Linux kernel required}"
: "${ISODO_GUEST_MODULES:?Directory containing matching Vsock modules required}"
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && -w /dev/kvm ]] || {
 echo 'Requires Linux amd64 and /dev/kvm.' >&2;exit 1;
}
readonly WORK="$(mktemp -d /tmp/isodo-port-preview-build.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/root"/{bin,proc,sys,dev,modules,workspace,tmp,root}
cp "$(command -v busybox)" "$WORK/root/bin/busybox"
ln -s busybox "$WORK/root/bin/sh"
for module in vsock vmw_vsock_virtio_transport_common vmw_vsock_virtio_transport;do
 cp "$ISODO_GUEST_MODULES/$module.ko" "$WORK/root/modules/"
done
# Reuse the command fixture's identity/bootstrap; add only a loopback HTTP app.
sed '/\/bin\/isodo-guest &/i /bin/busybox ip link set lo up\n/bin/http-port-server &' "$ROOT/scripts/fixtures/runtime-commands-init.sh" > "$WORK/root/init"
chmod 755 "$WORK/root/init"
cd "$ROOT"
CGO_ENABLED=0 go build -trimpath -o "$WORK/root/bin/isodo-guest" ./cmd/isodo-guest
CGO_ENABLED=0 go build -trimpath -o "$WORK/root/bin/http-port-server" ./internal/node/orchestrator/testdata/http-port-server
(cd "$WORK/root" && find . -print0 | cpio --null -o -H newc --quiet | gzip -1 > "$WORK/initramfs.cpio.gz")
ISODO_CHV_INTEGRATION=1 ISODO_GUEST_INITRAMFS="$WORK/initramfs.cpio.gz" \
 CGO_ENABLED=0 go test ./internal/node/orchestrator -run '^TestRealCHVHTTPPortPreview$' -count=1 -timeout=100s -v
