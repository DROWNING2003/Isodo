# Isodo 沙箱服务规格索引

2026-10-24。范围为 Linux CHV 沙箱服务；桌面端、T3 工作台与通用 Agent 会话集成延后。主要参考 E2B 的服务行为，CHV 官方接口决定底层事实，Cocoon 补充 CHV 工程实现经验。

## 实施规格

| 文档 | 固定内容 | 本次完成范围 |
| --- | --- | --- |
| [product-architecture.md](product-architecture.md) | 最终产品对象、产品与服务边界、部署形态、信任边界和 P0/后续阶段 | 产品架构入口；桌面端与 T3 延后 |
| [service-behavior.md](service-behavior.md) | 服务默认行为与参考取舍 | 行为基准 |
| [guest-protocol.md](guest-protocol.md) | Exec/PTY/Files 字段、流、重连、幂等、文件提交、错误/限额 | 详细协议与验收，附 [可编译 proto](../api/proto/isodo/guest/v1/guest.proto) |
| [guest-agent-runtime.md](guest-agent-runtime.md) | 首个模块 Guest Agent 的进程表、执行/回收、输出流、PTY master、断连与启动顺序 | 基于 Cocoon silkd/Agent 源码的实现规格；实现与 VM 验收未开始 |
| [sandbox-lifecycle.md](sandbox-lifecycle.md) | 状态转移、操作 API、operation、去重、所有权、Create 补偿、Stop/Delete、接管/GC/Nomad | 具体状态表、阶段/故障矩阵与默认预算 |
| [network-security.md](network-security.md) | API/mTLS 身份、IPv4/CIDR 默认/优先级、网络顺序、更新撤销、Secretless Gateway | 参数、API 行为、权限边界和安全负例验收 |
| [template-storage.md](template-storage.md) | manifest、Guest 基线、宿主布局、缓存、两盘、配额、Stop/GC、快照术语 | 模板字段、存储工作流与验收矩阵 |
| [isodo-sandbox-runtime-v0.1-spec.md](isodo-sandbox-runtime-v0.1-spec.md) | 总体架构、范围、参赛交付约束 | 架构入口，详细领域以以上规格为准 |
| [directory-responsibilities.md](directory-responsibilities.md) | 代码布局与依赖方向 | 目录职责，尚无完整 Runtime 代码 |

实施顺序与逐项验收见 [tasks/plan.md](../tasks/plan.md) 和 [tasks/todo.md](../tasks/todo.md)。任务全部待开始，已经存在的协议/探针仅按文档记录范围计入，不代表完整 Runtime 已完成。

所有工程默认值均为实现目标，不是商业套餐或已验证性能。正式接口发布前还需将公开 HTTP 契约和 Node RPC 机器定义按这些规格展开，不能用总体文档的早期伪代码代替 schema。

## 依据和验证

- [references.md](references.md)：参考仓库、固定 commit、许可证来源和本地路径。
- [research-e2b-behavior.md](research-e2b-behavior.md)：E2B 网络、Exec/PTY、Pause/Kill 源码核对。
- [research-cocoon-behavior.md](research-cocoon-behavior.md)：Cocoon SDK/Guest/Engine 层差异对照。
- [research-network-details.md](research-network-details.md)：E2B allow/deny、平台链、IPv6 和旧连接边界。
- [validation.md](validation.md)：已在 jfcs1024 完成的 CHV/Vsock 最小探针及剩余验证。
- [agent-integration.md](agent-integration.md)：T3 参考记录，当前延后。

## 契约使用规则

改动进程/PTY/Files 时同步 guest.proto 和 Guest 文档；改动状态、网络或磁盘语义时同步对应详细文档及验收矩阵。跨领域字段（sandbox_id、ownership_generation、revision、boot_id、guest_epoch）必须保持相同含义，不自行造第二份 ID/状态。

规格定义完成、protobuf 编译、源码核对与真实 Runtime 验收是不同证据；每次交付分别说明。实现和演示遵守 [参赛要求](../docs/competition-requirements.md)。
