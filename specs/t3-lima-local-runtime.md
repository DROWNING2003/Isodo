# T3 Server 与 Lima 本地沙箱接入规格（调查稿）

状态：2026-10-24，基于当前 T3 源码和 Lima 本机运行情况完成的可行性规格；不是已实现能力或产品 P0 范围变更。调查目标是复用 T3 现有服务/客户端协议，由 Isodo 在 macOS 上把 Lima VM 管理成逐沙箱的本地运行时，并在 Guest 中运行 T3 Server、Codex、Claude 等工作负载。

## 结论

**可行，建议把 Lima 做成 Isodo Node 的本地 VM 后端，每个 Sandbox 对应一个 Lima Linux 实例。** Isodo 继续负责身份、沙箱生命周期、Guest 进程/文件接口和本地 HTTP/WebSocket 代理；T3 Server 使用原有 server bundle 和 RPC contracts。不要另写一套 T3 RPC，也不需要在 Lima VM 里面再运行 Cloud Hypervisor。

需要先解决两个接入边界：

1. T3 当前沙箱启动器依赖 E2B `envd`，并使用 `https://<port>-<sandboxID>.<domain>` 解析 sandbox endpoint。Lima 没有这套 DNS/envd。Isodo 应将 bundle 下发和进程启动映射到自己的 Guest 文件/进程接口，并为 T3 提供 Loopback 代理 endpoint。
2. 当前 T3 Server bundle 明确携带 `fff-bin-linux-x64-gnu` 原生库；本轮验证用的 Lima Guest 是 ARM64。该 bundle 不能作为已兼容 ARM64 的产物使用。首选产出并验证 Linux ARM64 bundle/原生依赖；在此之前不宣称 T3 能在当前 ARM64 VM 运行。

## 1. 范围与协议分层

```text
macOS 客户端 / Isodo UI
   │ 本机 Isodo API；T3 HTTP + WebSocket 经 Loopback Proxy
   ├───────────────────────────────┐
   │                               │
Isodo Control / Local Node     Lima Host Agent / limactl
                                   │ SSH 管理、受控端口转发
                               Lima Linux Guest（一个 Sandbox）
                                   ├─ Isodo Guest Agent
                                   ├─ T3 Server :8080
                                   └─ Codex / Claude / 其他 CLI
```

有三条不同协议链路，不能混为一谈：

| 链路 | 当前/建议协议 | 责任边界 |
| --- | --- | --- |
| Isodo Control → Node | `NodeOrchestrator` 双向 TLS gRPC | 沙箱生命周期、Lima 实例所有权与本地资源。 |
| Node → Guest | 当前 Isodo 草案是 Cloud Hypervisor Vsock 握手后 HTTP/2 gRPC；Lima 适配器尚未定义 | Guest Ready、文件、进程与 PTY。CHV 的 Vsock 握手不可直接假定适用于 Lima。 |
| T3 客户端 → T3 Server | HTTP API + `/ws` WebSocket 上的 Effect RPC/JSON | T3 工作台与服务端；RPC 方法/Schema 由同版本 T3 contracts 提供。 |

T3 当前 `startT3Server` 控制链路还存在一个 `Node → envd` 跳：envd 的 HTTP/Connect 接口负责健康检查、文件上传和启动命令。对 Isodo 的建议是通过 Isodo Guest 接口实现等价动作，而不是为了兼容现有 helper 再把 envd 当成 Isodo Guest 协议。

## 2. T3 Server 的源码行为

本节依据当前工作区 `app/` 的源码；调查时 app 索引 HEAD 为 `05d0b8cf16acb8fdb358d36d15d4161ddd5fe9c8`。

### 2.1 部署与启动

| 步骤 | T3 当前实现 | Isodo + Lima 映射 |
| --- | --- | --- |
| 探测 Guest 管理面 | `GET https://49983-<sandboxID>.<domain>/health`，最多重试 30 次。 | Node 先等 Lima running，再等 Isodo Guest `Ready`。T3 HTTP 健康检查另行做，不用 envd `/health` 冒充。 |
| 上传 runtime | `POST https://49983-<sandboxID>.<domain>/files?path=/tmp/.t3-server-t3-server-dist.bundle&username=user`，`multipart/form-data` 字段名 `file`。 | Guest `WriteFile` /受控文件传输写入 staging path；校验 bundle digest 后原子改名。 |
| 准备目录和配置 | 解包到 `/tmp/.t3-server-runtime`；entry 是 `bin.mjs`；建立 `/home/user/.t3`、`/home/user/.codex`，写入 Codex config/auth 文件。 | 镜像预装匹配架构的 Node 和 Agent CLI。身份/凭证通过 Isodo 的授权入口注入，不把 host 环境复制给 Guest。 |
| 启动 T3 | envd `POST /process.Process/Start`，Connect JSON 5-byte frame，body 为 `{"process":{"cmd":"/bin/bash","args":["-lc",command]},"stdin":false}`；默认命令为 `node bin.mjs serve --port 8080 --host 0.0.0.0 --base-dir /home/user/.t3 --mode web`，外层使用 `nohup`。 | Guest `StartProcess` 运行等价 argv，或由 Guest 内 supervisor/systemd 托管。不要让 `limactl shell` 的交互 SSH 会话承担 Server 生命周期。 |
| 更新重连 | 启动脚本检测旧 `bin.mjs serve --port 8080` 进程并替换，避免重复启动。 | Isodo Node 用 sandbox ID + generation + Guest epoch reconcile；只管理本实例 T3 进程。 |

当前上传请求的 envd header 为：`Authorization: Basic dXNlcjo=`、`Connect-Protocol-Version: 1`、`Content-Type: application/connect+json`、`X-Sandbox-ID`，可附 `X-Access-Token` 和 `e2b-traffic-access-token`。这些是 T3 当前连 envd 的凭据，不是客户端连接 T3 Server 的协议，也不应复制成 Lima 的安全模型。

Server bundle 由 `app/apps/server` 的 `build:bundle` 生成 `app/artifacts/sandbox/t3-server-dist.bundle`。构建脚本复制 Linux x64 `fff` `.so`，启动器也在解包后解压该 `.so.gz`。Node/Server JS bundle 本身不是完整 Guest 镜像：Guest 还需要适配目标 CPU 的原生依赖、Node、Codex/Claude 所需 CLI/运行环境、CA/网络配置和工作目录。

### 2.2 客户端 HTTP、认证与 WebSocket RPC

1. 已认证客户端通过 `POST /api/auth/websocket-ticket` 调用 T3 auth API；Bearer 流程发送 `Authorization: Bearer <access-token>`。服务端依据现有 session 签发 `{ ticket, expiresAt }` 的短期 WebSocket ticket。
2. 客户端连接 `GET /ws?wsTicket=<ticket>`。`/ws` 校验 query 中的 `wsTicket`；没有 ticket 时回退到普通 HTTP session/cookie/credential 认证。
3. Upgrade 成功后，Server 以 `RpcServer.toHttpEffectWebsocket(WsRpcGroup)` 提供 WebSocket RPC，并使用 `RpcSerialization.layerJson`。客户端则由共享 client-runtime 与 `@t3tools/contracts` 中的 `WsRpcGroup` 生成/消费 RPC。
4. 因此复用重点是 T3 Server + 同版本 contracts/client-runtime。不要把它简化为自定义 JSON 消息协议，也不要在 Isodo 重实现 Effect RPC 的 framing、错误编码或事件 schema。

建议的本地路径为 `http://127.0.0.1:<随机端口>`（桌面/Web 开发），通过 Isodo Loopback Proxy 转发到 Guest 的 `8080`。代理只绑定 loopback，按已认证 sandbox ID 路由，限制 Host/Origin，并对 HTTP 与 WebSocket 使用同一映射。非本机 Web 页面不可直接访问 Guest 地址；生产/远程产品要另行提供可信 TLS endpoint。ticket 是短期 bearer secret，必须避免写入 access log、诊断日志、剪贴板或持久 URL。

T3 还具备 bootstrap/pairing credential 与 browser-session cookie 流程。具体由 Isodo 主体映射到哪个 T3 session，以及 Isodo UI 是否采用 pairing 页面，属于 Isodo 身份集成决策；当前调查未验证这些流程在 Lima Guest 中端到端可用。

### 2.3 Guest 内运行 Codex / Claude

T3 Server 是工作台服务和 Provider Runtime 的宿主，不是 Agent CLI 二进制本身。源码中 Codex runtime 使用 app-server 客户端/子进程适配器，Claude runtime 包装 `@anthropic-ai/claude-agent-sdk` 并解析 Claude executable。启动 `t3-server-dist.bundle` 不会自动准备一台带完整 CLI、工作区和登录状态的 Agent Guest。

Lima 镜像需按 Provider 独立安装/固定 Codex、Claude Code（及用户选择的其他 Agent）版本，并配置非 root 工作用户、HOME、工作区、Git/语言构建依赖、出站网络和会话持久化路径。Codex 的 `~/.codex`、Claude 的 `~/.claude` 和 T3 的 `/home/user/.t3` 必须在 Guest 私有盘内；不要挂载宿主 `$HOME` 让 Agent 访问。

T3 当前沙箱启动器会构造 Codex auth/config，并提供 API key 占位符环境值。产品实现必须替换为 Isodo 凭证路径：用户凭证仍由 Guest 外的 Isodo 凭证服务/授权组件持有，只按 Sandbox/Provider/期限签发最小权限、可撤销的临时授权。任何直接写入 CLI profile 的临时秘密都要有 TTL、权限 `0600`、退出/撤销清理和重启行为；不得把用户原始长期 API Key、桌面全量 env、Casdoor/Node 凭证放入镜像、T3 bundle 或 Snapshot。

## 3. Lima 本地 Runtime 设计

### 3.1 后端形态

- macOS 上增加 Local Node 模式；Control API/Node 契约保持不变，Node 增加 `LimaBackend`。它调用本机 `limactl`/host agent，不直接调用 CHV API，也不让 UI 调用 `limactl`。
- 每个 Isodo Sandbox 建立一个 Lima instance（名称由 Isodo sandbox ID 派生并校验/转义）；本机 VZ + 原生架构 Guest 优先。Lima VM 本身已是隔离边界，无需在其中再套 CHV。Linux/云端 CHV provider 保持独立。
- Local Node 的 journal 持久化 sandbox ID、tenant/owner generation、Lima instance 唯一名、镜像版本/digest、期望状态、最近 boot ID/Guest epoch、分配的 host forward port、operation ID 与错误。重启后 `limactl list`/inspect 与 journal reconcile，未知 instance 不接管、不删除。
- `ApplyOperation` 接收 idempotency key 和 generation；将 Create/Start/Stop/Delete 映射为 Lima 生命周期命令，结果经 `GetOperation`/`WatchOperation` 暴露。设置有限 timeout、取消、日志脱敏、并发锁和失败补偿；绝不通过字符串拼接调用 shell。
- Guest 内安装 Isodo Guest Agent 并用其 Ready/Exec/Files/PTY 作为 Isodo 语义入口。当前 proto 已定义这些能力，但现有实现只有部分 Ready 逻辑；Lima host↔guest 的具体安全传输桥仍需单独实现。Lima SSH 管理链路属于 Host Agent，不等于 GuestService 已经实现。
- T3 runtime bundle 通过 Guest 文件 API 注入；T3 Server 与 Agent CLI 进程由 Guest 管理。Node 只负责 Guest 生命周期和进程原语，不复制 T3 工作台/session RPC。

### 3.2 Sandbox 生命周期映射

| Isodo 状态/操作 | Lima 侧行为 | 关键约束 |
| --- | --- | --- |
| Create | `limactl create` 固定模板/image；建立独立 Guest 磁盘和 Node journal。 | instance name 必须由该 Node 登记为 Isodo 所有；创建失败删除部分产物但保留错误证据。 |
| Start | `limactl start`；等 instance ready、Guest Agent Ready、T3 HTTP ready 后报告 Running。 | Lima Running ≠ Isodo Guest Ready ≠ T3 Ready；每层分别观察。 |
| Stop | `limactl stop`；Guest 进程随 VM 终止，保留专属工作盘。 | 不误删磁盘；下一次冷启动生成新 boot ID/epoch。 |
| Delete | `limactl stop`（必要时）后删除且只删除该 Isodo instance/磁盘。 | 只有 journal 所有权和 generation 均匹配时可删；路径不接受用户任意值。 |
| Node restart | 从 journal 与 Lima instance 状态重建管理关系。 | 不以 API 超时或失联推定 VM 已停；不创建第二个 owner。 |

VM 启动和控制尽量使用 Lima 稳定的 `limactl create/start/stop/delete/list/shell` 命令接口。`limactl shell` 当前通过 SSH 进入 Guest 执行命令；它适合管理/引导，不作为 T3 客户端的长期 HTTP/WebSocket 数据通道。HTTP/WS 走显式 Guest→Host loopback 端口转发及 Isodo Proxy。

### 3.3 文件、网络与进程隔离

- Lima 默认模板会提供宿主目录挂载选项。Sandbox 模板必须关闭默认宿主 Home mount；只允许显式配置的单项目目录，且 mountPoint/权限明确、可审计。不能把宿主 `.ssh`、`.codex`、`.claude`、凭证目录或整个用户目录暴露给 Guest Agent。
- Guest T3 监听 8080；Host 端分配随机可用端口并只绑定 `127.0.0.1`，或者由 Local Node 持有 guest endpoint 并由自己的 HTTP/WS Proxy 转发。不能把 `0.0.0.0` host listener 暴露到局域网。
- Guest 进程由 `isodo` 非 root 用户运行；限制 CPU、内存、磁盘、进程/文件描述符和 stdout/stderr 回放。Lima `cpus`/`memory` 是 VM 资源上限；仍需 Guest 内对单一 Agent 子进程做 cgroup/ulimit/超时控制。
- 网络策略要验证 Agent 无法访问 host loopback/VZNAT host services、宿主 LAN/private/link-local/metadata 地址。Lima VZNAT 可从 Host 访问 Guest，但这不自动构成 Isodo egress isolation；防火墙和 Local Proxy 规则由 Isodo 负责。
- Lima plain mode 会关闭共享挂载和端口转发等功能；本设计需要 T3 入口与管理传输，因此应显式配置 no/default mounts 和必要转发规则，而不是盲目使用 plain mode。

## 4. 接入边界和待实现项

| 现有能力 | 当前调查判断 | 落地任务 |
| --- | --- | --- |
| T3 Server 构建/启动 | 源码已有 Linux Server bundle、envd 健康/上传/进程启动 helper。 | 将 bundle build target 改为可选 Linux x64/arm64，并锁定 T3 server 与客户端 contracts 版本。 |
| T3 客户端 RPC | `/ws` + `wsTicket` + Effect JSON RPC 已在 T3 server/client-runtime 内。 | 在 Isodo 客户端只接入 endpoint/ticket，不重写 WsRpcGroup。 |
| Isodo Control → Node | NodeOrchestrator proto 已声明异步 Apply/Get/Watch operation 与 Inspect/Status。 | macOS Local Node 实现这些 RPC 和 durable operation journal；当前 Go Node 代码仍是骨架/状态查询切片。 |
| Isodo Node → Guest | Lima Host 通过 loopback 端口转发访问 Guest TCP gRPC；Ready/BindRuntime、非交互 Exec、基础 Files RPC 已接通并有真实 VZ Guest 往返证据。PTY/stdin、严格工作区挂载隔离、文件去重和竞态边界仍未完成；Ready 仅报告 `ready.v1`。 | 补全 Guest 协议和隔离边界，再将能力纳入 Ready 并接入 Node/Build API。 |
| Codex/Claude | T3 provider runtime 集成已存在；当前 Lima Guest 没有预置并验证 CLI。 | 镜像构建/版本固定、授权注入、会话持久化、真实 provider smoke flow。 |
| Lima 宿主所有权/回收 | Lima 本身维护 instance，但 Isodo ownership/reconcile 尚不存在。 | Node journal、命名/标签、并发锁、崩溃恢复、孤儿清理与租户资源限制。 |

推荐的最小实现阶段：

1. **Lima backend proof**：Isodo Local Node 创建/启动/停止/删除单个 ARM64 Lima Guest；禁用宿主默认挂载；Isodo Guest Agent `Ready` 报告匹配的 sandbox ID/boot ID/epoch。
2. **Guest process proof**：使用现有 Guest 进程/文件接口上传匹配架构的 T3 bundle、启动 Server；由 host loopback HTTP/WS Proxy 执行 `/api/auth/session` 与 WebSocket ticket/RPC 连通检查。
3. **Codex/Claude proof**：Guest 镜像包含 CLI；在临时授权下各开一条真实会话，观察审批、输出、停止、重启恢复和撤销后的行为。
4. **安全与体验**：验证宿主挂载不可越权、LAN 不可访问本机 proxy、资源限制、journal crash recovery、磁盘回收和凭证不进入 Snapshot。

## 5. 本机验证与证据边界

### 已验证

- 本机 Lima CLI 报告 `2.2.1`；当前 `agent-poc-nested-kvm` 是 VZ/aarch64 外层 Ubuntu 26.10 Guest，内有 `/dev/kvm`、Cloud Hypervisor 53.0.0。
- 本轮启动的 Cloud Hypervisor API 报告 VM state `Running`、2 vCPU、1 GiB Guest、ARM64 EFI 和 CID 3 VSOCK socket；串口日志到达 Ubuntu 24.04.5 Guest 的 systemd 启动阶段。
- Lima 外层 Guest 的 `isodo-sandbox-poc.service` 此刻为 active。该链路证明当前机器具备“Lima 内再启动 ARM64 VM”的能力，但这个额外嵌套层不是推荐的 Lima sandbox 产品架构。
- 本机 VZ Lima Guest 从含 Isodo Guest Agent 的版本化 Base 克隆；Guest Ready/BindRuntime、二进制 Files 上传/下载及非交互命令 stdout/退出事件通过端口转发 RPC 实测通过。

### 未验证

- 该 CHV Guest 仍是通用 Ubuntu 镜像；其中没有 Isodo Guest Agent、T3 Server、Codex/Claude CLI，未经过 Isodo Control/Node 生命周期调用。
- 没有在 Lima ARM64 Guest 启动 `t3-server-dist.bundle`；当前 Server bundle 的 FFF 原生库按构建脚本为 Linux x64。T3 HTTP、`wsTicket` 和 effect RPC 尚未从这个 Guest 做端到端握手。
- Node 的 Sandbox 创建仍从 Base 克隆；模板 Build 的 recipe 尚未产出、校验并登记 Lima 派生模板，也没有按固定 `template_build_id` 从对应派生模板创建 Sandbox。Codex/Claude CLI 及真实授权会话尚未安装或验证。
- 未验证 Lima 原生实例被 Isodo Node 管理、生命周期重启恢复、宿主网络访问负例、文件挂载权限和凭证隔离。
- Lima host agent/SSH/vsock tunnel ≠ Isodo `guest-protocol.md` 中 CHV `CONNECT 1024 + OK` 握手。必须通过独立 transport adapter 或显式端口转发验证，不能声称协议已复用。

### 风险与退出条件

- 若无法产出 Linux ARM64 T3 bundle/原生依赖，暂停 ARM64 Lima 集成；评估正式的 multi-arch bundle 或 x86_64 QEMU Lima 作为开发 fallback，不以未验证的 Rosetta/架构翻译作生产路径。
- 若 VZ port forwarding/Local Proxy 不能隔离 Host 和 LAN 访问，先补 Host firewall 和认证路由，不公开 guest 端口。
- 若 Isodo Guest gRPC over Lima 的 transport bridge 不稳定，明确作为一项新的 Node↔Guest transport，而不是在文档中把 Lima SSH 或 Lima 内部 gRPC 写成现成 Isodo 协议。

## 6. 源码与资料

### 当前 T3 工作区

- [t3-orchestrator.ts](../app/packages/sandbox-client/src/t3-orchestrator.ts)：bundle 解包、Provider 配置、默认启动命令、envd health/upload/process start。
- [envd.ts](../app/packages/sandbox-client/src/envd.ts) 与 [files.ts](../app/packages/sandbox-client/src/files.ts)：envd HTTP/Connect headers、5-byte framing、`/files` multipart 上传。
- [sandbox.ts](../app/packages/shared/src/sandbox.ts)：`sandboxUrl` 生成 `https://<port>-<sandboxID>.<domain>`。
- [ws.ts](../app/apps/server/src/ws.ts)：`GET /ws`、session 校验、`WsRpcGroup` 与 JSON Serialization。
- [EnvironmentAuth.ts](../app/apps/server/src/auth/EnvironmentAuth.ts)、[auth/http.ts](../app/apps/server/src/auth/http.ts)：`wsTicket` 查询字段、ticket 签发/验证与 auth HTTP handler。
- [remote.ts](../app/packages/client-runtime/src/authorization/remote.ts)：Bearer session 请求 `/api/auth/websocket-ticket`。
- [contracts](../app/packages/contracts) 与 [client-runtime RPC](../app/packages/client-runtime/src/rpc)：共享 RPC contracts 与客户端连接实现。
- [apps/server/package.json](../app/apps/server/package.json)：`build:bundle` 复制 `fff-bin-linux-x64-gnu`；T3 启动器解包后恢复库文件。

### 当前 Isodo 工作区

- `api/proto/isodo/node/v1/node.proto`：NodeOrchestrator 操作/状态 RPC 声明。
- `api/proto/isodo/guest/v1/guest.proto`、[Guest 协议规格](guest-protocol.md)：当前 Guest 能力草案；CHV Vsock 握手和 Lima transport 不兼容性需明确。
- `internal/node/orchestrator/status.go` 与 `cmd/isodo-node/main.go`：当前 Node 可验证范围；尚不能视作完整生命周期后端。
- [产品架构](product-architecture.md)、[Node 接入契约](control-node-contract.md)、[沙箱生命周期](sandbox-lifecycle.md)：新 Local Node 后端必须复用的 owner/generation/operation 语义。
- [Lima Port Forwarding](https://lima-vm.io/docs/config/port/)、[Lima VM Types](https://lima-vm.io/docs/config/vmtype/)、[Lima Filesystem Mounts](https://lima-vm.io/docs/config/mount/)、[limactl shell](https://lima-vm.io/docs/reference/limactl_shell/)、[limactl create](https://lima-vm.io/docs/reference/limactl_create/)：端口、VZ、宿主挂载、SSH 管理与实例创建说明。
