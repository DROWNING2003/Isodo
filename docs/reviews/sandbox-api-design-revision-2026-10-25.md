# Sandbox API 设计修订、目录依赖与最小变更清单

2026-10-25；设计总体认可，后续优先级和执行顺序以[黑客松计划](../plans/hackathon-unified-api.md)为准（取代本文旧串行顺序）。只做Review与文档修订，不修改实现、提交或部署。已合并基线为e368919；worktree先前已有实现草稿，本轮原样保留。

## 1. 本次收敛决定

| 议题 | 本版建议 | 相对前份Review的变化 |
| --- | --- | --- |
| 第一优先级 | Delete清理闭环、revision/generation fencing、实际UID/GID验证、现有Preview实例绑定 | 先安全收敛，不先扩展SDK/操作面 |
| desired/observed | VM意图只有running/stopped，观测只表达VM事实 | 不把deleted/deleting混进VM状态 |
| 删除事实 | `deletion:{requested_at,completed_at?}`，错误在last_action | 区分意图持久化与清理完成，不再添加重复cleanup_pending/status组合 |
| 动作状态 | accepted/running/succeeded/failed/superseded | last_action.status独立于VM observed_state；running在此只指动作执行中 |
| 等待 | 写响应保存target_revision；相同revision核对动作结果，更高revision明确superseded | 不再仅比较当前desired==observed |
| Environment API | 本轮不新增 | Agent helper使用已验证base布局，未来可变布局有实际需求再评审 |
| Files默认覆盖 | 用户已确认overwrite=true，可显式false | 撤回前份Review的默认false建议，不能未确认就改变行为 |
| 扩展范围 | 本轮管理+Files；Commands/PTY/Sessions/通用Ports/公开SDK独立阶段 | Preview现有授权修补不等于实现通用Ports |
| 目录 | 顶层不搬、包不改名，只消除真实边界耦合 | 不借统一API重新设计Runtime |

规范性建议集中在 [specs/unified-sandbox-api.md](../../specs/unified-sandbox-api.md)。

## 2. UID/GID结论校正

前份报告从`process.go`没有设置Credential指出应检查实际身份，这个检查方向正确，但不足以推断现有Process以root执行。

本次继续追到实际启动入口：

- `images/linux-base/isodo-agent:2`：setpriv 1000:1000、clear-groups、no-new-privs，然后exec Guest Agent。
- `internal/node/lima/base.go:243`：systemd User=nobody、Group=nogroup、NoNewPrivileges，workspace/HOME由相同用户拥有。
- `internal/guest/agent/process.go:171`：子进程继承Agent的OS身份；`USER=nobody`仅环境值。CHV实际UID1000与该USER字符串不一致，不表示提权，但不能作为身份展示依据。
- `images/base/isodo-t3:7`：T3单独setpriv 1000:1000。T3与Guest通用Process不可混作同一启动路径。

因此优先做标准启动路径的实测身份与权限测试，校验`id -u/-g`、supplementary groups、NoNewPrivs、workspace写入、系统目录拒绝。运行中VM证据本轮未采集。非标准root Agent启动不应直接获得租户Process准入；若补guard需检查T3初始化调用是否受影响，不能直接给全部进程强行setuid1000或对持久盘递归chown。

## 3. 目录Review的证据与结论

已运行`go list -json ./internal/... ./cmd/...`检查真实import，并读取App各package.json、生成脚本、CLI入口和调用方。

详细图表见[目录职责](../../specs/directory-responsibilities.md)，核心结论：

1. api/是机器协议源，internal/api是生成产物，二者保留。
2. control/orchestrator是RPC客户端，node/orchestrator是节点服务与编排；不是重复模块，保留。
3. node/orchestrator确实依赖localapi，原因是AgentConfigWriteResult返回类型；移除专用agent-config时消除这条反向依赖即可，不新增抽象框架。
4. app/apps/server是Guest内T3 Runtime，不是GoControl的重复实现，保留。
5. app/packages/contracts与client-runtime服务于T3协议；sandbox-client则含E2B形状API与mock，且仍被mobile使用，不得整体删除或一键替换。
6. cmd/isodo-template混有发布CLI与验证probe，依赖较厚，但它们当前提供真实验收入口。本轮保留，后续再按调用证据拆可复用probe；不新增gateway进程。
7. specs的状态/历史目标过时，需修订事实与权威来源，而不是移动全部文档或再造一套架构索引。

新增证据：`FilePreviewPanel.tsx:58,1270`实际引用`FileBrowserPanel`，后者引用E2B上传hook。因此不能声称此组件在仓库中无调用。用户要求删除旧面板的目标保留；实施时必须同步拆除其入口和引用，保留FilePreviewPanel其余任务预览/附件功能，不能只删文件造成编译或T3回归。

## 4. 最小逐项变更清单（未来实施；本轮不执行）

文件名标“新增”为建议位置，不代表已实现；迁移号在实施时重新核对。每一项单独验收，禁止先批量改所有目录再补测试。

### M0：冻结契约与修订文档

- 文件：`specs/unified-sandbox-api.md`、`specs/sandbox-lifecycle.md`、`specs/guest-protocol.md`、`specs/directory-responsibilities.md`、`api/openapi/README.md`、`docs/secure-connections.md`。
- 原因：统一desired/动作/删除、等待revision、UID/路径和生成职责；标明旧Operation/Redis必需/PTY完成状态等被替代条款。
- 风险：多份文档继续互相冲突，使实现各选一套。更新既有入口和替代关系，不删除历史证据。
- 测试：链接/状态交叉核对，字段对照表；本项不运行生成器、不改proto/OpenAPI实现。

### M1：Delete outbox与清理闭环

- 文件：`internal/control/postgres/store.go`、`internal/control/operation/lifecycle.go`、`internal/control/sandboxworker/worker.go`、`internal/control/sandbox/http.go`、新增`migrations/0012_sandbox_lifecycle_safety.sql`（建议名）、相应`lifecycle_integration_test.go`/worker/http测试。
- 原因：已有Control Delete仅软删，Node已有delete动作未接入；补删除意图、清理完成记录、内部job与tombstone。
- 风险：删错实例或在途create重建、资源未清就释放配额、迁移把旧deleted_at误当清理完成。旧迁移不重写，旧软删除先按未知清理处理。
- 测试：运行中/停止/创建中删除、重复删除、跨账号404、Node离线、清理部分失败重试、确认后盘和relay消失、未确认时占用保留；Postgres真实事务+两后端VM验收。

### M2：生命周期fencing与revision等待

- 文件：`internal/control/postgres/store.go`、`internal/control/sandboxworker/worker.go`、`internal/node/orchestrator/lima.go`、`chv.go`、`internal/node/localapi/{server,sandboxes}.go`、`api/openapi/openapi-public.yaml`、`internal/control/sandbox/http.go`；迁移复用M1安全迁移以免重复字段。
- 原因：完成写入CAS revision/generation/删除意图；本地Next/Apply/Commit串行；公开last_action及幂等记录；等待只认target_revision。
- 风险：错误回滚desired、把迟到成功当最新成功；相同幂等键返回另一个revision；stop失败被误认为停止。CHV恢复仍先fail-closed，不为接口统一擅自重建VM。
- 测试：相同/不同输入幂等、并发start/stop/delete、worker租约丢失、旧generation/boot/epoch、HTTP丢回复、旧revision完成不能写新状态、等待被替代/超时/再次等待；Local/Remote同fixtures。

### M3：进程实际身份与工作区权限（优先测试，必要时最小guard）

- 文件：`internal/guest/agent/process_test.go`、`internal/node/lima/manager_test.go`、CHV验证入口`cmd/isodo-template/probe_node_t3.go`或既有tests/validation；若测试暴露不安全启动，再改`internal/guest/agent/{server,process}.go`、`images/linux-base/isodo-agent`、`internal/node/lima/base.go`对应最小位置。
- 原因：验证真实OS身份而非USER环境变量；标准启动已经降权，避免重复setuid设计。
- 风险：测试程序权限不足时错误假通过；强行统一UID破坏既有盘、T3及配置写入；root guard误伤内部受信任初始化。
- 测试：两VM实际id/groups/NoNewPrivs；工作区可写、系统目录不可写；env USER伪造不改变权限；非标准privileged Agent拒绝租户执行；T3、Agent配置回归。单元测试中的宿主UID不等于VM实际UID。

### M4：现有Preview与Relay绑定实例

- 文件：`internal/control/preview/preview.go`、`internal/control/sandbox/http.go`、`internal/control/orchestrator/relay.go`、`internal/node/orchestrator/{chv_relay,chv_applink,apprelay}.go`、必要时`api/proto/isodo/node/v1/node.proto`，及现有preview/relay测试。
- 原因：claim、路由、RelayOpen绑定当前运行实例；移除受控路径generation=0绕过；stop/delete撤销旧route/token。
- 风险：T3新会话配对失效、stop/start后旧Cookie被错误续用、节点重启缓存路由指向旧实例、误开放Guest内部端口。
- 测试：现有T3首次/重连、全新Cookie store、过期/篡改/跨账号、同sandbox新boot旧token拒绝、stop期间在途WS关闭、无NIC CHV仍可连接。仅修现有入口，不新增公共Ports API。

### M5：Sandbox管理契约与App adapter同批迁移

- 文件：`api/openapi/openapi-public.yaml`、`internal/node/localapi/{server,sandboxes,templates}.go`、`internal/control/sandbox/http.go`、`internal/control/postgres/queries/templates.sql`、`internal/control/postgres/templates.go`；`app/apps/desktop/src/{main,preload,shutdown}.ts`、`app/apps/web/src/env.ts`、新增`features/isodo/sandboxClient.ts`及adapter、现有`sandboxQueries.ts`、`usePlatformSandbox.ts`、`pauseSandbox.ts`、`CreateSandboxDialog.tsx`、`CloudPages.tsx`。
- 原因：统一get/list/create/update/start/stop/delete/模板/App-link，去operation/state双格式；Ready default过滤在服务端；保留真实本地和云端transport差异。
- 风险：只改renderer漏掉退出暂停；生命周期轮询盲比VM状态；模板total不一致；没有bridge时误进mock路径。IPC错误需序列化DTO，不依赖Electron保留Error属性。
- 测试：相同管理fixtures/分页/错误码；revision等待；创建name缺省；旧路由404/405；退出暂停与原工作台导航/T3配对；本地/远端同用例。按生成脚本更新产物，不手改生成类型。

### M6：通用Files后端与流适配

- 文件：`api/openapi/openapi-public.yaml`、`api/proto/isodo/node/v1/node.proto`；新增`internal/control/sandbox/files.go`、`internal/control/orchestrator/files.go`、`internal/node/orchestrator/files.go`、`internal/node/localapi/files.go`（建议位置）；`internal/guest/agent/files.go`原则上复用，仅已有错误映射/元数据不足经测试证实才改；`cmd/isodo-api/main.go`、`cmd/isodo-node/main.go`装配。
- 原因：打通Control→Node→现有Guest Files；HTTP/IPC统一业务，Node做实例准入；API adapter从RPC结果映射统一FileInfo。
- 风险：无界缓存/暂存、multipart大小混淆、取消留下临时文件、symlink竞态、COMMIT_UNKNOWN被当作未写成功；普通文件接口影响内部目录。
- 测试：文本/零字节/二进制/中文名、大小与SHA256、256MiB边界/磁盘满、并发限额、慢消费者背压、两端取消/超时、原子覆盖、overwrite=false冲突、旧实例/跨账号/路径穿越/symlink；下载实际hash比对。
- 最小约束：不新增上传job资源、Environment服务或envd；不替换gRPC。暂存有上限/清理，完整性元数据与提交确认在同一FileClient中处理。

### M7：配置写入迁移与旧面板接线删除

- 文件：`app/apps/web/src/features/isodo/applySandboxCredentials.ts`、`agentConfigs.ts`、统一client Files adapter；`app/apps/desktop/src/{main,preload}.ts`、`app/apps/web/src/api/transport.ts`；`internal/node/localapi/agent_configs.go`、`internal/node/orchestrator/agent_config.go`、localapi/server.go及cmd/isodo-node装配；`components/files/{FileBrowserPanel,FilePreviewPanel}.tsx`、`hooks/useSandboxFileUpload.ts`、对应测试。
- 原因：配置helper按实际CODEX_HOME/HOME路径调用通用写文件；移除agent-config HTTP/IPC及Runtime→localapi结果类型依赖；拆旧E2B上传接线。transport不能对multipart/二进制强加JSON header。
- 风险：写到HOME/.codex错误位置；默认覆盖被暗改；把真实Key写入Guest；误删除仍被T3预览使用的组件；移动端依赖sandbox-client被破坏。
- 测试：Codex/Claude/OpenCode实际目标路径、文件只含占位符、修改覆盖保留既有语义；没有agent-config或旧E2B上传调用；保留T3预览/附件/文件工作流；mobile/shared包类型检查。

### M8：生成/文档/一致性验收

- 文件：生成的`internal/api/public/api.gen.go`、`internal/api/node/v1/*.pb.go`、`app/apps/web/src/api/public.generated.ts`、`internal/control/postgres/dbsqlc/*`和docs-site API页（只在对应源变化时生成）；新增共享contract fixtures建议`tests/contracts/sandbox/`，文档更新不假装真实E2E成功。
- 原因：协议源、实现、调用方只有一套契约；防止手改生成文件漂移。
- 风险：顺手生成无关admin改动、fixtures只被单端消费、fake测试代替真实VM证据。
- 测试：generate-check、Go相关包、Web/Desktop类型检查与定向测试；共享fixtures分别驱动local/cloud handler；真实Lima及无NIC CHV管理+文件完整流程、T3新会话回归，分别记录证据。

## 5. 实施顺序与规模约束

M0确认 → M1/M2风险闭环（关联设计，不独立绕过fencing）→ M3/M4现有安全边界 → M5管理迁移 → M6/M7 Files与配置迁移 → M8验收。

Commands/PTY/Sessions、公共Ports、公开SDK以及大范围目录调整均不进入此清单。本轮没有修改M1–M8的实现文件，只记录未来变更面；不以本报告作为提交或部署授权。
