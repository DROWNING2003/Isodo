# 黑客松关键路径：统一 API → 双运行环境 → 真实 Agent Demo

日期：2026-10-25；截止以项目参赛要求的2026-10-26 09:00为限，冻结前核对赛方时区。

状态：最终实施计划待确认。本轮只修订文档、核对代码及只读检查测试机；不修改实现、提交、同步部署、启动/停止VM或服务。先前worktree草稿继续保留，不算已完成。

## 已确认部署拓扑

```text
GitHub OAuth 公网回调 → 现有 ngrok 认证入口 → 本机认证服务
App → 本机 Control API / 数据库
Control → SSH Local Forward + mTLS → jfcs1024 Node → CHV/Vsock → Guest/T3
App → 本机 Preview Gateway（Host 路由）→ Node Relay → Guest/T3
Guest → 模型 API：独立验证出站与 injection
本地沙箱：Electron IPC → 本机 sidecar → Lima/Guest
```

ngrok 主要服务现有 GitHub OAuth 登录，不要求 Sandbox API、Node RPC 或 Preview 全部公开。4040 管理接口不可达不作为 Sandbox 阻塞项。App 优先使用本机 Control；保留现有 OAuth Session 所需认证 origin，不强制迁移登录方式。

Control 通过 `127.0.0.1:19190` SSH 本地转发访问远端 `127.0.0.1:9190`，仍验证 mTLS CA、ServerName 和 Node 身份。已建立本机转发；实测当前本机证书与远端服务证书不匹配，尚未通过 mTLS，不能跳过校验。

Preview 使用现有 `<port>-<sandbox_id>.<domain>` Host 路由，优先以本机 DNS/hosts 或保留 Host Header 的请求验收。公网通配子域名不是本地验收前提，不新增 DNS 服务、网关或复杂代理。命令行 Host 路由成功仅代表传输通过，Cookie/descriptor/WebSocket 仍需独立验证。

本机认证 Cookie 是 host-only、HttpOnly、SameSite=Lax，Secure 取决于配置 origin scheme；Preview Cookie 同样使用 Lax，Secure 取决于 Preview scheme。保留现有 HTTPS OAuth 入口，不把其 Cookie 假定可用于 localhost。先核实 T3 配对和实际浏览器 Cookie，再决定最小本地域名兼容方式，不重构 Gateway。

## 1. 最终P0范围

- 一个feature内SandboxClient，Local IPC与Remote HTTP仅在adapter区分；同一CRUD/状态/错误/分页/模板/App-link契约；保留本地Lima真实流程。
- create/get/list/update/start/stop/delete基本语义、last_action、按lifecycle revision等待；基本幂等/冲突与旧完成CAS，重试不重复创建VM。
- 默认模板available必须可创建，生产远端路径不落入mock。
- 尽早打通真实Remote CHV→Guest Ready→App-link/Gateway→T3工作台，再验证Agent真实模型请求和任务结果。
- 通用Files打通Local与Remote，迁移Agent配置，默认overwrite=true，可显式false；保留SHA256、限长、原子提交、取消/有界缓冲与路径边界。
- 最小真实Delete清理及可观察失败、归属/实例身份/过期凭证检查，不能以“Demo”绕过隔离。

不做公开SDK、通用Commands/PTY/Sessions/Ports、Environment API、完整灾难恢复、跨节点接管、全项目目录整理；保留现有架构和T3协议。

## 2. 冻结两个版本概念

生命周期revision仅在create/start/stop/delete新意图接受时递增，普通PATCH name/metadata不改变它，也不改变last_action。PATCH更新updated_at；本轮不为普通属性更新引入新的公开resource version/ETag系统，未来确需乐观锁再增量加入。

同revision根据last_action.status和目标事实等待；更高revision返回ACTION_SUPERSEDED，不能只看VM碰巧running。HTTP/等待超时不自动取消后台动作，不自动重发创建。

VM desired只有running/stopped；删除事实独立。Delete不得只隐藏DB：先记录删除意图、拒绝新操作，再用现有Node Delete和outbox/worker确认清理。未完成返回202/可查询状态，不宣称磁盘已删；失败保留归属/占用及错误，不做完整后台治理平台。

## 3. 调整后的实施顺序和验收门槛

### 双轨并行，预检不等App迁移

```text
轨道A：S1最小契约 → S2统一API/App → S4 Files/配置
轨道B：P0远端关键链路预检（立即开始，独立于S1/S2）
                         ↓
          S2最小可用 + 预检结论 → S3真实远端App验收 → S5双端Demo
```

并行指工作依赖可并行，不代表新增服务或自动启动多Agent。本轮仍只调整计划；批准实施后，首先采集预检证据，同时推进不依赖该结果的契约/adapter工作。预检失败立即记录并决定最小修复，不等App全部改完。API统一不是补网络能力的替代品。

| 预检 | 直接验证内容 | 通过证据 | 失败时动作 |
| --- | --- | --- | --- |
| P0-A OAuth/本机 Preview | OAuth 隧道是否正常；本机 Gateway 能否按 Host 路由；全新Cookie store走认证URL→Cookie→descriptor/T3配对；不能只测API 200 | 外部客户端实际请求的Host/目标、认证状态、descriptor/session/WS结果；脱敏记录 | 分别核实 OAuth 入口和本机 Host 路由；提出最小路由选择供确认，不默认有通配子域名、不重写整个Gateway |
| P0-B CHV创建策略 | 本机Control经mTLS/SSH访问远端Node；核对ALLOW/NetworkBridge组合；新建受控无NIC实例并确认Guest Ready、入站Vsock/T3可达 | 同一sandbox/revision的创建结果、Ready identity摘要、实际VM网络配置与清理结果 | 先修请求与既定无NIC策略不一致的最小接线；不默认启用bridge/TAP、不改服务器全局网络 |
| P0-C 模型出站/凭证 | Guest到目标模型API的真实DNS/TLS/HTTP通路；占位符经宿主注入有效认证；再做一次真实Agent请求 | 上游接受且模型返回；Guest配置无真实Key，日志无敏感材料；Agent任务与普通命令分开记录 | 没有现成安全通路则明确硬阻塞，单独确认最小出站/注入接入方案；不写真实Key、不用mock或离线命令冒充模型成功 |

先只读核对现有配置，使用已有测试工具和最少量隔离实例；任何需要改网络/授权边界的修复先形成具体选择。P0-B、P0-C可以先用受控Node/Guest探针验证，无需等通用Files或新App页面。探针成功仍不能代替S3的真实App集成。

三项分别报告通过/失败/未验证，不合并成“远端连通”。P0-A是进入T3的门槛，P0-B是可靠创建运行的门槛，P0-C是宣称真实Agent Demo的门槛；未通过时可继续本地API/Files开发，但不宣称远端验收完成。


| 阶段 | M项与依赖 | 最小工作 | 可执行验收/通过门槛 |
| --- | --- | --- | --- |
| S1 最小契约 | M0+M2必要部分；起点 | 冻结管理+Files DTO、last_action/revision、PATCH不影响动作、幂等/冲突、overwrite=true；记录未完成草稿 | contract fixtures两端可用；测试普通PATCH不替代start；旧revision完成CAS拒绝；先不做完整reconcile |
| S2 统一App管理 | M5+M2基本并发；依赖S1 | 本地handler/IPC与Control同契约；feature client+两个adapter；删除业务双格式/mock fallback；退出暂停同步迁移 | 同一业务fixture覆盖Local/Remote CRUD、templates/App-link/错误；Web/Desktop类型检查；真实Lima创建/查询/启停不回归 |
| S3 尽早远端App验收 | M8早期+M4直接路径+M3实测；依赖S2最小可运行及并行预检结论 | jfcs1024上真实CHV/Guest/T3；真实认证与外部URL；Gateway bootstrap；模型访问/凭证可用性验证 | App创建远端→运行态→新会话配对→工作台；随后Agent完成任务。工作台成功与Agent成功分开记录；有阻塞立即报告，不等S4全部做完 |
| S4 通用Files/配置 | M6+M7；依赖S1/S2，利用S3反馈 | Control→Node→现有GuestRPC；Local同DTO；配置走通用Files，撤agent-config；删E2B上传接线保留预览附件 | 两端写/读零字节、文本、二进制hash；配置真实路径/占位符/默认覆盖/显式false；取消与超限不残留；用新配置补齐S3 Agent验收 |
| S5 最终双端Demo | M8收尾；依赖S3+S4及最低安全边界 | 同一App业务流和证据；运行中/停止态真实Delete清理、重启身份拒旧连接 | Local与Remote各创建→工作台→Agent真实任务→文件读写→stop/start→清理；Remote需全新Cookie store，无mock；明确未完成项 |

S3若缺Agent配置，先尽早验证真实工作台，不能将该阶段标为全部通过；S4配置完成后立即补Agent任务。模型出网或注入缺口不能靠“文件已写入”补足，见下一节阻塞项。

### M1/M3/M4怎么插入，而不是整套前置

- M1最小Delete与M2防迟到写作为同一安全切片，最晚在S2对外开放删除前完成。尚无真实清理能力时UI暂禁删除并说明，测试只使用受控Node Delete清理；这不算完整CRUD验收，S5必须补齐。复杂自动接管/历史修复延期。
- M3首先在S3真实VM验证身份和权限；标准路径通过则不做UID重构。发现越权才最小修复，禁止全盘chown。
- M4只补S3公开入口涉及的归属、当前实例、过期Token、停止/删除撤旧路径，保持T3协议；不等完整Gateway/Ports重构。
- M8从S1 contract开始，每阶段记录证据，不是最后一次全仓测试。

## 4. Remote CHV App真实阻塞项

| 项 | 状态/证据 | 最小处理与门槛 |
| --- | --- | --- |
| KVM主机 | 2026-10-25只读确认jfcs1024 Linux x86_64，/root/zkp存在，/dev/kvm可O_RDWR打开 | 设备可用不等于VM测试通过；后续使用隔离run目录 |
| 现存Node/CHV | 主机有isodo-node与CHV进程；Node监听127.0.0.1:9190，exe=/var/lib/isodo/bin/isodo-node | 不替换现有进程/状态目录；版本与模板工件匹配仍待验 |
| Node/Guest/Vsock/Relay/T3 | 源码已有实现与probe；非完整App证据 | 同模板真实Guest Ready和T3 probe，再测外部App流程；不重写Runtime |
| 统一管理调用 | 旧本地与Control不同；worktree已改部分DTO但测试未全过 | 接续草稿，补cloud202、last_action/revision、single get、IPC、client和退出调用方 |
| 本机API与OAuth分别可达 | 用户已确认API/Control运行本机，jfcs1024只运行Node；真实ngrok/auth/callback链路待验 | API使用本机→SSH→远端Node的mTLS通路；App优先本地访问API，保留认证所需origin。核对相对URL/同源/CSRF，不把App base URL改成远端Node地址 |
| App-link/Gateway bootstrap | Control有publisher/cookie；App resolvePairingUrl直接取pairing_url再connectPairing | 验证是否实际访问Gateway认证URL建立Cookie，不只解析fragment；最小连接层补步骤，全新cookie store通过 |
| CHV创建出网策略冲突 | worker.go:186固定EGRESS_MODE_ALLOW；chv.go:154在无NetworkBridge时拒绝该请求 | 无网卡管理/配对测试应使用匹配的deny策略，不为API统一默认开网卡；模型访问单独确认 |
| 无NIC模型出网/真实凭证 | 现有入站PortForward不证明出站可用；cmd/isodo-node/main.go:67只为Lima接injection Manager | 并行P0-C立即检查已部署环境是否有现成安全出站/注入通路，不等S2结束。当前源码未证明CHV有等价通路，不能宣称改Files就能完成真实Agent |
| Agent配置写入 | 本地专用agent-config已实现；远端公开Files与CHV配置写入缺通道 | S4复用Guest Files，CODEX_HOME/HOME固定布局；实际Key仍需合法的注入通路 |
| Delete | Control只软删除；Node有真实Delete | 最小outbox/worker接线、清理确认、拒绝新准入；不先建完整reconcile |
| 标准UID/GID | CHV镜像setpriv1000，Lima unit nobody/nogroup；源码已证明降权入口 | S3实际id/groups/NoNewPrivs/workspace权限，未发现问题不重构 |

### 不能默默消除的约束冲突

“无网卡CHV”与“在线模型Agent Demo”需要一条真实出站路径。当前代码有已有可选bridge/TAP出网，但本轮约束不允许默认打开；现有安全注入也仅接Lima。

计划优先核实测试环境是否已有可复用的安全路径。若没有，必须显式选择：单独确认最小Vsock出站/注入接线；或由用户明确调整Demo网络/凭证边界。不得擅自启用bridge、把真实Key写进Guest、用预置回复或普通pwd命令代替Agent模型任务。此项是源码发现的具体阻塞，不是顺势增加平台架构。

## 5. 最小文件变更清单（待实施）

沿用[逐项清单](../reviews/sandbox-api-design-revision-2026-10-25.md)的风险与测试，本表按关键路径裁剪；生成产物只从源生成。

| 切片 | 涉及文件 | 复用/必要变化 |
| --- | --- | --- |
| S1契约与草稿 | api/openapi/openapi-public.yaml；internal/control/sandbox/http.go；internal/node/localapi/{server,sandboxes}.go | 已有草稿保留，补最小动作信息/revision，不重新设计；生成admin无关漂移不带入 |
| M2基本fencing+M1最小清理 | internal/control/postgres/store.go；operation/lifecycle.go；sandboxworker/worker.go；Node orchestrator/{lima,chv}.go；一份新增安全迁移 | outbox接Node Delete、完成CAS、幂等与删除事实；不重写0008等旧迁移，不实现跨节点恢复 |
| S2模板与App | postgres/queries/templates.sql及生成；web/features/isodo新增sandboxClient.ts/adapter；sandboxQueries/usePlatformSandbox/CreateSandboxDialog/CloudPages/pauseSandbox；desktop main/preload/shutdown；web/env.ts | 业务统一；Ready default服务端过滤；页面不解析operation/state双格式，不改mobile共享包 |
| S3真实连接 | web连接/Isodo配对入口；control/preview、control/orchestrator/relay、node/chv_relay按必要处最小改动 | 真实Gateway Cookie bootstrap与实例检查；不更换T3 token/WS协议，不新增Ports API |
| S4通用Files | Node proto；control/sandbox/files.go、control/orchestrator/files.go、node/orchestrator/files.go、localapi/files.go（建议新文件）；现有Guest files.go尽量不改；cmd入口只装配 | 相同Files DTO、受控路径、流式/有界暂存、SHA256/原子提交 |
| S4配置/旧接线 | applySandboxCredentials.ts、agentConfigs.ts、web/api/transport.ts、desktop main/preload；localapi/orchestrator的agent_config.go | 配置迁移后撤专用入口，消除orchestrator→localapi返回类型依赖；默认overwrite=true |
| S4旧面板 | FileBrowserPanel/FilePreviewPanel引用、useSandboxFileUpload.ts及测试 | 当前确有调用；同步删除旧入口/上传接线，保留T3预览附件和代码功能，不删整个sandbox-client包 |
| 各阶段证据 | 对应handler/worker/postgres/relay/files测试、共享fixtures、docs/validation新记录 | contract、Lima VM、CHV VM、App/T3分别标记通过/失败/未跑 |

## 6. 可执行验收与测试机边界

### 现在已允许的只读检查

```sh
ssh jfcs1024 'cd /root/zkp && pwd && uname -sm && test -c /dev/kvm'
```

已核实KVM可打开；远端go/cloud-hypervisor不在PATH。现存CHV exe为`/root/zkp/isodo-validation/bin/cloud-hypervisor`，工具目录`/root/zkp/.tools`。后续按绝对路径使用，不安装/替换系统版本。

### 实施批准后，阶段测试命令

```sh
# 本地worktree；各切片只运行相关项
go test ./internal/node/localapi ./internal/control/sandbox ./internal/control/sandboxworker
go test ./internal/guest/agent ./internal/node/orchestrator ./internal/control/preview
cd app
pnpm --filter @t3tools/web test src/features/isodo
pnpm --filter @t3tools/web typecheck
pnpm --filter @t3tools/desktop typecheck
```

远端首轮仅选一个经verify的模板，使用新run目录；CLI flags以同步后本次构建版本为准。下列是既有probe形态，不是本轮已执行命令：

```sh
# 先在本地Linux交叉编译；不依赖服务器Go PATH
GOOS=linux GOARCH=amd64 go build -o /tmp/isodo-template-linux-amd64 ./cmd/isodo-template
# 批准后传到 /root/zkp/isodo-api-unify/<run-id>/bin/，不覆盖 .tools/现存服务
# 远端该run目录内，设置经核对的工件路径及manifest digest（不含秘密）
./bin/isodo-template verify --dir "$ARTIFACT_DIR" --digest "$MANIFEST_DIGEST"
./bin/isodo-template probe-node-t3 --dir "$ARTIFACT_DIR" --digest "$MANIFEST_DIGEST" \
  --vmm-binary /root/zkp/isodo-validation/bin/cloud-hypervisor
```

Node probe只验证远端运行时。真实API/E2E使用本机Control经本地连接及mTLS/SSH访问远端Node，不在jfcs1024启动API服务。probe-api-t3的探针handler/认证结果也不能当作真实Casdoor、外部TLS/DNS或Electron Cookie E2E。只有App真实登录、创建、配对、Agent任务和结果才通过S3/S5。

运行记录必须包含：资源ID、拥有的PID/session、Node state目录、artifact digest、revision、Guest Ready identity摘要、每步结果。日志不记录密钥、完整配对链接、Cookie或材料。

仅使用`/root/zkp/isodo-api-unify/<run-id>`新增目录；每次只起少量测试VM，端口与现有9190分开。现有PID 1285/2845是只读观察值，不能拿旧PID当清理授权；后续重新核对身份。不批量kill、不清Lima/CHV列表、不改其他服务、系统防火墙/网络/证书/软件。只清理本轮记录拥有的Isodo实例与文件，不执行rsync --delete覆盖根目录。

## 7. 降级与不可跳过项

| 允许延期/降级 | 不能伪装或跳过 |
| --- | --- |
| 完整reconciliation、节点自动接管、历史删除修复治理 | 旧revision覆盖新意图、重试重复VM、Node未知时误认为已清理 |
| UID统一/目录美化 | 真实运行身份和写权限检查；不能放开root/任意Guest或宿主路径 |
| 大文件进度优化/断点续传 | 有界缓冲、取消、大小/SHA256、原子提交与二进制正确性 |
| Commands/PTY/Sessions/Ports、公开SDK | 保留已工作的T3终端/配对；过期、跨账号和旧实例凭证拒绝 |
| S3先展示工作台，S4后补配置 | 未真实请求模型前不能称Agent Demo成功；无安全出网/注入时明确阻塞 |
| 删除暂禁UI作为阶段性保护 | 最终必须有真实清理路径；软删不计资源回收成功 |

执行目标：远端三项预检与API统一并行，最早暴露远端App/模型链路问题，同时守住最小隔离边界；每阶段通过后再推进，不以整体架构重构阻塞Demo。
