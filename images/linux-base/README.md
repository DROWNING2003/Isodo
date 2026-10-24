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

该命令调用真实 CHV Node 后端（直接调用服务实现，不经过公开 HTTP API），检查持久化创建与重复请求、绑定后 Inspect、停止保留写盘、删除写盘和资源预留释放。命令使用临时 Node 状态目录，完成后清理；缓存保留。

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
