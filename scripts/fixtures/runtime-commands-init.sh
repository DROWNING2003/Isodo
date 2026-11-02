#!/bin/sh
set -eu
/bin/busybox mount -t devtmpfs devtmpfs /dev
/bin/busybox mount -t proc proc /proc
/bin/busybox mount -t sysfs sysfs /sys
/bin/busybox insmod /modules/vsock.ko
/bin/busybox insmod /modules/vmw_vsock_virtio_transport_common.ko
/bin/busybox insmod /modules/vmw_vsock_virtio_transport.ko
/bin/busybox --install -s /bin
mkdir -p /workspace /root /tmp
for item in $(cat /proc/cmdline); do
 case "$item" in
 isodo.sandbox_id=*) export ISODO_SANDBOX_ID="${item#*=}" ;;
 isodo.ownership_generation=*) export ISODO_OWNERSHIP_GENERATION="${item#*=}" ;;
 isodo.boot_id=*) export ISODO_BOOT_ID="${item#*=}" ;;
 isodo.guest_epoch=*) export ISODO_GUEST_EPOCH="${item#*=}" ;;
 esac
done
export PATH=/bin
/bin/isodo-guest &
while true; do /bin/busybox sleep 3600; done
