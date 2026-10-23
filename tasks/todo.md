# Isodo 沙箱服务 TODO

状态：全部待开始（除规格/探针已有内容注明外）。任务顺序受 [实施计划](plan.md) 的依赖关系约束。勾选完成时必须补上 PR、验证命令和证据链接；仅有文档、编译或 mock 不算 Runtime 完成。

状态定义：`[ ]` 未开始，`[~]` 进行中，`[x]` 已按验收标准完成。优先级：P0 是当前服务可用和安全的核心；P1 是后续产品/平台能力；P2 是规模化增强。

## P0 — 开发基线门禁

### T0.1 核实 Isodo 仓库来源与可审计基线

- 状态：`[ ]` · 优先级：P0 · 依赖：无
- 现状：Isodo 根目录无 Git 元数据；`app/` 是本地 T3 参考仓库，包含大量未提交改动，且不是可直接采用的 Isodo 工作基线。
- 验收：
  - [ ] 记录 Isodo 正式 GitHub 仓库、上游来源、默认分支和可还原的原始提交。
  - [ ] 根据参赛要求核实并建立真实 `v0` tag；如果不能满足，记录赛方允许的书面处置，不伪造基线。
  - [ ] 建立每项变更对应 PR 的开发流程；首个实现 PR 能展示原始基线到新增贡献的差异。
- 验证证据：仓库 URL、`git show-ref`/tag 指向、PR URL 与 clean baseline 检查记录。
- 预计范围：L；需在代码实现前单独完成。

### T0.2 冻结开发环境与比赛交付门槛

- 状态：`[ ]` · 优先级：P0 · 依赖：T0.1
- 验收：
  - [ ] 核对截止日期采用的时区并明确功能冻结时间。
  - [ ] 锁定 Linux/KVM 测试宿主、CHV 版本、Go 工具链、Nomad 版本和复现账号/权限。
  - [ ] 列出源代码、运行说明、模块规格/职责、demo 视频和演示环境的交付 owner 与路径。
- 验证证据：可复现环境清单与交付清单；不提交凭证。
- 预计范围：S。

## P0 — 可运行的 Sandbox Runtime

### T1.1 冻结公开 API 与 Node RPC 契约

- 状态：`[ ]` · 优先级：P0 · 依赖：T0.1
- 规格依据：[Runtime 总体规格](../specs/isodo-sandbox-runtime-v0.1-spec.md)、[生命周期](../specs/sandbox-lifecycle.md)、[目录职责](../specs/directory-responsibilities.md)。
- 验收：
  - [ ] 生成版本化 OpenAPI 与 Node gRPC schema，覆盖 Sandbox、Operation、身份、幂等和失败语义。
  - [ ] 明确 Node RPC 的 owner generation、command revision、boot_id、guest_epoch 含义和兼容策略。
  - [ ] API 状态/错误码与详细生命周期规格一致；HTTP timeout 不取消持久 operation。
- 验证证据：schema lint/生成输出、契约测试与版本兼容说明。
- 预计范围：M。

### T1.2 构建可校验的 Guest 镜像

- 状态：`[ ]` · 优先级：P0 · 依赖：T1.1
- 验收：
  - [ ] 产出锁定 kernel、rootfs、init、Guest Agent 版本的构建配方和 manifest。
  - [ ] 镜像按 CPU 架构和 SHA-256 digest 不可变引用；启动时报告可核对的 guest version/capabilities。
  - [ ] 明确非 root Guest 用户、工作区路径、运行时目录与容量上限。
- 验证证据：可复现构建记录、digest、CHV 启动日志和 Guest readiness RPC。
- 预计范围：M。

### T2.1 Guest Exec 与进程登记

- 状态：`[ ]` · 优先级：P0 · 依赖：T1.1、T1.2
- 规格依据：[Guest Agent 运行时](../specs/guest-agent-runtime.md)、[Guest 协议](../specs/guest-protocol.md)。
- 验收：
  - [ ] `argv` 安全启动进程并返回稳定 execution ID、PID、开始时间和真实退出状态。
  - [ ] stdout/stderr 独立、有界流；支持 wait/list/attach/kill、超时、信号和输出截断标记。
  - [ ] 客户端断连只停止观察；命令超时或显式 kill 才终止进程组。
- 验证证据：运行在真实 VM 的契约测试覆盖成功、非零退出、超时、取消、断连重连和超限输出。
- 预计范围：M。

### T2.2 Guest PTY

- 状态：`[ ]` · 优先级：P0 · 依赖：T2.1
- 验收：
  - [ ] Guest Agent 自行管理 PTY master、子进程 session/process group 和终端尺寸；不假设平台提供 PTY RPC。
  - [ ] Create/Attach/Input/Resize/Signal/Wait/Disconnect/Kill 语义符合 Guest proto。
  - [ ] attach 断开后 PTY 进程继续运行；支持后续连接接收新输出；Kill/VM stop 可回收进程与 fd。
- 验证证据：真实 bash 交互、Ctrl+C、尺寸变化、断线重连、并发 attach 限制与资源回收测试。
- 预计范围：M。

### T2.3 Guest Files

- 状态：`[ ]` · 优先级：P0 · 依赖：T1.2
- 验收：
  - [ ] 支持路径受限的上传、下载、列目录和删除；目录穿越、符号链接越界和特殊文件访问拒绝。
  - [ ] 上传使用临时文件与原子提交；大小、并发和请求超限显式返回错误。
  - [ ] 文件 RPC 不访问宿主目录；Guest 可写范围仅含 Sandbox 工作目录和必要临时目录。
- 验证证据：路径攻击负例、权限/大小限制与真实磁盘读写集成测试。
- 预计范围：M。

### T3.1 Node CHV Supervisor 与本地 Journal

- 状态：`[ ]` · 优先级：P0 · 依赖：T1.1、T1.2、T2.1
- 验收：
  - [ ] Node 独立托管 VMM unit；CHV API socket 权限只授予对应 Node 进程。
  - [ ] journal 持久化 Sandbox owner/generation、VMM unit、socket、磁盘和阶段结果；创建/删除失败有补偿。
  - [ ] Node 重启后能识别并接管真实 VM；陈旧 generation 不得操作新 owner 的资源。
- 验证证据：注入 Node kill/restart、CHV shutdown/异常退出和重复 RPC 的恢复演练。
- 预计范围：L。

### T3.2 模板、实例盘与回收

- 状态：`[ ]` · 优先级：P0 · 依赖：T1.2、T3.1
- 验收：
  - [ ] 不可变模板以 digest 缓存；每个 Sandbox 拥有独立可写系统盘/工作区。
  - [ ] Stop 完成后盘保留；Start 为冷启动并换 boot_id；Delete 仅清理 Sandbox 拥有的资源。
  - [ ] 磁盘限额、剩余空间、并发下载和模板引用保护可核对。
- 验证证据：创建两个实例验证写隔离；Stop/Start 验证文件保留；Delete 后验证宿主资源清理。
- 预计范围：M。

### T3.3 Vsock relay 与宿主网络策略

- 状态：`[ ]` · 优先级：P0 · 依赖：T3.1、T2.1
- 验收：
  - [ ] 只允许 Node 连接其管理的 VM Guest Vsock CID/service；来宾不能访问宿主 CHV 控制 socket。
  - [ ] 默认公网出口；可关闭或限制目的地；阻止宿主、节点管理网、私网、link-local 和云元数据目标。
  - [ ] IPv6 按安全规格实现并默认禁用；DNS/重定向/既有连接策略行为有明确测试。
- 验证证据：在 Guest 中访问公网允许地址和宿主/私网/元数据拒绝地址的真实网络负例。
- 预计范围：L。

### T4.1 Control API Sandbox 与 Operation

- 状态：`[ ]` · 优先级：P0 · 依赖：T1.1、T3.1、T3.2
- 验收：
  - [ ] 实现认证、租户隔离、配额和 Create/List/Get/Start/Stop/Delete。
  - [ ] 写操作幂等并返回可查询 Operation；中途失败可重试/补偿，desired 与 observed state 分开。
  - [ ] API 不直接访问 CHV/宿主文件；Node 失联不会被错误表示成 Sandbox 停止。
- 验证证据：API 集成测试覆盖跨租户拒绝、重复请求、并发删除/启动、Node timeout 与状态对账。
- 预计范围：L。

### T4.2 Nomad 部署与生命周期恢复

- 状态：`[ ]` · 优先级：P0 · 依赖：T4.1、T3.1
- 验收：
  - [ ] API/Gateway 以 service job 部署，Node 以 system job 部署；不生成每 Sandbox 一个 Nomad job。
  - [ ] Node allocation 滚动更新/restart 不终止用户 VM；服务发现、健康检查、配置和降级策略明确。
  - [ ] 运维可观测 Node、VM、Guest readiness、磁盘配额和操作失败，日志不包含秘密。
- 验证证据：Nomad 更新/重启演练及 Sandbox 连续运行、Node 重新接管证据。
- 预计范围：M。

### T5.1 最小真实 Connection Gateway

- 状态：`[ ]` · 优先级：P0 · 依赖：T4.1、T3.3
- 验收：
  - [ ] 连接授权与凭证值分离；Guest 仅携 Sandbox/connection 受限身份，不携长期密钥。
  - [ ] 至少一种模型连接和一种真实 MCP/工具连接可经 Gateway 调用；授权撤销阻止后续调用。
  - [ ] 上游/目的地策略固定且可审计；禁止通用任意 URL/头注入凭证的开放代理。
- 验证证据：真实成功调用、撤销后的拒绝、审计关联及 Guest 环境/文件秘密扫描。
- 预计范围：L。

## P1/P2 — 后续产品能力

### T6.1 Snapshot 产品契约与对象存储

- 状态：`[ ]` · 优先级：P1 · 依赖：T3.2
- 验收：快照只代表一致的文件/环境工件，不包含运行内存或凭证；上传后创建独立 Sandbox，重新授权连接；保留策略和删除引用安全明确。

### T6.2 本地桌面端与 T3 工作台

- 状态：`[ ]` · 优先级：P1 · 依赖：桌面/本地安全架构另行立项，云端 API 契约稳定
- 验收：桌面、本地后端和云端 API 的认证、资源所有权、密钥存储、终端/文件通道与升级边界另立规格并通过平台安全评估。当前延后，不纳入服务 P0。

### T6.3 内存快照、Warm Pool 与多节点恢复

- 状态：`[ ]` · 优先级：P2 · 依赖：T3.1、T3.2、T6.1
- 验收：每项能力独立 ADR/规格、性能基线、一致性模型、故障恢复与容量成本测量完成后再实现。

## P0 交付关卡

- [ ] T0 基线门禁完成，可审计 PR 和贡献历史。
- [ ] Guest、Node、Control API 的真实 Sandbox 垂直链路通过。
- [ ] 网络拒绝与 Gateway 撤销负例通过。
- [ ] Nomad 更新和 Node 恢复符合规格。
- [ ] README、运行说明、架构/模块职责、演示环境和视频按参赛要求逐项核对。
- [ ] 所有能力按已实现、mock、未验证分类，保留复现命令和证据链接。
