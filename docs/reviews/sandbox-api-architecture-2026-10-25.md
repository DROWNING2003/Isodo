# Isodo Sandbox API 架构 Review 与规格修订建议

> 修订说明：本报告保留初次Review证据。用户反馈后的收敛设计、UID/GID启动链校正、目录依赖与最小变更清单见[修订报告](sandbox-api-design-revision-2026-10-25.md)。早期Environment API、desired=deleted、默认不覆盖建议已撤回，以[当前设计](../../specs/unified-sandbox-api.md)为准。

日期：2026-10-25。结论：保留现有 Runtime/Guest；先补身份、失败和删除收敛语义，再统一管理与操作面。

本轮仅文档 Review，不修改实现代码、生成接口、提交或部署。以下“推荐”均待规格确认。

## 1. 证据范围

- 已合并基线：`e368919352cab29d40b5347466f9a8ce6a5fe273`。
- 当前 worktree：`drowning/unify-sandbox-api`。OpenAPI、生成类型、本地 server/index、Control DTO cast 有此前未完成改动；不能计入已合并能力。
- 审阅源码：Control handlers/store/worker、Node Lima/CHV/journal/relay、Guest files/process/identity、OpenAPI/proto、App IPC/配对调用与现有规格。
- E2B 本地参考：`fork.sandbox@7c7ae6316da7c2c021d0e9dfec7f75f63bd57ca8`，`fork.envd@5342fec5cdd0c23eddd84cc9e09a5433b414ef3a`；这是 fork 实现，不把其扩展视作上游通用事实。
- Cocoon/CubeSandbox：本轮官方在线文档核对；未运行项目，也未声称性能、可靠性或安全保证已经在 Isodo 验证。
- 本轮没有真实 VM/远端 E2E，不操作已有沙箱。

## 2. 当前真实实现状态

| 能力 | 状态 | 依据与边界 |
| --- | --- | --- |
| Control 创建、列表、详情、更新 | 已有 | `internal/control/sandbox/http.go:105,152,185,349`；创建实际 201，不是新规格的 202 |
| Control start/stop | 部分 | handler 202；DB 事务/operation outbox/worker 轮询已实现。失败仅 operation.error_code，公开 get 没有 last_error |
| Control delete | 部分且有 P0 缺口 | `postgres/store.go:555` 只更新 deleted_at，没有删除 job；不等于 VM 已回收 |
| 本地管理 API | 已有旧契约；统一草稿未验收 | 基线 localapi/server.go 返回 sandbox_id/operation、列表 state；当前 worktree 已开始改 DTO，不能称统一完成 |
| Node 生命周期/fencing | 部分 | Lima/CHV 有 operation ID/hash、generation、revision、journal；CHV 对未解决记录拒绝启动，未实现自动接管恢复 |
| Guest 身份 | 已有 | `agent/server.go:148` 校验 sandbox/generation/boot/epoch；不等于所有公开代理已绑定 epoch |
| Guest Files | 已有实现和测试 | `agent/files.go`：Stat/List/Read/Write/Mkdir/Remove/Move，workspace root、大小/摘要校验、原子提交；不是公开远端 Files API |
| 本地配置写入 | 已有专用链路 | `/agent-config` → LimaServer.WriteAgentConfig → Guest MakeDirectory/WriteFile；256 KiB JSON 内容，并非通用大文件传输 |
| Control→Node→Guest Files | 未接入 | NodeOrchestrator proto 无 Files RPC，public OpenAPI 无 Files 路由 |
| Guest 非交互 Process | 已有 | Start/Get/List/Subscribe/Signal，输出 seq/gap、去重、超时、进程组信号 |
| Guest PTY/stdin/Sessions | 未实现 | process.go:89/92 显式拒绝 PTY/stdin；WriteInput/ResizeTerminal 没有 override；持久 shell 无实现。proto 声明不算实现 |
| T3 终端/附件/文件 | 独立已有链路 | T3 服务自身能力，不可用于证明通用 Guest Terminal/Files 公共契约已实现 |
| Vsock 服务访问 | 部分已有 | Guest PortForward + Node RelayPort + Control preview；已能复用，公共 Ports 授权模型及实例绑定未完备 |
| 模板 available | 部分 | SQL 只按可见性过滤；Ready default 由 App 再查询 tag/build，不满足服务端可创建目录的目标 |
| 统一 SDK | 未实现 | App 管理仍分本地 IPC/云端 generated client；旧 sandbox-client 同时存在 E2B 形状接口 |
| MCP/Skill | 不在本轮实现范围 | 沿用独立功能进度，不把入口/设计当作完整同步能力 |

### 本轮测试证据

从 HEAD 导出独立 `/tmp` 源码副本后运行：

```sh
go test ./internal/node/localapi ./internal/guest/agent ./internal/node/orchestrator ./internal/control/sandbox
```

四包通过。该结果是单元/handler 测试，不是本地/远端 VM 验收。

当前 worktree 同命令：Guest、orchestrator、Control sandbox 通过；localapi 的 `TestCreateSandboxRejectsUnbuiltTemplate` 失败（400，期望 409）。原因是草稿先增加 name 校验，原测试请求无 name，尚未完成契约/调用方迁移；本轮记录，不修实现或删测试。

## 3. 规格与实现问题（按优先级）

### P0-1：通用 Files 迁移必须冻结实际 HOME、配置根和保留目录

依据：`orchestrator/t3.go:282`、`images/base/isodo-t3:8` 设置 HOME=/workspace/.home、CODEX_HOME=/workspace/.codex。`orchestrator/agent_config.go:43` 将 `.codex/...` 映射到前者之外的专用 Codex 根，其余配置到 HOME。旧统一规格 §7 只说“上层映射”，没有可复用的环境/路径契约。

影响：把 `~/.codex/config.toml` 机械展开为 HOME/.codex 会写到错误位置。放开任意 Guest 路径则扩大 API 权限；允许写 `.isodo` 或 `.t3` 内部状态也可能破坏代理 CA、配对与工作台。

建议：保留 `/workspace` 唯一文件根；环境信息提供 home、workspace、Agent 配置根。Files 只收规范绝对路径；Agent helper 按 config root 生成路径，不在通用服务里识别 Agent 名称。保留内部目录写入策略，不开放宿主路径、任意 username/root。系统目录保护不应依赖文件名字符串拼接。

Guest 已使用 `os.OpenRoot`/`OpenRoot(parent)` 处理根约束，拒绝 `..`、特殊文件和终端 symlink；保留这些机制及 TOCTOU/并发 symlink 测试。内部不逃逸的 symlink 是否允许，须在契约说明，不能把“禁止逃逸”写成“禁止所有 symlink”。

这是 Files API 权限边界；已有 Process 可执行 Guest 命令，它不是对整个 Guest 文件系统的 MAC 隔离承诺。真实 secret 由宿主 injection 保管，文件里是占位符；通用文件服务既不取材料也不做凭证替换，不能声称能识别并阻止用户任意上传的所有秘密。

### P0-2：不公开 Operation 时仍需暴露动作结果，且 desired 是意图

依据：新规格 §6 只有 desired/observed；`postgres/store.go:386` 失败时会回滚 desired；error_code 在 operation 表，`sandboxResponse` 不返回。create handler 实际 201，与草稿 202 不同。

影响：等待 running 时只能得到 error，无法说明何事、为何失败；desired 回滚还混淆用户目标与观测。worker 完成写 Sandbox 只按 ID，不按 revision/generation/deleted_at 条件更新，迟到完成可能改写后续意图的状态。

建议：增加轻量 last_action（revision、type、status、stage、error、时间），不新增公开 Operation API；desired 不因执行失败自动改变。操作完成写入必须同时匹配预期 revision/generation。故障状态保留最近真实观测时间，unknown 不等于 stopped。

### P0-3：Delete 不能只是软删除，也不能把隐藏当作清理成功

依据：`postgres/store.go:555` 无异步 Node delete；`0008_sandbox_lifecycle_operations.sql` action 仅 create/start/stop。Node proto/runtime 虽有 delete，Control 不发它。新规格 §6 的“204 + 列表隐藏”没有清理结果契约。

建议：事务记录 deleting 意图、递增 revision、创建内部 delete job，并停止新操作准入；Node 按身份停止 VM/清盘/撤销 relay 和注入，再完成 deleted。以 tombstone 跟踪 cleanup_pending/error。清理未确认时不能释放容量并宣称 deleted。

建议 DELETE 返回 202 Sandbox；同账号可 GET deleting/deleted tombstone，默认列表隐藏它们。已完成删除重复请求 204；不同账号仍 404。tombstone/幂等记录保留至少 24 小时，未完成清理不淘汰。UUID 不重用。

### P1-1：不能把 journal 和 fail-closed 当 reconciliation

依据：CHV `chv.go:104` 检出 pending/running 直接拒绝服务启动；Lima `lima.go:718` 重发 pending operation；Lima Inspect 只读 VM 状态，不证明 Guest Ready。Control 没有完整周期状态修复。

建议：原归属优先；Node 重启先核实原实例、进程/锁和磁盘，不能另起 VM。支持的接管才恢复准入；无法核实则 unknown、可重试，保留磁盘和容量。Guest epoch 变化使旧执行/会话句柄失效。测试覆盖半提交/丢回复，不要求两种后端同一内部恢复实现。

### P1-2：文件流适配还缺长度、摘要、取消和提交不确定语义

依据：`files.go:230` 限 256 MiB，233 要求首帧 SHA256，292 校验 chunk offset/上限，304 校验总长，320 原子 rename，340 有 COMMIT_UNKNOWN。`t3.go:415` 的配置 helper 已先把内容放在内存；不能原样拿来处理大文件。

建议：先选一种明确方案：可重读 Blob/本地文件预计算摘要后流式发送；不可预计算流使用 Node 有界磁盘暂存并计算，不修改现有 Guest wire。HTTP multipart 总长度不是文件长度。限并发/暂存配额/空闲超时，取消清理 staging。COMMIT_UNKNOWN 或丢成功回复时允许查询摘要确认，不自动覆盖重试。

IPC 用有界 chunk + ACK/credit 或 main 侧打开的文件句柄，禁止整文件 JSON/base64/单条 IPC。两端相同 FilesClient 业务契约，但无需强制相同 transport。

### P1-3：身份约束必须作用于准入、流和端口凭证

依据：Guest `checkIdentity` 完整；Node `RelayPortConn` 的 generation=0 跳过 generation 检查；preview claims（preview.go:62）只有 sandbox_id/port/exp，proxyRelay 使用 generation=0（164）。

建议：运行句柄包含不透明 runtime_id，服务端映射当前 generation/boot/epoch；公开调用者不得提交内部 identity。Node admission 检查最新目标和运行实例；Stop/Delete 撤销在途数据流与路由。新的端口令牌绑定 runtime_id/port/expiry，旧实例 token 不能因相同 sandbox ID 继续访问新启动。T3 现有配对不改协议，Gateway 逐步补实例校验及回归。

### P1-4：Commands 可复用，交互 Sessions 不能只做 adapter

依据：`agent/process.go:89`、92；非交互实现有 Start/Get/Subscribe/Signal；终端方法未实现。`process.go:619` 环境 USER=nobody 不等于操作系统降权（启动处只有 Setpgid）。

建议：先公开非交互 commands 并复用 execution ID、seq/gap、超时、信号；再补 Guest PTY/stdin/resize 和持久 shell session。明确 exec 的实际 UID/GID、cwd/env，而非依赖 USER 字符串。commands.run 的取消需显式 signal，订阅断开默认仅 detach；进程跨 Stop/Guest 重启不恢复。

### P1-5：规格之间存在多套相互冲突的契约

依据：`specs/sandbox-lifecycle.md` 仍写公开 Operation、colon action、page token、Redis 必需、desired deleted；新统一规格使用快照、slash action、offset、只两种 desired。Guest 规格的 PTY/输入是目标而非实现；Node proto:10 的“从不携带 I/O”注释与 RelayPort 已不符。

建议：确认新统一规格为外部契约来源，逐项标明旧章节被替代；内部 operation/fencing 继续保留。Node 操作面单独 service 或明确允许受控 I/O，不把所有事都塞进 lifecycle RPC。此轮只提出修订，不修改 proto。

### P2-1：模板 available 应由服务端保证 Ready default

依据：`postgres/queries/templates.sql:13` 只过滤可见性，App cloudAppTemplates 再取 tag/build。新规格却已将 available 定义为可创建。

建议：列表/count 使用相同 Ready default 条件和确定排序；返回解析后的 default_build_id/digest。权限仍在 create 再检查，列表结果不构成永久创建保证。base 先具备构建/发布事实，不伪造 Ready。

### P2-2：客户端接口与输入校验仍有产品层不一致

依据：示例 `create({template:"base"})`，新规格 name required；可接受服务端生成名称以支持目标 SDK。新规格 `listTemplates()` 未带分页参数，可能漏模板。错误码 schema 和实际 retryable/request ID 不一致；旧面板/生成 client/local IPC 并存。

建议：name 可选、缺省生成服务端展示名；ID 稳定独立。list/listTemplates 同分页接口；SDK 分页 helper 处理后续页，不悄悄截断。统一 SDK typed error，Electron 显式传错误 DTO；request_id 与幂等键不同。移除旧路由和调用方，不留双格式 parser。

## 4. 推荐修订后的契约（待确认）

### 4.1 管理资源

两端 HTTP 语义统一 `/v1`，Electron IPC 仍为本地 transport，不暴露本地 token。建议方法如下：

| 方法 | 路由 | 返回 |
| --- | --- | --- |
| GET | /v1/sandboxes?limit&offset | 200 SandboxList |
| POST | /v1/sandboxes | 202 Sandbox（name 可选、template 默认 base） |
| GET/PATCH | /v1/sandboxes/{id} | 200 Sandbox |
| POST | /v1/sandboxes/{id}/start 或 /stop | 202 Sandbox；已收敛同目标 200 |
| DELETE | /v1/sandboxes/{id} | 202 deleting Sandbox；已清理 204 |
| GET | /v1/templates?scope=available&limit&offset | 200 TemplateList（只可创建 default） |
| POST | /v1/sandboxes/{id}/app-link | 200 SandboxAppLink，保留 T3 专用流程 |

资源分页保持现有 `{items,total,limit,offset}`，默认 100、范围 1..100；offset>=0，数据库时间+ID 排序确定。目录继续 Guest page_token，不为命名一致重写目录 RPC。

Sandbox 增补：

```json
{
  "id": "uuid",
  "name": "my-sandbox",
  "metadata": {},
  "desired_state": "running",
  "observed_state": "starting",
  "revision": "7",
  "runtime_id": "opaque-instance-handle",
  "last_action": {
    "type": "start",
    "revision": "7",
    "status": "running",
    "stage": "guest_ready",
    "error": null,
    "updated_at": "2026-10-25T08:00:00Z"
  },
  "observed_at": "2026-10-25T08:00:00Z",
  "cleanup_pending": false
}
```

示例仅展示新增字段；保留模板、created_at/updated_at 和实际 resources。runtime_id 仅运行实例存在时提供，Node 内部持有真实 identity；revision 序列化为不透明字符串，避免 JS 大整数精度问题。last_action 不是必需查询的公开 Operation 资源。error 非空时为统一结构，包含可安全公开的原因与失败时间。

建议 desired 为 running/stopped/deleted；observed 为 requested/preparing/starting/running/stopping/stopped/deleting/deleted/error/unknown。内部 failed 映射 error，不维持公开 error/failed 两个同义终态。actual observed_state 与 last_action.status 分离：某次动作失败，不代表 VM 一定已经停止。

### 4.2 状态、并发、等待与恢复规则

| 当前事实 | start | stop | delete |
| --- | --- | --- | --- |
| stopped | 接受新 revision | 同目标 no-op | 接受删除 |
| running | 同目标 no-op | 接受新 revision | 接受删除 |
| 正在执行同目标 | 返回当前快照，不重复 job | 返回当前快照，不重复 job | 返回 deleting 快照 |
| create/start 正在执行，收到 stop | 409 ACTION_IN_PROGRESS | 409，用户明确重试 | 可升级为删除目标，但必须 fencing 后安全接管 |
| stopping 收到 start | 409 ACTION_IN_PROGRESS | 返回现有快照 | 升级删除目标，等待清理 |
| unknown | 503，不能另起实例 | 首版 503；保留状态供运维核实 | 记录删除意图但 cleanup_pending，原节点未核实前不报告清理成功 |
| deleting/deleted | 409/410 | 409/410 | 返回已有结果，保留 tombstone |

- HTTP 202 仅表示意图持久化，不保证就绪；响应 Location 指向 Sandbox GET。
- lifecycle Idempotency-Key 作用域为账号+方法+规范路径+键，绑定规范请求 hash；相同键返回当前同一资源/动作，不再执行，不同输入 409。SDK/App 为每个逻辑动作生成一次，网络重试复用。
- 保留现有 PostgreSQL row lock、outbox、worker 租约和 Node journal；本地用 journal/持久 index 实现相同语义，不引入 Redis 集群。
- 同沙箱操作串行化；本地 index.Next/Apply/Commit 必须作为逻辑原子操作，分别加锁不足以保证 HTTP 并发。
- waitUntilRunning 默认客户端预算 5 分钟（可配置、取消）；超时只结束等待，不推断后台失败、不自动 stop/delete。等待错误携带最近快照/last_action，用户可再次等待。
- 服务端操作预算与客户端等待分开；Node 丢回复先查同 operation，再决定重试，不能新建实例。真实故障则更新 last_action；控制失联置 unknown。
- Stop 的成功是 VM 已停止、盘仍在；Start 冷启动新 boot；pause/resume 内存语义不进入当前 API。
- Delete 的逻辑 tombstone 与清理事实分别持久化；完成更新 CAS revision/generation，迟到 start/create 不能覆盖 tombstone。删除不因已有动作“已取消”而跳过残留清理。

### 4.3 通用 Files

维持 `POST/GET /v1/sandboxes/{id}/files?path=` 的上传/下载设计，目录操作复用旧规格 §7；移除 agent-config HTTP/IPC。multipart 单文件是外部易用性选择，不意味着内部重写 Guest RPC。

增加环境描述（GET `/v1/sandboxes/{id}/environment`，运行态）：

```json
{
  "runtime_id": "opaque",
  "workspace": "/workspace/project",
  "filesystem_root": "/workspace",
  "home": "/workspace/.home",
  "agent_config_roots": {"codex": "/workspace/.codex"},
  "capabilities": ["files.read", "files.write", "commands.run"],
  "limits": {"max_file_bytes": 268435456}
}
```

这是 Node/模板验证的布局信息，不由客户端任意指定；Lima/CHV 返回实际值。capabilities 来源于实际实现与健康检查，目前 ready.v1 宣告需补齐，不能根据 proto 自动声称支持 PTY。

FilesClient 在配置前获取布局并缓存到 runtime_id；agent helper 使用 codex config root，Claude/OpenCode 使用 HOME 下相应配置。通用 Files 写绝对路径，不接收 `~`、username、宿主路径。

建议明确 header：单文件传输 `X-Isodo-File-Size` 与 `X-Isodo-File-SHA256`；下载先返回服务端可确认的大小，校验结果在完整接收后比对。Guest 读流摘要在结束帧，不为抢先填 HTTP header 而隐式全量缓存；SDK 可消费传输完成元数据/预查询摘要或采用有界暂存，最终传输响应格式需在 Files 阶段一起冻结。

待审选择：上传默认不覆盖（overwrite=false）以防 SDK 误写，配置 helper 明确传 overwrite=true；这与 E2B 默认覆盖不同，不能由 adapter 自行决定。覆盖前置摘要/版本作为后续增量，首版并发覆盖按明确策略返回冲突或声明最后提交者获胜，不伪装乐观锁。

完成表示 Guest 验证 hash/length、原子提交并确认；确认丢失返回 COMMIT_UNKNOWN，SDK 可 stat/摘要核对，不承诺 exactly-once。取消在提交前保留旧文件，提交后取消不能声称撤销。

### 4.4 Commands / Sessions

建议业务接口：

```ts
sandbox.commands.run({ argv: ["pwd"], cwd, timeoutMs, signal });
sandbox.commands.start({ argv, cwd, env, timeoutMs }); // CommandHandle
handle.output({ afterSeq, signal }); // stdout/stderr/exit/gap/heartbeat
handle.signal("SIGTERM");
const session = await sandbox.sessions.create({ cwd, env, pty: { cols: 80, rows: 24 } });
session.write(bytes);
session.closeStdin();
session.resize({ cols: 100, rows: 30 });
session.close();
```

`commands.run("pwd")` 作为 SDK 便捷重载时明确执行 `/bin/sh -lc`；argv 形式不经过 shell。先实现 argv，不强迫后端猜字符串语义。

建议资源路由：POST/GET `/commands`、GET `/commands/{execution_id}`、GET `/commands/{execution_id}/events?after_seq=`、POST `/commands/{execution_id}/signals`；均位于 `/v1/sandboxes/{id}` 下。执行句柄绑定 runtime_id，跨 Guest 重启返回 STALE_RUNTIME，不把宿主 PID 当公共 ID。

Commands start 返回 202 Command；run 是 SDK start+订阅+等待的封装。订阅取消默认 detach，命令取消显式 SIGTERM、宽限后 SIGKILL 并等待；timeoutMs 省略=60秒，0=显式无命令超时（仍受实例寿命/配额约束），最大非零值 24h，与已有 Guest 对齐。输出有界，必须暴露 gap/truncated；非零 exit code 是执行结果，不是网络错误。

Sessions 是 Guest 内持续 shell/PTY 进程，不是 T3 Thread，不是运行一个新 shell 就伪装恢复 cwd/env。连接断开默认保留会话到超时，可重新 attach；Stop/Guest 重启全部失效，不承诺内存持久化。先补 Guest PTY/stdin/ACK/resize，再公开 Sessions capability。PTY 通常合并 stdout/stderr，不能承诺仍可分离。

双向交互采用二进制 WebSocket/等价有序流，背压/ACK、帧大小、seq/gap、EOF、exit 终帧和 ticket 鉴权在 phase 3 定义公共 wire；不导出 T3 RPC，不为这轮文档直接生成一套未验证的新协议。

### 4.5 Ports 与 T3 分离

新增通用端口入口建议 POST `/v1/sandboxes/{id}/ports/{port}/links`，返回 `{url,expires_at,runtime_id}`，SDK `ports.getUrl()` 包装此调用；首版 HTTP/WebSocket 预览，任意 TCP dial 为后续能力。

复用 Control RelayPort → Node → Guest PortForward → Guest loopback，不装网卡。按账号、实例、端口授权，拒绝内部管理端口/未授权服务；不要将现有 Guest Agent/T3 管理服务经任意端口参数暴露。

T3 app-link 保留其配对 token/cookie/bootstrap 流程；通用端口链接不需要 T3 pairing token。新链接的 auth 可复用 Gateway 机制，但必须绑定 runtime_id；start/delete 撤销旧路由，过期/跨账号拒绝。

## 5. 开源参考的采用边界

| 项目 | 本轮核对来源 | 借鉴 | 不引入 |
| --- | --- | --- | --- |
| E2B | 本地 fork 管理 OpenAPI、envd YAML/Filesystem/Process proto、SDK 调用测试 | 资源式管理、files/commands 对象分组、内容流与管理分离 | envd、旧版兼容 API、同步创建假设、隐式 connect 恢复、E2B 凭证 |
| Cocoon | [silkd](https://cocoonstack.github.io/sandbox/silkd.html)、[sandboxd API](https://cocoonstack.github.io/sandbox/sandboxd-api.html) | vsock relay、原子写、半关闭、detach/attach、协议 fixtures、取消必须管理实际进程 | 自定义 NDJSON/base64 wire、替换 gRPC、warm-pool/mesh、未测性能承诺 |
| CubeSandbox | [官方架构](https://docs.cubesandbox.com/architecture/overview.html)、[域名/代理说明](https://github.com/TencentCloud/CubeSandbox/blob/master/docs/guide/https-and-domain.md) | 外部 API 与节点编排分离、资源归属、独立 proxy 路由 | Redis 中央协调、CubeVS/eBPF、containerd/CubeShim、新 VMM、透明自动恢复 |

参考事实的限制：Cocoon 的会话/独立进程可重 attach，但 foreground exec 断线检测有局限，其 SDK 会显式 kill；不是拿 HTTP cancel 当“进程保证终止”。其传输含 base64，Isodo 文件不照搬。CubeSandbox 明确使用 Redis 和自有 VMM，架构图相似不表示可直接采用实现。上述是阅读设计，不是上游代码复制。

## 6. 最小改造方案与阶段验收

| 阶段 | 改动边界 | 两端共同验收 |
| --- | --- | --- |
| 0 契约确认 | 修订统一/spec lifecycle/guest 边界，冻结 DTO/错误/身份/删除/幂等；不动 Runtime | 同一 fixtures 表达状态与异常，无两套 parser |
| 1 管理统一 | Control/local handler、index/journal、DB job、worker CAS；删除旧 IPC/路由；连接不重写 | create/get/list/update/start/stop/delete、重复/冲突、超时/丢回复、tombstone、Node 重启；原 VM 归属不漂移 |
| 2 Files | 新 Node 操作面 service 转发现有 Guest；HTTP/IPC 流；上层配置 helper | 文本/二进制/零字节/256MiB 上限/摘要/取消/磁盘满/symlink/旧 epoch/原子覆盖；配置写入真实路径且无 secret |
| 3 Commands/Sessions | 先复用非交互 Process，再补确实缺失的 PTY/stdin；不碰 T3 wire | 退出码、双输出、stdin EOF、timeout、signal 子进程组、seq/gap、断线重 attach、resize、重启失效 |
| 4 Port Proxy | 复用 relay，加通用授权与实例路由 | 无 NIC 的 CHV 上 HTTP/WS、半关闭、取消、拒绝内部端口；stop/start 后旧链接不可用 |
| 5 SDK/回归 | 统一对象式客户端；transport 留在 adapter，生成类型/契约 fixtures | 同一 SDK 用例分别运行 Lima/CHV；创建→配置→命令→上传下载 hash→端口→stop/start→删盘；全新会话 T3 仍配对成功 |

各阶段先 handler/contract 测试，再真实运行；单元 fake 通过不算 Remote CHV 成功。当前 `.env.example`/create worker 支持可选 bridge/egress，因此验收无 NIC 用例必须明确关闭 NetworkBridge，并检查 CHV 配置没有 net/TAP，不得按“用了 vsock”推断没有网卡。此轮不改动已存在的网络代码。

推荐测试命令（实施时）：

```sh
go test ./internal/control/sandbox ./internal/control/sandboxworker ./internal/node/localapi
go test ./internal/guest/agent ./internal/node/orchestrator ./internal/control/preview
go test ./internal/control/postgres  # DB integration 需单独配置测试数据库，skip 不算通过
cd app
pnpm --filter @t3tools/web test src/features/isodo
pnpm --filter @t3tools/web typecheck
pnpm --filter @t3tools/desktop typecheck
```

以真实证据记录：源码存在、contract 测试、Lima VM、CHV 无 NIC VM、全新客户端 Gateway/T3 会话五个层级分别报告。未通过重启接管的 CHV 不能称 crash recovery 完成。

## 7. 需要冻结的决定

本报告建议统一采用：workspace 内的实际 Agent roots；公开 last_action 而非 Operation 资源；desired 保留意图；202 删除及可查询清理；生命周期幂等/串行化；Files 首帧摘要与有界流；非交互 Commands 先于 PTY Sessions；runtime_id 绑定的 Ports；本地旧接口直接替换。

待用户最终规格确认后才修改实现。当前 worktree 草稿保留原样，不把本报告变成部署或提交授权。
