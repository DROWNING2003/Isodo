#!/bin/sh
# Operator-only Linux image packaging. User recipes are never executed here.
# Default input is the complete base workspace image. For Guest-only development
# artifacts, explicitly set ISODO_BASE_IMAGE and ISODO_BASE_NAME=linux-base.
set -eu
if [ "$(id -u)" != 0 ]; then echo 'Run base packaging as root on the Linux build host.' >&2; exit 1; fi
if [ "$#" != 1 ]; then echo 'usage: build-base-template.sh NEW_OUTPUT_DIRECTORY' >&2; exit 1; fi
isodo_output=$(realpath -m "$1")
isodo_image=${ISODO_BASE_IMAGE:-isodo-base:0.1.0-dev}
isodo_kernel_version=${ISODO_BASE_KERNEL_VERSION:-$(uname -r)}
isodo_modules_root=${ISODO_BASE_MODULES_ROOT:-/}
isodo_kernel=${ISODO_BASE_KERNEL:-$isodo_modules_root/boot/vmlinuz-$isodo_kernel_version}
isodo_kernel_config=${ISODO_BASE_KERNEL_CONFIG:-$isodo_modules_root/boot/config-$isodo_kernel_version}
isodo_allow_no_vmgenid=${ISODO_ALLOW_NO_VMGENID:-false}
isodo_vmgenid_required=1
if ! grep -Eq '^CONFIG_VMGENID=(y|m)$' "$isodo_kernel_config"; then
    if [ "$isodo_allow_no_vmgenid" != "true" ]; then
        echo 'Guest kernel must enable VM Generation ID before packaging a snapshot-capable base.' >&2
        exit 1
    fi
    isodo_vmgenid_required=0
    echo 'Warning: guest kernel has no VMGENID; packaging a cold-boot-only base.' >&2
fi
isodo_name=${ISODO_BASE_NAME:-base}
isodo_version=${ISODO_GUEST_VERSION:-0.1.0-dev}
isodo_hypervisor=${ISODO_CH_VERSION:-v54.0}
isodo_rootfs_size=${ISODO_ROOTFS_SIZE:-8G}
isodo_temp=$(mktemp -d)
isodo_container=
cleanup() {
    if [ -n "$isodo_container" ]; then docker rm "$isodo_container" >/dev/null; fi
    rm -rf "$isodo_temp"
}
trap cleanup EXIT
mkdir "$isodo_output"
isodo_root="$isodo_temp/rootfs"
isodo_initrd="$isodo_temp/initramfs"
mkdir -p "$isodo_root" "$isodo_initrd/bin" "$isodo_initrd/modules" "$isodo_initrd/dev" "$isodo_initrd/proc" "$isodo_initrd/sys" "$isodo_initrd/newroot"
isodo_container=$(docker create "$isodo_image")
docker export "$isodo_container" | tar --numeric-owner -xp -C "$isodo_root"
if [ "$isodo_name" = base ]; then
    for required in sbin/isodo-t3 opt/isodo/t3/bin.mjs opt/isodo/t3/client/index.html; do
        if [ ! -s "$isodo_root/$required" ]; then
            echo "Default base requires the complete T3 workspace image; missing $required." >&2
            exit 1
        fi
    done
fi
# Host DNS/machine identity never becomes part of a published base template.
[ ! -L "$isodo_root/etc" ]
rm -f "$isodo_root/etc/resolv.conf" "$isodo_root/etc/hosts" "$isodo_root/etc/hostname" "$isodo_root/etc/machine-id"
printf '127.0.0.1 localhost\n' > "$isodo_root/etc/hosts"
printf 'isodo\n' > "$isodo_root/etc/hostname"
: > "$isodo_root/etc/resolv.conf"
docker cp "$isodo_container:/bin/busybox" "$isodo_initrd/bin/busybox"
ln -s busybox "$isodo_initrd/bin/sh"
: > "$isodo_temp/module-dependencies"
isodo_module_list='virtio_pci virtio_blk virtio_net ext4 vmw_vsock_virtio_transport'
if grep -q '^CONFIG_VMGENID=m$' "$isodo_kernel_config"; then isodo_module_list="$isodo_module_list vmgenid"; fi
for module in $isodo_module_list; do
    modprobe --dirname "$isodo_modules_root" --set-version "$isodo_kernel_version" --show-depends "$module" >> "$isodo_temp/module-dependencies"
done
awk '$1 == "insmod" && !seen[$2]++ {print $2}' "$isodo_temp/module-dependencies" > "$isodo_temp/modules-unique"
: > "$isodo_initrd/module-order"
while IFS= read -r module; do
    name=$(basename "$module")
    case "$name" in
        *.ko) cp "$module" "$isodo_initrd/modules/$name" ;;
        *.ko.zst) name=${name%.zst}; zstd -q -d -c "$module" > "$isodo_initrd/modules/$name" ;;
        *.ko.xz) name=${name%.xz}; xz -d -c "$module" > "$isodo_initrd/modules/$name" ;;
        *.ko.gz) name=${name%.gz}; gzip -d -c "$module" > "$isodo_initrd/modules/$name" ;;
        *) echo "Unsupported kernel module: $name" >&2; exit 1 ;;
    esac
    printf '%s\n' "$name" >> "$isodo_initrd/module-order"
done < "$isodo_temp/modules-unique"
cat > "$isodo_initrd/init" <<'INIT'
#!/bin/sh
set -eu
/bin/busybox mount -t devtmpfs devtmpfs /dev
/bin/busybox mount -t proc proc /proc
/bin/busybox mount -t sysfs sysfs /sys
while IFS= read -r module; do /bin/busybox insmod "/modules/$module"; done < /module-order
isodo_vmgenid_bound=0
for attempt in $(/bin/busybox seq 1 50); do
    for device in /sys/bus/acpi/drivers/vmgenid/VMGENCTR*; do
        if [ -e "$device" ]; then isodo_vmgenid_bound=1; break; fi
    done
    [ "$isodo_vmgenid_bound" = 1 ] && break
    /bin/busybox sleep 0.1
done
if [ "$isodo_vmgenid_bound" != 1 ]; then echo 'isodo-init: VM Generation ID driver did not bind' >&2; exit 1; fi
echo 'isodo-init: VM Generation ID driver bound'

for attempt in $(/bin/busybox seq 1 100); do
    [ -b /dev/vda ] && break
    /bin/busybox sleep 0.1
done
/bin/busybox mount -t ext4 -o rw /dev/vda /newroot
/bin/busybox mount --move /dev /newroot/dev
/bin/busybox mount --move /proc /newroot/proc
/bin/busybox mount --move /sys /newroot/sys
exec /bin/busybox switch_root /newroot /sbin/isodo-init
INIT
if [ "$isodo_vmgenid_required" = 0 ]; then
    python3 - "$isodo_initrd/init" <<'PY'
import sys
from pathlib import Path
path = Path(sys.argv[1])
text = path.read_text()
start = text.index('isodo_vmgenid_bound=0')
end_marker = "echo 'isodo-init: VM Generation ID driver bound'\n"
end = text.index(end_marker, start) + len(end_marker)
path.write_text(text[:start] + text[end:])
PY
fi
chmod 0755 "$isodo_initrd/init"
cp "$isodo_kernel" "$isodo_output/vmlinux"
cp "$isodo_kernel_config" "$isodo_output/kernel.config"
(cd "$isodo_initrd" && find . -print0 | cpio --null --quiet -o --format=newc) > "$isodo_output/initramfs.cpio"
truncate -s "$isodo_rootfs_size" "$isodo_output/rootfs.raw"
mkfs.ext4 -F -q -m 0 -E lazy_itable_init=0,lazy_journal_init=0 -L isodo-root -d "$isodo_root" "$isodo_output/rootfs.raw"
python3 - "$isodo_output" "$isodo_name" "$isodo_version" "$isodo_hypervisor" "$isodo_kernel_version" <<'PY'
import hashlib,json,sys
from pathlib import Path
root=Path(sys.argv[1])
def artifact(name):
    path=root/name
    checksum=hashlib.sha256()
    with path.open('rb') as source:
        for chunk in iter(lambda: source.read(1<<20), b''): checksum.update(chunk)
    digest=checksum.hexdigest()
    return {'file':name,'size_bytes':path.stat().st_size,'sha256':digest}
manifest={'schema_version':1,'name':sys.argv[2],'version':sys.argv[3],'arch':'amd64',
 'vmm':{'kind':'cloud-hypervisor','tested_version':sys.argv[4]},'kernel':artifact('vmlinux'),
 'kernel_config':artifact('kernel.config'),'kernel_version':sys.argv[5],
 'initramfs':artifact('initramfs.cpio'),'rootfs':{**artifact('rootfs.raw'),'format':'raw','filesystem':'ext4','partitioned':False},
 'workspace':{'filesystem':'ext4','minimum_size_bytes':4<<30},'guest_protocol':{'major':1,'minimum_minor':0},
 'guest_agent':{'version':sys.argv[3],'capabilities':['ready.v1']},
 'workload_user':{'name':'isodo','uid':1000,'gid':1000},'minimum_memory_mib':512}
data=(json.dumps(manifest,sort_keys=True,separators=(',',':'))+'\n').encode()
(root/'manifest.json').write_bytes(data)
print('Unpublished candidate manifest SHA-256:',hashlib.sha256(data).hexdigest())
PY
chmod 0400 "$isodo_output/kernel.config" "$isodo_output/manifest.json" "$isodo_output/vmlinux" "$isodo_output/initramfs.cpio" "$isodo_output/rootfs.raw"
echo 'Candidate packaged; Guest Ready verification and S3/Registry publication are still required.'
