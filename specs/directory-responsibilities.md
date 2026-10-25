# 目录职责与真实依赖（2026-10-25 Review）

状态：基于已合并 `e368919` 与当前 worktree 的源码/import/生成脚本核对。本轮只修文档；目录调整是建议，不执行源码移动。

旧文档关于“app未跟踪”“仅有Guest proto”“internal尚无Runtime”的描述已过时：当前Git跟踪app约3690个文件，Node/Guest proto与Runtime均有实现。约束与最终接口建议见[统一设计](unified-sandbox-api.md)。

## 1. 当前目录与职责

| 目录 | 真实内容/调用证据 | 结论 |
| --- | --- | --- |
| `api/openapi/` | openapi.yaml（auth/session/keys）与openapi-public.yaml（sandbox/template/injection/documents）；scripts/generate-api.sh + tools/openapi配置生成Go/TS | 保留。admin命名不等于Node内部管理面，文档说明边界；无需拆更多schema目录 |
| `api/proto/isodo/node/v1/` | Control→Node Apply/GetOperation/Inspect/AppLink/RelayPort等 | 保留；新增Files转发时更新机器契约。service边界先明确，不新建部署进程 |
| `api/proto/isodo/guest/v1/` | Ready/BindRuntime、Files、Process、输入/PTY声明、PortForward | 保留；区分已声明与已实现，不为E2B兼容替换gRPC |
| `specs/` | 设计、研究与历史目标混排，多个状态/分页/Operation/Redis约束冲突 | 保留路径；加状态/替代关系，统一设计指向单一契约来源，不批量搬动研究文档 |
| `internal/api/` | OpenAPI生成的admin/public handler/types，protoc生成的node/guest binding | 保留；它是产物，不是api/协议源的重复实现；禁手改 |
| `internal/control/` | auth、sandbox/template handler、postgres事务与sqlc、operation payload、worker、Node client、injection、preview | 保留；不直接操作VM。preview是用户入口代理，不是宿主出站injection |
| `internal/httpapi/` | 组合admin/public handlers、认证middleware及路由 | 保留；不属于另一套Control业务，不合并到cmd/main |
| `internal/node/` | localapi HTTP adapter、orchestrator server/journal、lima manager、guesttransport、vmm、templatebuilder、injection | 保留现有实现；只修少量边界反向依赖 |
| `internal/guest/agent/` | Guest身份、files/process/port_forward执行与测试 | 保留，Guest不拥有账号权限或宿主VM管理 |
| `internal/rpcsecurity/` | mTLS凭证与可信peer校验，Node/Control入口使用 | 保留共享低层模块 |
| `internal/templateartifact/` | manifest/S3/缓存/校验；Node/templatebuilder/CLI调用 | 保留共享工件库，不挪入control或app |
| `cmd/` | isodo-api/node/guest/template四个进程/工具 | 保留四入口；无须新建isodo-gateway/isodo-orchestrator进程 |
| `app/apps/web` | Isodo UI、generated API、T3工作台；管理调用仍有本地/云端与mock分支 | 保留，管理收敛到feature内client；不重写T3 |
| `app/apps/desktop` | Electron、IPC、本地Node生命周期、自动暂停 | 保留，不把Lima控制放回renderer |
| `app/apps/server` | Guest内T3 Runtime，build:bundle打包server及web client/native依赖 | 保留，它与GoControl是不同业务，不是两个重复服务端 |
| `app/apps/mobile` | 独立移动入口，仍消费E2B风格sandbox-client等 | 本轮保留；不能因桌面迁移就删除共享包 |
| `app/packages/contracts` | T3环境/RPC等契约，server/web/mobile/client-runtime消费 | 保留，与Isodo管理OpenAPI不是同一个协议 |
| `app/packages/client-runtime` | T3连接、授权与运行状态封装 | 保留，不等同于管理SandboxClient |
| `app/packages/sandbox-client` | 现有E2B形状api/envd/file等及cloud mock，web/mobile/shared依赖 | 不能整体删除或原地假装换成Isodo SDK；按调用方迁移隔离 |

## 2. 真实Go依赖图

依据本轮 `go list -json ./internal/... ./cmd/...`（不是按目录名猜测）：

```text
cmd/isodo-api
 → control/{auth,injection,postgres,preview,sandbox,sandboxworker,template,orchestrator}
 → httpapi
control/orchestrator → api/node + rpcsecurity + grpc（RPC客户端）
httpapi → generated api/admin/public + control handlers
control/postgres → control领域接口/payload + dbsqlc + generated api/public

cmd/isodo-node → node/{localapi,orchestrator,lima,injection} + rpcsecurity
node/localapi → api/node/public + node/injection
node/orchestrator → api/node/guest + guesttransport + lima + injection
                  + templatebuilder + templateartifact + localapi（问题边）
node/templatebuilder → node/{guesttransport,vmm} + api/guest + templateartifact

cmd/isodo-guest → guest/agent + api/guest
guest/agent → api/guest，不引用control/node/app
```

两个orchestrator包不是重复：control/orchestrator是Node RPC client；node/orchestrator是服务实现和编排。可用import alias提高可读性，不为名字相同移动整个包。

`cmd/isodo-template` 实际同时包含verify/build/publish及多种T3/API/Node探针，依赖Control、Node、Guest多个层。它是确实偏厚的CLI，但当前真实验收依赖它；第一轮不搬。未来只把可复用probe主体移入既有tests/validation或单独validation包，CLI保留命令调用；发布命令不是测试，不能一并删除。

## 3. 职责重复、反向依赖与处理

### 3.1 本轮应收敛

- 同一管理行为在localapi与Control handler各有DTO、错误、分页和状态映射。两adapter仍需存在，统一机器契约/fixtures和小型边界映射，不把认证、持久化或VM后端硬合并。
- App `features/isodo/sandboxQueries/usePlatformSandbox/CreateSandboxDialog/CloudPages` 直接使用本地IPC和generated client；集中到 `features/isodo/sandboxClient.ts` 与本地/云端adapter。本轮不新建“公共SDK”package或重构所有T3共享包。
- `node/orchestrator/agent_config.go` 返回 `localapi.AgentConfigWriteResult`，导致Runtime依赖HTTP层。通用Files迁移后消除专用返回类型和localapi import；HTTP映射放adapter，Runtime返回已有Guest FileInfo/写入结果或在既有runtime包内定义最小结果。无需先创建一层framework。
- `specs/directory-responsibilities.md` 旧状态与源码矛盾。本次更新事实；后续修订lifecycle/guest/工程生成说明的过时条款及链接，避免新增第五份权威API描述。

### 3.2 应保留，不能误判重复

- `api/` vs `internal/api/`：输入契约与生成产物。
- GoControl vs T3 `app/apps/server`：用户沙箱管理与Guest内Agent工作台。
- `control/injection` vs `node/injection`：账号加密材料管理与宿主注入执行，信任边界不同。
- `control/preview` vs Node apprelay/Guest PortForward：外部授权/HTTP代理、Node转发、Guest loopback连接，各自必要。
- Node lima/vmm/templatebuilder：Lima命令管理、CHV HTTP VMM适配、工件构建与启动逻辑不等价；templatebuilder/runtime.go确有运行态与构建态同包，但当前调用清晰，无需本轮搬目录。
- T3 contracts与Isodo OpenAPI：操作协议和管理契约不同，不合并生成流程。

### 3.3 延后且需调用证据

- mock cloud仍被CloudPages/CloudSidebar/CloudWorkspace及mobile使用；不能仅见mock字样就删包。先移除生产路径fallback和入口，再处理确无调用的mock。
- 用户要求删除的旧文件面板，须先定位路由/组件引用，删除未使用的面板接线与E2B上传hook；保留仍被T3任务使用的预览/附件/代码编辑部件。
- `cmd/isodo-template`探针收拢、templatebuilder/runtime包改名、apps/web server build关联整理，作为独立维护项，不阻塞管理/Files。
- 不创建旧“目标目录”中尚无需求的internal/control/redis、internal/gateway、internal/vmm/cloudhypervisor、isodo-gateway入口，不因整理目录引入组件。

## 4. 生成与App依赖证据

- `scripts/generate-api.sh`：oapi-codegen → internal/api/admin/public；orval → app/apps/web/src/api/admin.generated.ts/public.generated.ts。
- `tools/openapi/orval.config.mjs`：generated fetch使用web `api/transport.ts:isodoFetch`；它当前默认JSON/same-origin，Files二进制/multipart需在transport边界支持，不能全局强制JSON header。
- `scripts/generate-proto.sh`：api/proto → internal/api/node/guest；根Makefile聚合生成和docs-site生成。
- `app/apps/web`依赖client-runtime/contracts/sandbox-client/shared；mobile也依赖这些。server依赖contracts/shared/ssh/tailscale/effect-agent包，build bundle时还构建web并装入Guest工件。
- `app/apps/desktop`未通过workspace依赖复用web SandboxClient；使用preload IPC。TS DTO生成/共享须明确单一来源，不能由preload手写一套第三种状态。

## 5. 最小目录调整决定

本轮不搬顶层目录、不改包名、不新增服务。仅在现有目录中增加统一client/Files adapter及对应tests；删除专用agent-config导致的反向类型依赖；同步specs的事实和替代关系。完整逐文件最小变更清单见[设计修订清单](../docs/reviews/sandbox-api-design-revision-2026-10-25.md)。
