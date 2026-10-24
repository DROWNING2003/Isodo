# 模板、实例磁盘与存储规格 v0.1

状态：2026-10-24，实现契约草案；正式镜像尚未构建，存储流程尚未验收。主要参考 E2B 的不可变模板和实例隔离思想；首版采用 CHV raw 磁盘、原节点 Stop/Start。内部预热 Guest 内存快照另见[预热模板启动规格](warm-template-startup.md)，不等于用户可见的产品 Snapshot，也不移植 E2B 的 Firecracker/NBD 格式。

模板资源与个人 public/private 权限见[模板服务](template-service.md)。模板工件统一保存在 SeaweedFS S3 私有 bucket；Node 本地目录是可校验缓存。

相关：[生命周期](sandbox-lifecycle.md)、[Guest](guest-protocol.md)、[网络安全](network-security.md)、[验证记录](validation.md)。本次已有 BusyBox initramfs 只是探针，不能作为满足本规格的正式模板。

## 1. 制品与生命周期

| 对象 | 所有者 | 可变性 | 回收条件 |
| --- | --- | --- | --- |
| Template manifest / kernel / rootfs | SeaweedFS S3 模板仓库 | 按 digest 不可变 | 无 live/stopped 实例、在途 operation、快照引用且超过保留窗口 |
| 实例系统盘 rootfs.raw | sandbox | 可写，保留用户 home/环境变化 | 仅 Create 未发布失败的安全补偿或明确 Delete |
| 实例 workspace.raw | sandbox | 可写、独立文件系统，挂载 /workspace | 与实例系统盘同生命周期 |
| Node staging/download | 在途操作 | 尚未发布 | 失败/取消后确认无使用者再回收 |
| 产品 Snapshot（后续） | 独立资源 | 发布后不可变 | 显式删除且无引用，不随实例 Delete 回收 |

首版没有独立 Volume API、对象存储备份承诺或用户可见的运行态快照 API。Node 内部可使用预热 VM 内存快照加速新实例启动；用户数据仍保存在每 Sandbox 独立的持久盘。一个沙箱停止后仍占实例磁盘和模板引用；停止只释放运行计算/网络资源。

## 2. 正式模板清单

系统默认模板由平台发布；用户通过模板服务构建/派生自己的个人模板，并选择 public/private。公开模板只开放服务层的发现和使用权限，底层 S3 bucket 始终私有。用户请求只选择已授权的 template/build，不提交任意宿主路径或下载 URL。版本名是展示/查找别名，Create 时解析成 manifest digest 固定保存，Start 不重新解析可变别名。

```json
{
  "schema_version": 1,
  "name": "ubuntu-agent",
  "version": "0.1.0",
  "arch": "amd64",
  "vmm": {"kind": "cloud-hypervisor", "tested_version": "v54.0"},
  "kernel": {
    "file": "vmlinux",
    "size_bytes": 123,
    "sha256": "<64 lowercase hex>"
  },
  "rootfs": {
    "file": "rootfs.raw",
    "size_bytes": 8589934592,
    "sha256": "<64 lowercase hex>",
    "format": "raw",
    "filesystem": "ext4",
    "partitioned": false
  },
  "workspace": {"filesystem": "ext4", "minimum_size_bytes": 4294967296},
  "guest_protocol": {"major": 1, "minimum_minor": 0},
  "guest_agent": {"version": "0.1.0-dev", "capabilities": ["ready.v1"]},
  "workload_user": {"name": "isodo", "uid": 1000, "gid": 1000},
  "minimum_memory_mib": 1024
}
```

以上 hash/size 中的占位信息不能直接发布；123 是示意长度。实际构建输出以准确字节数、哈希和测试报告替换。v54.0 仅通过基础启动/Vsock 探针，不意味着此模板已兼容通过。

manifest digest=原始 manifest.json 字节的 SHA-256；工件 hash 逐一覆盖内容。发布时固定 manifest 字节，不通过字段重排来重新生成相同“版本”。schema_version 不支持则拒绝，禁止对未知格式猜测。

首版 kernel/rootfs 是平铺文件名，不接受绝对路径、`..`、symlink、设备或 tar 解包任意路径。模板来源只能是管理员设置的本地仓库或受信任 HTTPS 制品源，制品必须匹配管理员发布的 manifest digest；哈希不能代替发布者信任和 TLS 验证。

## 3. Guest 基线和启动约定

- 首个支持目标为 Linux amd64，KVM、cgroup v2、CHV v54.0 候选版本；arm64 未验收前不发布相应模板，创建不相容架构返回 422。
- 使用可直接启动的匹配 kernel/raw rootfs，内核含 virtio block/net/vsock、ext4、cgroup v2 与安全路径解析所需能力。OCI 镜像不是可直接交给 CHV 的磁盘。
- Guest init/systemd 挂载可写根盘 `/dev/vda`，工作区盘 `/dev/vdb` 独立 ext4 挂载 `/workspace`。不使用根盘某目录冒充独立 workspace 文件系统。
- `/workspace` 不向 workload 暴露别名挂载，workload 无 mount 权限，避免 File RPC dirfd 被移出授权根。Guest Agent 在此检查成功后才 Ready。
- Guest Agent 以受控特权运行，仅为 workload fork 时降至 UID/GID 1000；workload 无 sudo、capabilities 或 Agent systemd unit 管理权限。User home 位于实例系统盘，命令环境遵守 Guest spec。
- Guest cgroup 单独划分 Agent 与 workload；Agent 能在 fork 前放入目标执行 cgroup、清理全部后代，并在自身重启时清理旧工作负载。cgroup 能力缺失不得假报 Ready。
- 平台启动 identity 通过 Node 生成的受限配置注入；可用只读、独立 per-boot config 盘（`/dev/vdc`，Guest root 只读访问）实现，不能修改共享模板。配置只含 sandbox/generation/boot_id、网络初始化和版本信息，不含永久凭证。
- per-boot 配置盘由 Node 生成，启动成功后保留到 VM 退出；系统盘和工作区同架构、驱动与挂载约定需一同验收。
- 模板不包含第三方密钥、宿主 SSH key、Nomad/数据库 token、历史任务或真实用户文件。首次冷启动初始化 machine-id、随机性和用户 home，避免所有实例共享身份。

安装系统包在模板构建阶段由管理员完成。workload 可在 home/workspace 安装用户级工具；不以开放 sudo 为手段支持任意安装。模板需预装演示所需 CLI，并记录其版本/来源和授权配置方法。

## 4. 宿主布局与路径所有权

```text
/var/lib/isodo/
  templates/sha256/<manifest_digest>/
    manifest.json
    vmlinux
    rootfs.raw
    verified.json
  staging/<download_id>/
  instances/<sandbox_id>/<ownership_generation>/
    inventory.json
    rootfs.raw
    workspace.raw
    boot/<boot_id>/config.raw
  node.db
/run/isodo/<sandbox_id>/<boot_id>/
  api.sock
  vsock.sock
```

sandbox/digest/boot ID 全部经服务端生成与校验，路径不接受用户自定义。实例目录 root-owned，不给租户宿主文件权限；VMM 专属 UID 只获自身盘和运行目录的必要访问。原始卷不能通过 Guest virtio-fs 共享宿主目录。

`inventory.json` 辅助现场核对，SQLite journal 为 Node 操作事实来源；两者更新不假定原子，要用 operation/revision 匹配并 reconcile。inventory 写临时文件+fsync+rename+目录 fsync；不因 inventory 缺失立即认定磁盘可以删除。

## 5. SeaweedFS 对象仓库、下载与缓存

开发环境通过 Compose 的 SeaweedFS 单节点服务提供 S3，默认 endpoint 为 `http://127.0.0.1:8333`，bucket 为 `isodo-templates`。平台存储配置由管理员固定；客户端不能覆盖 endpoint/bucket/key。配置和命令见 [SeaweedFS 部署说明](../deploy/seaweedfs/README.md)。

- PostgreSQL 保存 Template/Build 的 owner、visibility、状态、manifest digest 和工件引用；SeaweedFS 保存文件内容，Redis 不保存模板工件。
- 对象路径为 `templates/sha256/<manifest_digest>/<filename>`。先上传 kernel/rootfs 等文件，最后上传 `manifest.json` 作为完整发布标记；全部验证成功后才能将 DB Build 标为 Ready。
- 上传使用条件写，拒绝覆盖已有对象；重试命中已有对象时流式校验 SHA-256 和长度，不将 multipart ETag 当作 SHA-256。
- 使用 AWS SDK v2 S3 Transfer Manager 上传，64 MiB 分片、并发 2；支持大于 5 GiB 的磁盘工件，失败中止 multipart，后台 GC 还需处理进程崩溃遗留分片。
- 构建服务持有 bucket 范围写入凭证；Node 使用独立的 bucket 范围只读凭证。凭证仅保存在服务配置，不能打进模板/Guest、返回用户或写入日志。个人模板改为 public 不修改 bucket ACL。
- `/data` 持久卷同时保存 master、volume 与 filer 元数据；停止/重建容器保留卷。开发单节点不构成多副本或备份承诺。



1. 按 manifest digest 获取下载锁，记录在途引用。
2. 下载到随机 staging，不覆盖已发布 cache；校验文件类型、大小、hash、架构与 schema。
3. 完成 fsync 后原子 rename 发布完整模板目录，再更新 verified 状态与引用；Node 只有 VERIFIED 才允许实例化。
4. hash 不符隔离 staging，审计来源和错误；不回退到“差不多”的可变版本。
5. 重启时重新确认中断 staging，完整且校验通过可继续发布，其他按持有者与超时回收；不清理 live 下载。

下载限制：单模板缓存初始上限 16 GiB，可由管理员配置；连接超时 10 秒、无进展 30 秒、准备总预算 300 秒。禁止将工件整体读入内存，必须流式下载/校验。网络/容量失败明确报错，不将下载未完成 rootfs 交给 VMM。

只读模板目录按宿主权限保护，且不作为 VM 可写磁盘传入。定期完整校验与异常时重新校验；Verified 标记关联实际文件身份，发现变更撤销验证并停止新建，不去覆盖正在使用的实例盘。

## 6. 实例化与容量

实例系统盘由已校验 rootfs 在同一支持的文件系统 reflink 克隆，失败仅在明确不支持 reflink 时回退完整复制；ENOSPC/EIO/权限错误不能被当作“不支持”掩盖。普通文件硬链接不是可写实例隔离。

workspace.raw 新建固定虚拟大小 raw 文件并格式化 ext4；mkfs 仅在新盘且 journal 明确尚未发布时执行。Start 禁止再次 mkfs。系统盘首版尺寸由模板固定，不在线扩容；workspace 可在 Create 选择大小，创建后固定。

资源字段：`disk_gib` 表示系统盘+工作区的逻辑上限。首个基线模板系统盘 8 GiB、工作区至少 4 GiB，所以该模板 minimum disk_gib=12；disk_gib 省略由模板默认值确定，过小拒绝。memory 默认 2048 MiB、至少 manifest.minimum_memory_mib；vcpu 默认 2。工程默认不是商业套餐承诺。

Node 按两盘逻辑容量预留 tenant/node 磁盘额度，不按 reflink/sparse 初始 du 低估最坏增长；共享模板只计节点 cache，不重复计用户实例额度。主机缓存/staging/每次启动 config 盘额外预算由平台保留。

即使用 sparse 文件，也必须对磁盘配额和真实 free space 双重准入；磁盘不足时拒绝新 Create/Start/下载，运行实例保留并发告警，不能靠删 stopped 用户盘救急。管理员默认留出 max(20 GiB, 存储总量 15%) 空闲水位；正式配置根据节点容量显式给出。

宿主 ext4/btrfs/XFS 支持路径需要分别测试；不可在未经验证的网络文件系统上部署 Node journal/实例盘。CoW 宣传、创建耗时和真实分配空间都以实测为准。

## 7. Stop/Start、损坏与迁移边界

Stop 保留两盘与 template pin。正常 Guest shutdown 尽量 flush；强杀只标记 crash consistency。Start 由同节点、同锁定模板与 CHV 配置使用原盘，执行正常文件系统恢复；失败不得清空盘、覆盖模板或自动 format。

文件数据、home 下用户安装可保存，RAM/进程/PTY 不保存。Guest 内 rootfs/agent 被管理员改坏时不能承诺自动修复；节点离线则无法启动本地盘。磁盘没有远端副本时，节点永久丢失即存在数据丢失风险；不能将“Stop 保留”展示为备份。

升级模板不会原地替换已有实例系统盘；旧模板 pin 保留到实例 Delete。新模板用于新沙箱，用户迁移文件需显式流程。CHV 升级/回退须对旧实例盘和 kernel 组合验证，不默认运行中变更。

## 8. 删除、GC 与引用

- Delete 必须先确认 VMM unit/cgroup无活动消费者，再删实例盘和 per-boot config；删盘失败保持 cleanup_pending，不能先释放磁盘配额。
- 使用 journal inventory 精确删除实例拥有的文件；不跟随 symlink，不递归任意租户传入路径；路径/文件身份不符进入 quarantine。
- 删除盘后 fsync 相关父目录，更新占用与引用；回复丢失可同 operation 重试。Node 重启后以实物核对占用，不凭“曾调用 unlink”认定回收完成。
- template pin 覆盖 preparing/running/stopped/error 中仍保盘的实例、Create/Start/捕获 operation；“VM 没运行”不是 GC 条件。
- 模板 GC 先标 deleting，原子禁止新 pin，再二次核对引用；仅删除无引用且 idle 至少 24 小时的 VERIFIED 模板。下载 staging 超时需确认无活跃 operation 后才清理。
- schema/header/hash 错误的用户盘不能直接当垃圾；保存错误与人工恢复路径。首版不实现自动文件系统修复或数据抢救。

### 8.1 后续快照的术语

Workspace Checkpoint 只含工作区，不等于完整环境。产品 Snapshot 含模板 pin、系统盘变化与工作区，不含内存，恢复创建独立 sandbox 并重新授权连接。VM Memory Snapshot 另含 RAM/设备状态和匹配磁盘，必须锁架构/CPU/CHV/设备兼容性。

这三者沿用总体规格定义。首版不暴露用户可见的捕获 API；运行中复制 raw 文件不作为任何一致快照的实现。内部预热模板快照的构建和恢复契约见[预热模板启动规格](warm-template-startup.md)。对象存储产品快照仍需 manifest 原子提交、工件校验/引用、上传完成与恢复验收，再开放相应 capability。

## 9. 验收矩阵

| 场景 | 完成条件 |
| --- | --- |
| 正式模板构建→启动→Guest Ready | 版本/hash/内核/驱动/用户/cgroup/独立 workspace 均可核对 |
| 模板 hash/架构错误、下载中断 | 不启动 VMM，不发布半成品，重试不覆盖好 cache |
| 两实例分别改 rootfs/home/workspace | 互不污染，模板 hash 不变，禁止硬链接共享写层 |
| 无 reflink 文件系统 | 明确完整复制成功并记录耗时；ENOSPC 不伪装成 fallback 成功 |
| Stop/Start | 两盘原数据保留，boot_id 改变，旧进程失效，未重复 mkfs |
| 断电/磁盘损坏/缺失 | 明确错误，盘保留，不用空盘替代 |
| 大文件/磁盘配额/节点低水位 | 正确拒绝或 ENOSPC，无无界内存和静默丢数据 |
| Node 崩溃发生在克隆/发布/删盘中 | journal 可恢复，已发布盘不被 staging GC 清理 |
| stopped 实例仍引用旧模板 | 模板不能 GC；新版本不会改写旧实例 |
| Delete 与 Start/下载并发 | 先阻止新消费者，再停止/删盘，不释放未知资源额度 |
| File RPC 并发目录移动/符号链接 | 独立 fs 与安全解析边界有效，不能越出 workspace |
| 镜像/Guest 文件扫描 | 不含真实密钥、历史个人数据、跨实例共享 machine-id |

运行说明须列出镜像构建来源、准确工具版本、产物 hash、首个模板发布步骤、已验收组合和故障诊断位置。未完成本矩阵之前只报告模板设计，不将基础 initramfs 探针算作正式镜像交付。
