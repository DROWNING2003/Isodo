# 预热模板与快照启动规格 v0.1

状态：实现进行中。磁盘 base 工件、S3 发布/只读获取、双 VM 身份绑定及 CHV Node create/stop/delete 后端已有真实验证。现行 VM 路径仍为冷启动；Pause/Resume/Snapshot/Restore 客户端原语已有代码，且已完成两实例 Runtime Snapshot + CoW PoC，但尚未完成持久化快照产物、生产创建路径与完整安全验收。公开 Control API dispatch、独立网络、恢复和加速仍未完成。

## 1. 目标与范围

- 创建模板 VM，启动正式 Guest Kernel、rootfs 与 Guest Agent，等到模板态 Ready。
- 暂停 VM 并保存 CHV VM state 与 guest memory，形成节点本地、按 digest 固定的预热模板。
- 新 Sandbox 从模板快照恢复；CHV 使用 `memory_restore_mode=OnDemand` 按访问加载内存页。
- 恢复后的 VM 必须先绑定新的 `sandbox_id`、`ownership_generation`、`boot_id` 和 `guest_epoch`，Node 验证身份后才发布 Guest Ready 或接受用户工作负载。
- 首阶段只支持 Linux amd64、单 Node、本地文件系统快照；不包含对象存储、跨节点复制、公开 Snapshot API、Firecracker provider 或 Lima provider。

本规格中的预热 VM 快照是平台内部的可启动模板，不是用户可见的产品 Snapshot，也不替代持久化实例盘。模板应在无租户身份、无用户数据、无永久凭证的状态下制作。

## 2. 安全与身份重绑定

VM memory snapshot 会复制 Guest Agent 的进程状态。不能把制作模板时的 RuntimeIdentity、进程 ID、PTY、执行记录或连接状态当作新 Sandbox 的身份。

Guest Agent 必须支持模板态和实例态：

1. 模板构建时 Agent 进入 template-ready，Ready 不带可用于 Sandbox API 的 RuntimeIdentity，且拒绝 StartProcess、Files 和 PTY 等租户工作请求。
2. restore 后，Node 通过专用 Guest RPC 发送 Node 新生成的 RuntimeIdentity。Agent 只允许从未绑定的模板态执行一次绑定；重复绑定或已绑定实例的身份变更均拒绝。
3. 绑定时 Agent 生成新的 guest_epoch，清理/确认没有继承的 workload、PTY、执行结果、订阅和 writer token；workspace 由实例私有盘提供且在发布前为空或按模板声明初始化。
4. Node 重新读取 Ready，逐字段比对 identity、Guest protocol、版本和能力。任何失败都不得发布路由或报告 Guest Ready。
5. Node 完成网络、实例盘与 Guest 身份准备前，恢复的 VM 不能被租户访问。CHV API socket 和 Guest Vsock socket 保持 Node 私有。

若 Guest Agent 尚不支持模板态/身份绑定，则该模板能力不可启用；不能用固定或伪随机写在模板中的 Sandbox ID 规避。

## 3. CHV 恢复契约

- 快照前必须让 Guest 到达 template-ready，并暂停 VM；不对运行中的 Guest 直接复制 RAM 文件。
- 快照包含 CHV 版本、VM 配置、device state、memory ranges 与模板盘引用；发布前验证所有文件完整且 digest 已固定。
- restore source 必须位于 Node 管理的绝对路径，URI 由服务端生成；请求不能接受用户提供的 host path 或 URL。
- 默认使用 CHV `OnDemand` memory restore。恢复路径不支持时返回明确错误，不静默改成 `copy`。
- Node 先在隔离的临时目录准备每 Sandbox 独立的磁盘写层和网络设备，再启动恢复；模板只读且不能作为多个实例的可写共享盘。
- CHV/内核版本、架构、CPU 配置和设备集不兼容时拒绝 restore。首阶段模板与 Node 的 CHV major/minor 必须相同，升级兼容矩阵另立任务。
- 快照创建与恢复会读写 RAM 文件，VMM 请求使用有界 operation deadline；失败保留模板源，临时产物仅在确认没有 VMM 消费后回收。

## 4. API 与生命周期

用户仍通过 `POST /sandboxes` 创建 Sandbox。Control API 持久化异步 operation，Node 在目标节点恢复模板 VM、绑定身份、验证 Guest Ready，随后再发布运行路由。HTTP 请求成功不能早于 durable operation 接收；不能因 API deadline 取消 Node 已接受的恢复。

首版 Create 仍分配独立系统盘与 workspace。模板快照只减少 Guest 启动和 Agent 初始化时间，不把模板内存状态当作用户持久盘，也不代表完整产品 Snapshot 能力。

失败补偿遵守 [生命周期规格](sandbox-lifecycle.md)：在确认 VMM 已停止后清理该 operation 创建的临时 VM、网络和实例写层；模板本身不可被 Create 补偿删除。结果未知时保留资源并标记 cleanup_pending。

## 5. 性能指标

E2B 对照（本地 `reference.e2b-infra` HEAD `6b82caf69`）：普通创建/恢复流程在 `packages/orchestrator/pkg/sandbox/sandbox.go` 调用 `WaitForEnvd`，成功后才 `MarkRunning`；`SkipEnvdWait` 是 GDB 专用例外。JS SDK 的 `Sandbox.create()` 返回后可直接调用 commands/files；`Sandbox.connect(id)` 用于已有实例的重新连接或暂停恢复，不是创建后的必需步骤。这里的基础设施 Ready 不保证用户另行启动的 HTTP server、数据库或自定义应用已经就绪。

Isodo 的 durable operation 接收与可运行状态必须分开：如提前返回，只能表示异步受理（202/operation），不能把 VM 刚建出来报告为 Running。对外同步创建成功语义应等到身份绑定、Guest Ready 与运行路由可用；SDK 不要求用户紧接着再调用 connect 才能执行命令。

统一测量从 Create API 接收时间到 Node 验证身份并发布 Guest Ready，分别报告模板首次冷启动、模板 snapshot 构建、warm snapshot restore 和完整 API Create。记录 p50/p95、CHV/Guest/宿主版本、模板 digest、内存大小、节点缓存状态和 UFFD restore mode。

约 1 秒是对照 E2B 的产品体验目标，不是当前实现或既定 SLA。冷启动探针的 20.27 秒、CHV `vm.boot` 调用的 184ms 不可互换或冒充快照恢复指标。

## 6. 完成条件

- [ ] Guest Agent template-ready / BindRuntime 协议及实现完成；绑定后身份唯一，模板 Guest 未绑定前不能运行租户 workload。
- [ ] 管理员可在 Linux KVM 节点生成可校验、无秘密的预热模板。
- [ ] Node 从模板恢复到 Guest Ready，并为多个 Sandbox 建立不同 RuntimeIdentity 与独立写盘。
- [ ] 失败/重试/Node 重启不会复用旧 identity、丢失模板或误删其他实例资源。
- [ ] 真实 CHV v54、目标宿主内核与本地文件系统完成 snapshot/restore 验证，明确确认 CHV 使用 OnDemand/UFFD。
- [ ] 同口径多轮基准报告 Create API→Guest Ready p50/p95，并与冷启动对照。
- [ ] Control API `POST /sandboxes` 已连接 Node durable operation；元数据 CRUD 不再被描述为真实沙箱创建。

## CHV Node 后端接入进度与约束

`cmd/isodo-node` 的 CHV 分支使用真实后端，接收固定 template/build/artifact 引用；只允许配置中的 bucket 和规范 `templates/sha256/<digest>` 前缀，不在 Node 解析用户可变 tag。接受操作前持久化 intent，按 Sandbox generation/revision 拒绝旧请求；同一 operation ID 必须匹配完整请求内容。创建即预留 CPU/内存，停止/删除或创建失败后释放。

当前实现 create/stop/delete，Guest Ready 身份核对通过后才将 Node operation 标为成功；停止保留独立写盘，删除回收写盘。仅支持无网卡的 egress deny。Node 状态目录持有跨进程排他锁，CHV 持久化边界保存 protobuf 副本，worker 不与读取方共享可变消息。失败信息使用固定摘要，内部诊断不直接进入 operation 响应。

未完成：保留磁盘后的 start、WatchOperation、Node 崩溃后的进程/cgroup 核对与恢复、独立 VMM UID/cgroup、Control API durable dispatch 与状态回写、Gateway 路由发布。重启发现未完成操作或旧运行态记录时拒绝启动，不将其自动视为已恢复。上述约束不能被本地成功探针替代验收。


## 运行态模板调研核对（2026-10-24）

- E2B 的常规快速路径恢复预启动快照，结合 UFFD、RootFS CoW 和热页预取；filesystem-only 模板仍可以走冷启动。来源：[Runtime 架构](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)。
- CHV v54 `ondemand` 使用 userfaultfd；`copyonwrite` 通过私有文件映射和宿主页缓存按需加载，两者不等同。CoW 遇到 shared/hugepages 等配置会退回 eager copy，应检查实际模式并将回退样本单独归类。内存快照文件必须在所有恢复 VM 生命周期内保持不变。来源：[v54 snapshot/restore](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/v54.0/docs/snapshot_restore.md)。
- CHV snapshot 文件保存内存与设备/VM 状态，所引用的外部磁盘还需在同一暂停一致性点单独固定，再为实例派生独立写层；不能把普通 snapshot 目录视为已经完整复制磁盘。
- CHV VM Generation ID 在恢复时变化，Guest 需启用 `CONFIG_VMGENID=y`，或在 initrd 加载匹配的 `CONFIG_VMGENID=m` 模块并确认 ACPI 绑定，才能将变化混入内核随机池。2026-10-24 核对当前基础候选来源 `/boot/config-5.15.0-46-generic` 未找到该配置。现有冷启动的不同 Guest epoch 不证明快照克隆随机性安全；运行态模板实验必须先更换/验证 Guest kernel，并检查应用自有 PRNG、clock、连接与执行记录。
- 本项目记录的 2–6 秒是 `vm.boot` 成功后到 Guest Ready/BindRuntime 的局部区间，排除了下载、完整工件哈希和写盘准备；完整探针曾耗时数十秒至数分钟。不能将其和 Create→首条命令返回计算倍率。
- Lizard 2026-10-12 的 E2B P50 264ms、P95 603ms 是其发布的 Create→首个 Python 输出样本；Create 本身 P50 162ms。不同提供商的区域/规格/模板不同，该结果不是本项目复测。来源：[原始基准说明](https://lizard.build/blog/e2b-vs-daytona-vs-modal)。

下一阶段依次验证：受控无绑定模板暂停与磁盘一致性 → snapshot 单实例 Copy 恢复 → 两个实例独立写盘/身份/随机性 → OnDemand 与 CopyOnWrite 对照 → 缓存命中/未命中的完整 API→首条 Exec 分位数。先完成正确性与少量样本，稳定后再跑重复基准；不把 200–500ms 预先写成已验证能力或 SLA。


### 已验证的 CoW PoC

真实 isodo-test 节点上，两次 Snapshot + Memory CoW + QCOW2 写层派生到首条固定 Python 命令完成分别为 867ms、781ms；CHV restore 调用分别为 37ms、49ms。确认 `/proc/<pid>/maps` 的私有内存文件映射、不同 Guest epoch/随机样本、rootfs/workspace 标记不串写以及 ACPI 清理。一次性准备模板耗时 52.55 秒，局部克隆指标不含 API、S3 分发与调度。

工件已换用包含 VMGENID 模块的 6.8.0-147 Guest kernel，仍未发布替换系统目录的 base build。运行态快照目前只存在于 PoC 临时目录，尚未作为持久化产物接入生产创建路径。详细证据见 [CoW 快照验证](../artifacts/validation/chv-warm-snapshot-20261024/README.md)。两次样本不能作为分位数或 SLA。

### 持久化运行态产物契约

运行态模板应成为独立不可变 build：manifest 同时固定 kernel/config、暂停时的 rootfs seed、workspace seed、CHV config/state、memory-ranges，以及源 manifest digest、精确 VMM binary digest、Guest 资源规格和宿主 CPU 兼容指纹。所有文件使用扁平文件名、精确大小和 SHA-256，最后发布 manifest；不依赖构建临时目录中的文件。

Node 首次获取/启动时做完整验证。后续创建可复用进程内的已验证只读工件凭据，凭据由校验器创建，使用前检查目录/文件 inode、大小、mtime 和只读权限；变化时必须重新校验，不能仅凭缓存目录存在便跳过哈希。凭据不跨 Node 进程重启持久化。生产 Snapshot restore 不应每次重复读取十几 GiB 模板，否则亚秒级 VMM restore 会被准备阶段淹没。

恢复必须匹配 manifest 记录的 CHV binary、CPU 指纹和资源规格，重写实例私有的磁盘、kernel/initramfs、Vsock、serial 路径，并保留内存/磁盘种子至 VM 回收。初版按严格兼容性拒绝不匹配；不能静默把不兼容运行态当作成功恢复。
