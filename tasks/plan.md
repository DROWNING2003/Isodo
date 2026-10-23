# Isodo 沙箱服务实施计划

状态：待实施；依据 [产品架构规格](../specs/product-architecture.md) 与 [Runtime v0.1 详细规格](../specs/isodo-sandbox-runtime-v0.1-spec.md)。桌面端和 T3 工作台延后。

## 目标

先交付可由 API/测试客户端使用的 Linux + Cloud Hypervisor 沙箱服务：用户能创建隔离 VM、执行命令和 PTY、管理文件、停止后保留磁盘、重启后恢复管理，并在宿主出口执行网络策略。身份、连接授权和密钥必须处于 Guest 之外。每个阶段都以真实运行时证据验收，不把原型或设计规格当成实现。

## 架构决策

- 先做 Guest Agent Exec/PTY/Files 的端到端切片，因为它定义 Node 与 VM 的实际调用契约；PTY 由 Isodo 自行实现，不依赖 Cocoon shell session。
- Control API 保存租户期望状态及 operation；Node 保存本机 journal 并独立管理 CHV 生命周期。Nomad 部署平台守护进程，不管理单个 Sandbox。
- Node 与 Guest 通过 Vsock + 版本化 RPC 通信；公网 API 与 Node RPC 使用不同身份和信任边界。
- P0 的 Stop 是冷启动语义且保留原 Node 磁盘；Delete 才回收；产品 Snapshot、内存 Pause/Resume 和跨节点恢复延后。
- Guest Agent 和 Agent 均按不可信负载设计；平台凭证由 Guest 外 Gateway 授权调用，普通公网不授予凭证。
- 第一阶段使用预构建、按架构区分、可校验 digest 的 Guest 镜像。通用镜像构建流水线后续实现。
- 编码前先修复代码仓库与来源基线问题，建立符合参赛要求的 PR 和真实开发记录；不重写或清理被忽略的 `app/`。

## 依赖顺序

```text
T0 仓库来源 / v0 / PR 门禁
  └─ T1 接口、镜像和部署输入冻结
       └─ T2 Guest Exec 最小切片
            ├─ T3 Guest PTY 与 Files
            └─ T4 Node Supervisor / CHV / 存储 / 网络
                 └─ T5 Control API / 调度 / Operation
                      ├─ T6 Gateway 最小真实连接
                      └─ T7 集成、故障恢复、安全验证与交付
```

T3 与 T4 在 Guest RPC/镜像契约冻结后可以并行；T5 依赖稳定 Node RPC；T6 依赖 Sandbox 身份、策略和 Guest 外转发路径；T7 汇总所有 P0 切片。T0 未完成前不得向被混入的代码基线写入产品实现。

## 阶段

### Phase 0 — 可审计的开发基线

核清根仓库、`app/` 来源、原始提交与 `v0` tag 真实性，恢复或建立正确的 Isodo repo 和分支/PR 工作流，保留真实贡献记录。当前 Isodo 根目录无 `.git`；`app/` 是独立 T3 参考仓库且有大量未提交变更，不能直接开始往其中实现 Isodo。

**Checkpoint 0：** 有可核验的干净工作仓库、真实基线 tag 或赛方允许的处置记录；首个实现 PR 可创建并关联基线；参赛截止时区已核对。

### Phase 1 — 运行契约与制品基线

冻结公开 API、Node RPC、Guest 协议兼容策略；准备正式 Guest rootfs/kernel/init、镜像 manifest/digest、CHV 版本锁和开发/测试环境说明。现有 Guest proto 已编译，尚缺正式 HTTP/Node schema、镜像和生产配置。

**Checkpoint 1：** schemas 可生成、兼容规则明确；镜像可在指定 CHV/宿主上重复启动；部署假设和资源上限可复现。

### Phase 2 — Guest Agent 垂直切片

先完成 Guest Agent readiness、Exec（argv、stdout/stderr、exit code、deadline、kill、output cap）、然后 PTY（master fd、attach、输入/输出、resize、disconnect/re-attach）和受限 Files（原子写、下载、路径拒绝）。进程断开控制连接后仍由 Guest 所有，只有显式 kill 或 VM stop 才结束。

**Checkpoint 2：** 通过 Vsock 使用真实 Guest Agent；真实 bash PTY 可交互；重连、resize、Ctrl+C、文件边界和超时行为符合协议。

### Phase 3 — Node Runtime 垂直切片

实现本地 CHV adapter 与受限 API socket、VM 托管单元、实例盘创建/挂载/回收、guest Vsock relay、宿主出口策略以及可重启接管的 local journal/reconcile。采用 transaction/compensation，不能依赖 API allocation 生命周期保活 VM。

**Checkpoint 3：** Node 重启/更新不杀 VM；Stop 后盘数据保留，Start 冷启动可读，Delete 资源可核验清理；网络拒绝规则有真实负例。

### Phase 4 — Control API 与租户工作流

实现认证/授权、Tenant 限制、Sandbox CRUD、异步 Operation、幂等键、Node 选择和 RPC、状态 reconcile、审计及容量拒绝。异步请求超时不终止后台操作；用户只能读取和操作本租户 Sandbox。

**Checkpoint 4：** API 测试客户端完成 Create → Ready → Exec/PTY/Files → Stop/Start → Delete，状态与 CHV/Guest/磁盘证据一致。

### Phase 5 — 最小 Connection Gateway

实现一种真实模型出口及一种真实 MCP/工具出口。外部调用带 sandbox/connection/credential version 身份；Gateway 拒绝未授权连接、过期授权与策略外目的地，记录可关联审计。不将长期凭证注入 Guest 环境变量、文件或进程参数。

**Checkpoint 5：** 真实调用成功；撤销后后续调用失败；Guest 工件和进程环境未发现秘密；普通公网不等于已授权服务凭证。

### Phase 6 — 集成、运行验证与交付

完成 Nomad API/Gateway service 和 Node system jobs、升级/回滚检查、日志/健康/资源 GC、恢复演练、安全负例、运行说明、架构图、验收矩阵及真实 demo。桌面端暂不进入本计划。

**Checkpoint 6：** 全部 P0 验收有可复现命令与证据；mock、已实现和未验证能力在 README/演示中分开；参赛交付和仓库截止冻结清单逐项完成。

## 风险和处置

| 风险 | 影响 | 处置 |
| --- | --- | --- |
| 根目录无 Git，嵌套 `app/` 有大量改动 | 无法证明代码来源、贡献和比赛 `v0` 基线 | 实现门禁 T0；不清理/覆盖 `app/`，由用户确认合法来源后建立独立 Isodo 基线。 |
| 截止时间近且赛方时区未知 | 计划可能超出可交付时间 | 先核对赛方时区，按真实剩余时间收缩 P0；禁止用 mock 替代安全能力并宣称完成。 |
| CHV/Vsock 探针为固定命令且 root 运行 | 可能误以为 Guest Agent 或生产隔离已验证 | 每项状态按证据分层；正式 Guest、非特权/宿主防护、安全测试重新验收。 |
| Snapshot、桌面和服务 P0 混杂 | 核心沙箱延迟并重复定义 Stop/快照 | 按当前架构范围分阶段；Snapshot 和桌面保持后续阶段。 |
| 网络默认公网与密钥授权被混同 | Guest 可滥用平台身份/内网访问 | 宿主出口拒绝、Gateway 独立授权、元数据/内网负例测试为上线门槛。 |

## 尚需在实现前核实的输入

- 参赛要求中截止时间的实际时区，以及现阶段应按参赛交付还是长期产品目标排序。
- Isodo 根仓库应该从哪个可证明的原始 codebase 建立；`app/` 的参考 fork 是否只保存在本地、是否有可核验的导入记录。
- 目标 Linux/KVM 节点、CHV 发布版本与 Nomad 运行环境是否继续使用已验证节点作为首个测试宿主。
