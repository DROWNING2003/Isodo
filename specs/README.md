# Isodo 沙箱服务规格索引

2026-10-24。首个服务实现范围为 Linux CHV；桌面端、T3 工作台和通用 Agent 会话仍延后。本地 Lima/T3 文档是可行性调查，不改变首版实现范围。主要参考 E2B 的服务行为，CHV 官方接口决定底层事实，Cocoon 补充 CHV 工程实现经验。

## 实施规格

| 文档 | 固定内容 | 本次完成范围 |
| --- | --- | --- |
| [product-architecture.md](product-architecture.md) | 最终产品对象、产品与服务边界、部署形态、信任边界和 P0/后续阶段 | 产品架构入口；桌面端与 T3 延后 |
| [service-behavior.md](service-behavior.md) | 服务默认行为与参考取舍 | 行为基准 |
| [guest-protocol.md](guest-protocol.md) | Exec/PTY/Files 字段、流、重连、幂等、文件提交、错误/限额 | 详细协议与验收，附 [可编译 proto](../api/proto/isodo/guest/v1/guest.proto) |
| [guest-agent-runtime.md](guest-agent-runtime.md) | 首个模块 Guest Agent 的进程表、执行/回收、输出流、PTY master、断连与启动顺序 | 基于 Cocoon silkd/Agent 源码的实现规格；实现与 VM 验收未开始 |
| [sandbox-lifecycle.md](sandbox-lifecycle.md) | 状态转移、操作 API、operation、去重、所有权、Redis active registry/routing、Create 补偿、Stop/Delete、接管/GC/Nomad | 具体状态表、阶段/故障矩阵与默认预算 |
| [control-node-contract.md](control-node-contract.md) | Control API/Worker 到 Node Orchestrator 的 mTLS gRPC、异步 operation、Redis active record/index 与 Node routing record | 接入顺序、职责所有权、恢复与错误语义；尚未实现 |
| [sandbox-endpoints.md](sandbox-endpoints.md) | 应用链接/端口预览的 URL 形状、app-link/preview 契约、generation、token/cookie 与 Lima/CHV 职责 | `POST /app-link`、Lima/CHV relay 与 API Preview Gateway 已实现；`t3-base-005` 4G 已在 jfcs1024 真实 CHV 通过 probe-t3（含出网/DNS）、probe-node-t3（含网络/Stop-Start）和 probe-api-t3（多宿主 relay）；本地 Registry 发布/拉取通过；公开 start/stop、UI、Node relay、CHV 出网已落地；真实 Casdoor/浏览器 E2E、Connection Gateway 凭证注入、preview 通用化未完成 |
| [network-security.md](network-security.md) | API/mTLS 身份、IPv4/CIDR 默认/优先级、网络顺序、更新撤销、Secretless Gateway | 参数、API 行为、权限边界和安全负例验收 |
| [template-service.md](template-service.md) | 系统默认模板、个人 public/private 模板、Build/tag、Guest Agent 基础模板与 API | 个人 Registry、Build 队列/tag/fork 和权限已实现并通过 PostgreSQL 集成测试；Build Worker 与真实 base build 待完成 |
| [template-storage.md](template-storage.md) | manifest、Guest 基线、宿主布局、缓存、两盘、配额、Stop/GC、快照术语 | 模板字段、存储工作流与验收矩阵 |
| [warm-template-startup.md](warm-template-startup.md) | 对 Ready 模板捕获预热 Guest 内存快照、CHV 按需恢复、身份重绑定和启动性能指标 | 模板 build/Registry 后的启动优化；编排代码未接入 Node API，真实恢复未验收 |
| [isodo-sandbox-runtime-v0.1-spec.md](isodo-sandbox-runtime-v0.1-spec.md) | 总体架构、范围、参赛交付约束 | 架构入口，详细领域以以上规格为准 |
| [directory-responsibilities.md](directory-responsibilities.md) | 代码布局与依赖方向 | 目录职责，尚无完整 Runtime 代码 |

## 本地运行时可行性调查

| 文档 | 固定内容 | 本次完成范围 |
| --- | --- | --- |
| [t3-lima-local-runtime.md](t3-lima-local-runtime.md) | T3 Server bundle/HTTP/WebSocket auth 协议、Isodo Guest 接入方式、Lima 本地 Node 后端设计与验证边界 | 基于当前 T3 源码及本机 Lima/嵌套 VM 证据；T3 在 Lima 中尚未端到端运行 |

实施顺序与逐项验收见 [tasks/plan.md](../tasks/plan.md) 和 [tasks/todo.md](../tasks/todo.md)。部分规格和 R-01 元数据 API 已开始；已有 Guest 协议/CHV 探针仅按文档记录范围计入，不代表完整 Runtime 已完成。

所有工程默认值均为实现目标，不是商业套餐或已验证性能。当前 HTTP 契约覆盖网页登录、API Key 管理和 Sandbox 元数据 CRUD；完整 Runtime API 与 Node Orchestrator gRPC 机器定义仍需按这些规格展开，不能用总体文档的早期伪代码代替 schema。

机器可读的 HTTP 契约统一放在 `api/openapi/`：[控制面 API](../api/openapi/openapi.yaml) 和 [公开 Sandbox API](../api/openapi/openapi-public.yaml)。Node 内部契约放在 `api/proto/isodo/node/v1/`，并和 Guest proto 一起经根目录 `make generate` 生成。公开接口仍需认证，是否公开由契约边界决定，不代表匿名访问。Redis active registry/routing 和 Control→Node gRPC 的职责见[接入契约](control-node-contract.md)。

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
# Isodo 规格索引

## 产品与服务

- [产品架构](product-architecture.md)：用户场景、组件边界和信任关系。
- [Sandbox 生命周期](sandbox-lifecycle.md)：状态、异步操作、恢复与清理。
- [Control API 与 Node RPC 接入契约](control-node-contract.md)：对照 E2B API 与 Cocoon sandboxd，定义真实 VM 接入的 HTTP operation、mTLS Node RPC、幂等、fencing 和恢复边界。
- [R-01 Sandbox CRUD 与 Casdoor 鉴权](sandbox-crud-auth.md)：当前首个 Control API 实现边界。
- [网络、身份与凭证安全](network-security.md)：出口策略、租户身份与 Gateway 信任边界。
- [目录职责](directory-responsibilities.md)：代码模块和依赖方向。

## Runtime 契约与研究

- [Runtime v0.1 总体规格](isodo-sandbox-runtime-v0.1-spec.md)
- [Guest 协议](guest-protocol.md) · [Guest Agent Runtime](guest-agent-runtime.md)
- [模板与持久化](template-storage.md) · [Agent 集成](agent-integration.md)
- [E2B 行为研究](research-e2b-behavior.md) · [Cocoon 行为研究](research-cocoon-behavior.md) · [参考项目](references.md)
- [验证计划](validation.md)

- [网页登录与 API Key](web-auth-api-keys.md)：GitHub-only Casdoor 授权码登录、服务端会话、CSRF、密钥创建/调用/撤销及本地验证。

- [工程技术栈与 API 生成约定](engineering-stack.md)：keel 模板对照、OpenAPI 生成链路和后续接入顺序。

- [本地与远端 Sandbox API 统一（待审）](unified-sandbox-api.md)：E2B Control/envd 整体对照、管理与文件契约、配置文件通用写入、客户端边界和验收。
