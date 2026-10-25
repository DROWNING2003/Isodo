> 当前待审规格以 [specs/unified-sandbox-api.md](../../specs/unified-sandbox-api.md) 为准。本文保留最初输入，后续更正包括直接替换旧路由、通用文件上传/下载、删除未使用的旧文件面板。

# 本地与远端 Sandbox API 统一

实施分支：`drowning/unify-sandbox-api`。用户已明确：直接移除旧本地路由和旧字段，不保留兼容入口；统一 SandboxClient 作为业务入口。

本地与远端沙箱管理 API 统一计划
目标
让 App 的业务层只维护一套 SandboxClient 调用方式。切换本地/远端时只替换 transport 和 base URL/auth 配置；列表、创建、单沙箱状态、启动、停止、删除、模板、App-link 的请求与响应模型保持一致。

统一后的前端边界
前端业务层只依赖以下接口：

ts


interface SandboxClient {
  list(params?: { limit?: number; offset?: number }): Promise<SandboxList>;
  get(id: string): Promise<Sandbox>;
  create(input: CreateSandbox): Promise<Sandbox>;
  start(id: string): Promise<Sandbox>;
  stop(id: string): Promise<Sandbox>;
  delete(id: string): Promise<void>;
  listTemplates(): Promise<TemplateList>;
  createAppLink(id: string): Promise<SandboxAppLink>;
}
本地和远端分别实现同一个 transport/client adapter：

Web cloud adapter：请求 /v1/...，使用现有 Cookie/Bearer/CSRF transport。
Electron local adapter：通过 IPC 访问 sidecar，但 IPC 返回统一 JSON；sidecar 继续持有本地 Bearer token。
UI、React Query、生命周期和错误展示不再判断 local 或 cloud 的响应字段。
统一 API 契约
Sandbox
统一响应结构：

json


{
  "id": "...",
  "name": "...",
  "metadata": {},
  "template_id": "...",
  "template_build_id": "...",
  "template_digest": "...",
  "desired_state": "running",
  "observed_state": "running",
  "created_at": "...",
  "updated_at": "..."
}
统一状态枚举：

desired_state: running | stopped
observed_state: requested | preparing | starting | running | stopping | stopped | failed | error | unknown
请求和响应
操作	统一请求	成功响应
列表	GET /sandboxes?limit&offset	200 {items,total,limit,offset}
单个	GET /sandboxes/{id}	200 Sandbox
创建	POST /sandboxes，{name,template?,metadata?}	202 Sandbox
启动	POST /sandboxes/{id}/start	202 Sandbox
停止	POST /sandboxes/{id}/stop	202 Sandbox
删除	DELETE /sandboxes/{id}	204
模板	GET /templates?scope=available&limit&offset	200 {items,total,limit,offset}
App 入口	POST /sandboxes/{id}/app-link	200 SandboxAppLink
创建、启动、停止都返回当时的 Sandbox 快照；异步完成由客户端比较 desired_state 与 observed_state，不把 operation ID 暴露成业务必需协议。内部 operation 可以继续存在。

SandboxAppLink
两端统一返回：

json


{
  "sandbox_id": "...",
  "service": "t3",
  "url": "...",
  "pairing_url": "...",
  "status": "ready",
  "expires_at": "...",
  "capabilities": ["terminal", "files"]
}
本地可以令 url 与 pairing_url 为同一条本地直连配对地址；远端必须保证 pairing_url 的 Gateway 认证步骤能被 App 执行并建立 preview Cookie。

Error
统一为：

json


{
  "error": {
    "code": "SANDBOX_NOT_FOUND",
    "message": "sandbox not found",
    "retryable": false,
    "request_id": "...",
    "field": "..."
  }
}
实施阶段
Phase 1：冻结契约和生成类型
修改 OpenAPI，补齐本地/远端共用的 Sandbox、SandboxList、SandboxAppLink、Error 和状态枚举。
明确模板列表的 available 语义：所有可创建模板，必须有 Ready 的 default build；base 是默认项，T3 由基础模板保证。
重新生成远端 TypeScript API client。
添加 contract fixtures，覆盖创建、启停、列表、模板和 app-link 响应。
验收：生成类型与前端 adapter 编译；契约 fixture 同时能被 local/cloud adapter 解析。

Phase 2：先改本地 sidecar API
保留旧路由作为短期兼容入口，但新增/切换到统一返回结构。
GET /sandboxes 返回分页 SandboxList。
增加真正的 GET /sandboxes/{id}。
创建、启动、停止返回统一 Sandbox 快照；operation 只保留内部轮询或兼容字段。
将 GET /sandboxes/{id}/app 兼容为 POST /sandboxes/{id}/app-link，返回完整 SandboxAppLink。
将本地错误转换为嵌套 Error。
为每个路由增加 handler tests。
验收：本地 sidecar 可以用统一 HTTP contract 完成创建、状态收敛、启停、删除和 app-link。

Phase 3：对齐远端 API
保持远端现有资源模型，但检查所有成功响应与统一结构一致。
确认 GET /sandboxes/{id}、模板 available、分页和状态枚举完整可用。
统一 app-link 响应字段。
统一错误编码和 retryable 语义。
为 start/stop/delete/app-link 增加 API tests。
验收：同一组 contract tests 同时通过 local handler 和 cloud handler。

Phase 4：抽取前端 SandboxClient
新建共享的 SandboxClient 接口和统一领域类型。
把 React Query 列表、创建、启停、删除和模板读取改为只调用 client。
把当前 window.isodoDesktop 的本地分支移入 Electron adapter。
把当前 generated OpenAPI 调用移入 cloud adapter。
统一错误解析、状态映射、轮询和缓存更新。
业务组件只保留运行位置选择，不再处理两套响应字段。
验收：UI 业务代码不再读取 state、operation.state、observed_state 的分支格式；本地与远端使用同一套测试 fixture。

Phase 5：统一 App-link 和 Gateway 配对
本地 adapter 返回完整 SandboxAppLink。
远端 App-link 明确返回可执行的 pairing URL。
在连接层增加 Gateway bootstrap：先访问远端 auth URL，保存 preview Cookie，再读取 descriptor 和交换 T3 token。
确保 WebSocket ticket 和 /ws 请求携带正确的会话凭证。
增加本地直连和远端 Gateway 两条连接测试；验证首次连接、重连、过期链接。
验收：新客户端不需要手动打开 URL；本地和远端都能完成 descriptor、token exchange、session、WebSocket 连接。

Phase 6：迁移和清理
运行 local/cloud contract tests、前端定向测试、Go handler tests 和类型检查。
保留旧本地路由一个迁移周期，记录调用方并移除 App 对旧字段的依赖。
更新 API 文档、架构说明和运行手册。
进行真实本地 E2E 和真实远端 E2E；远端必须包含全新浏览器/客户端状态下的 Gateway Cookie 流程。
关键依赖
代码块


OpenAPI / domain contract
  -> local sidecar handler
  -> cloud handler
  -> local/cloud adapters
  -> shared UI lifecycle
  -> App-link Gateway bootstrap
  -> E2E verification
风险与处理
风险	影响	处理
本地 sidecar 通过 IPC 暴露而非页面 HTTP	高	IPC 只做 transport，返回统一 JSON，不把本地细节带入 UI
远端 Gateway Cookie 未建立	高	将 bootstrap 变成连接层明确步骤，并以全新会话 E2E 验证
本地没有公开单沙箱查询	中	增加 GET /sandboxes/{id}，统一轮询该接口
模板列表存在 Ready 记录但工件不可下载	中	服务端只发布通过 T3 probe 的 base；模板 endpoint 只返回已解析的 Ready default
operation 仍被旧 App 使用	中	先兼容返回和旧路由，再移除业务依赖
跨域 Cookie/CSRF 与 base URL	高	cloud adapter 明确 credentials、CORS、CSRF；不把 same-origin 假设带入通用 client
不纳入本轮统一
安全连接/injection 业务接口，继续按独立进度推进。
模板 Dockerfile 构建器、公开 TS/Python SDK。
账号、API key 和登录页面。
T3 终端/文件/WebSocket RPC 协议本身；只统一其连接入口。
分阶段验证命令
前端：pnpm --filter @t3tools/web test ...、pnpm --filter @t3tools/web lint ...
Go：go test ./internal/node/localapi ./internal/control/sandbox ./internal/control/...
契约：同一 fixtures/contract test 分别调用 local/cloud handler
最终：本地创建→连接→启停→删除；远端全新会话创建→Gateway bootstrap→连接→启停→删除
交付门槛
在开始大范围实现前，需要确认两项决策：

本地旧路由是否保留一个迁移周期。
是否接受 SandboxClient 作为 App 唯一业务入口，local/cloud 差异只存在于 transport adapter 与 auth 配置。