#!/bin/sh
set -eu
base=/root/zkp/isodo-validation
cd "$base"
mkdir -p root/bin root/dev root/proc root/sys root/modules
cp /usr/bin/busybox root/bin/busybox
ln -sf busybox root/bin/sh
gcc -static -O2 -Wall -Wextra vsock-probe.c -o root/probe
cp /boot/vmlinuz-$(uname -r) kernel
for module in vsock vmw_vsock_virtio_transport_common vmw_vsock_virtio_transport; do
 cp "$(modinfo -n "$module")" root/modules/
done
cat > root/init <<'INIT'
#!/bin/sh
/bin/busybox mount -t devtmpfs devtmpfs /dev
/bin/busybox mount -t proc proc /proc
/bin/busybox mount -t sysfs sysfs /sys
/bin/busybox insmod /modules/vsock.ko
/bin/busybox insmod /modules/vmw_vsock_virtio_transport_common.ko
/bin/busybox insmod /modules/vmw_vsock_virtio_transport.ko
/probe
while true; do /bin/busybox sleep 3600; done
INIT
chmod 755 root/init
(cd root && find . -print0 | cpio --null -o --format=newc) > initramfs.cpio
sha256sum bin/cloud-hypervisor kernel initramfs.cpio > artifacts.sha256
