# Linux base 模板

此目录构建内置 Isodo Guest Agent 的受控 OS image。Docker 构建只准备平台基础镜像；个人模板的构建命令应在隔离 Guest 内执行。

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o images/linux-base/isodo-guest ./cmd/isodo-guest
# 使用已核实的 OCI digest，不使用可变 tag 作为发布标识。
docker build --build-arg BASE_IMAGE='ubuntu@sha256:<verified-digest>' -t isodo-linux-base:0.1.0-dev images/linux-base
sudo sh scripts/build-base-template.sh /var/lib/isodo/template-candidates/base-001
```

候选目录必须不存在。打包需要 Docker、cpio、e2fsprogs、Python 3，以及与目标 Guest kernel 匹配的宿主 modules。脚本生成 sparse rootfs，清除机器身份和宿主 DNS 配置，并将源文件的精确大小/SHA-256 写入 manifest。initramfs 中仅打包启动必需模块和静态 BusyBox。

```sh
go build -o .tools/isodo-template ./cmd/isodo-template
.tools/isodo-template verify --dir /var/lib/isodo/template-candidates/base-001 --digest '<manifest-sha256>'
.tools/isodo-template probe --dir /var/lib/isodo/template-candidates/base-001 --digest '<manifest-sha256>' --vmm-binary /usr/local/bin/cloud-hypervisor
```

`verify` 是文件完整性检查；`probe` 需要 Linux/KVM，会创建自己的 VM/磁盘副本并清理。成功探针后通过下述平台发布命令完成 S3 上传和 Registry 事务，才能成为用户可选择的系统默认模板。

发布为系统模板（仅平台运维执行）：

```sh
# 环境包含 ISODO_DATABASE_URL、ISODO_TEMPLATE_S3_ENDPOINT/REGION/BUCKET
# 及 SEAWEEDFS_ACCESS_KEY / SEAWEEDFS_SECRET_KEY；不要将凭证写入命令行。
.tools/isodo-template publish-system --name linux-base \
  --dir /var/lib/isodo/template-candidates/base-001 \
  --digest '<manifest-sha256>' --vmm-binary /usr/local/bin/cloud-hypervisor
```

命令强制重新执行真实 Guest Ready 与关机探针，随后上传所有工件，最后原子创建 Ready build、`default` tag 和系统目录项。相同 manifest/bucket 的重试返回原 build；替换已有系统模板必须附带 `--expected-build-id '<current-build-id>'`，并发冲突不会移动默认指针。失败上传不会登记 Ready；已上传但未登记的工件保留，可供重试或后续 GC。

运行绑定与隔离探针：

```sh
.tools/isodo-template probe-bound --dir /var/lib/isodo/template-candidates/base-001 \
  --digest '<manifest-sha256>' --vmm-binary /usr/local/bin/cloud-hypervisor
# 或使用 Node 的只读 S3 凭证下载/验证缓存后启动：
.tools/isodo-template probe-bound --cache-dir /var/lib/isodo/template-cache \
  --digest '<manifest-sha256>' --vmm-binary /usr/local/bin/cloud-hypervisor
```

`probe-bound` 从同一不可变工件创建两台 VM，各自使用独立 rootfs/workspace 文件和 RuntimeIdentity。探针验证创建上下文取消后仍可 Ready、Guest epoch 不同、跨 Sandbox 重新绑定被拒绝、同身份重放绑定成功、ACPI 关机与 VMM 回收。缓存模式只读取 `SEAWEEDFS_READ_ACCESS_KEY` / `SEAWEEDFS_READ_SECRET_KEY`，不回退到写凭证。该探针不代表已经接通公开 Sandbox API 或完整多租户安全隔离。

验证 Node 后端完整操作流程：

```sh
.tools/isodo-template probe-node --cache-dir /var/lib/isodo/template-cache \
  --digest '<manifest-sha256>' --template-id '<published-template-id>' \
  --build-id '<published-build-id>' --vmm-binary /usr/local/bin/cloud-hypervisor
```

该命令调用真实 CHV Node 后端（直接调用服务实现，不经过公开 HTTP API），顺序检查两个实例的持久化创建、绑定后 Inspect、固定 Python 命令、重复请求、停止保留写盘、删除写盘和资源预留释放，并要求两次 FetchVerified 返回同一进程内校验凭据及不同 boot/Guest epoch。输出分别记录每次 create→Ready 与 create→首命令耗时；第一台包含该进程首次完整校验，第二台复用凭据。该顺序探针验证缓存与生命周期，并发双盘标记隔离另由 `probe-artifact` 验证。命令使用临时 Node 状态目录，完成后清理；缓存保留。

`make node NODE_BACKEND=chv` 现在启用上述 CHV 后端。需要 mTLS 配置、Linux/KVM、Cloud Hypervisor、`ISODO_NODE_STATE_DIR`、S3 endpoint/bucket 与 Node 只读凭证。当前支持 create/stop/delete 与无网卡的 egress deny；未实现磁盘保留后的 start、WatchOperation、自动进程恢复、VMM cgroup/UID 隔离或公开 API 调度回写。发现未完成 journal 或旧运行态记录时启动失败，必须先完成进程核对，不自动启动替代 VM。

快照候选使用的 Guest kernel 必须启用 VMGENID。打包脚本支持从独立解压目录读取内核/模块，无需安装宿主内核：

```sh
ISODO_BASE_KERNEL_VERSION=6.8.0-147-generic \
ISODO_BASE_MODULES_ROOT=/var/lib/isodo/guest-kernels/6.8.0-147/root \
  sh scripts/build-base-template.sh /var/lib/isodo/template-candidates/base-vmgenid
```

目录应包含对应的 `boot/vmlinuz-*`、`boot/config-*` 和已完成 `depmod -b` 的 `lib/modules/*`。支持 `.ko`、`.ko.zst`、`.ko.xz`、`.ko.gz`；所需模块缺失/加载失败会停止构建或启动。initrd 必须确认 VMGENID 的 ACPI 设备已绑定，再启动 Agent。内核 config 与版本随 manifest 固定并参与哈希验证。

本地运行态快照实验：

```sh
.tools/isodo-template probe-warm --dir /var/lib/isodo/template-candidates/base-vmgenid \
 --digest '<manifest-sha256>' --restore-mode copyonwrite \
 --vmm-binary /usr/local/bin/cloud-hypervisor
```

需要 `qemu-img`。`--restore-mode` 还支持 `copy`、`ondemand`，应独立复测后报告结果。实验冷启动一次无身份模板，暂停并保存内存/VM 状态，冻结源磁盘，为两个实例建立 QCOW2 写层，重写私有 config/vsock/serial 路径后恢复。通过实际私有内存映射确认 CoW，验证身份、随机样本和两个磁盘的标记隔离，执行固定 Python 探针并记录分段耗时。源快照保留至全部 VM 回收；该临时 PoC 不登记 Registry，也不替代公开创建链路或生产安全验收。

制作并验证自包含的持久化运行态模板：

```sh
.tools/isodo-template build-warm --dir /var/lib/isodo/template-candidates/base-vmgenid \
  --digest '<cold-source-manifest-sha256>' \
  --out-dir /var/lib/isodo/template-candidates/base-runtime \
  --vmm-binary /usr/local/bin/cloud-hypervisor
.tools/isodo-template probe-artifact --dir /var/lib/isodo/template-candidates/base-runtime \
  --digest '<new-warm-manifest-sha256>' --vmm-binary /usr/local/bin/cloud-hypervisor
```

输出目录必须不存在且位于冷源目录之外。新 manifest 固定暂停时的两盘种子、内存、CHV state/config、内核、精确 VMM binary 摘要及 CPU 指纹；`SourceDigest` 指向冷源 manifest，新 warm manifest 使用独立摘要。`probe-artifact` 经 `StartRuntime` 恢复两个实例，验证身份、随机样本及双盘写入隔离，报告完整校验与缓存恢复/首命令的分别耗时。

持久化工件沿用 `publish-system` 发布；它强制运行上述双实例探针再上传和 CAS 更新 Registry。Node 的 S3Store 首次获取完整验证，随后在同进程内用校验凭据复用只读缓存；Node 重启后重新验证。运行目录与缓存必须在同一文件系统以建立硬链接；资源规格、CPU 或 VMM binary 不匹配会明确拒绝，不回退冷启动。2026-10-24 的 v5 发布与 Node 验证记录见 [预热模板规格](../../specs/warm-template-startup.md)。
