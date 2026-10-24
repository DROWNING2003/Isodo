# 工程技术栈与 API 生成约定

## 参考基线

本次使用本机 `keelctl v0.2.1` 在 `/tmp/isodo-keel-reference` 生成独立参考项目，没有将模板覆盖到 Isodo。模板来源为 `DROWNING2003/keel`，版本 `tpl/v0.1.12`，commit `fbb96cf2d5d0bc8a8cf0cd877212ef9d81f5cc57`。下面区分模板已有能力、Isodo 已采用部分和后续工作。

## OpenAPI 是 HTTP 契约源

`api/openapi/openapi.yaml` 定义控制面接口，包括网页登录、会话、API Key 管理与健康检查；`api/openapi/openapi-public.yaml` 定义供用户和 SDK 使用的 Sandbox CRUD。公开接口仍必须认证。新增和调整 HTTP API 时先改对应契约，然后运行 `make generate`，禁止直接修改生成产物。

| 层 | 工具与输出 | Isodo 用法 |
| --- | --- | --- |
| Go 路由和接口 | oapi-codegen `v2.8.0` | 分别从两个契约生成 `internal/api/admin/` 与 `internal/api/public/` 中的路由、ServerInterface、参数和模型 |
| Node 内部 RPC | Protocol Buffers + gRPC-Go | `api/proto/isodo/node/v1/` 是 Control Worker 到 Node Orchestrator 的契约；在 `make generate` 中生成 `internal/api/node/v1/`，不进入公开 API 文档 |
| 前端调用与类型 | Orval `8.42.0` | 分别生成 `admin.generated.ts` 和 `public.generated.ts`，每份文件内联客户端和模型类型 |
| 用户 API 文档 | Fumadocs OpenAPI | 仅将 `openapi-public.yaml` 生成为 `docs-site/content/docs/api-reference/endpoints/` 页面；控制面契约不发布 |
| 传输策略 | 手写 `api/transport.ts` | 同源 Cookie、CSRF、401 清理账号状态、错误文案；不手写各接口路径和 payload 类型 |
| 业务实现 | 手写 Go handler/store | 租户鉴权、输入校验、状态与数据库事务，不由生成器代替 |
| 漂移检查 | `make generate-check` | 重新生成并比较内容摘要，覆盖当前仍被忽略的 app 文件和新文件 |

生成 Go 代码固定使用根 `go.mod` 声明的工具链；生成工具配置和 Orval lockfile 放在 `tools/openapi/`。Fumadocs 站点和依赖锁文件位于 `docs-site/`。契约、生成配置与产物按控制面和公开 API 的边界分组；前端 `transport.ts` 是手写的 HTTP 传输适配器。旧合并客户端及 `models/` 生成文件由生成脚本清理。

```sh
make generate
make generate-check
make test-integration
```

根目录 `make generate` 汇总 OpenAPI 绑定、protobuf/gRPC 绑定和 Fumadocs API 页面生成。`make generate-proto` 使用根 `go.mod` 版本对应的 Go toolchain，固定 `protoc-gen-go` / `protoc-gen-go-grpc` 版本；开发机需安装 `protoc`。OpenAPI 生成和文档站不需要 `protoc`。

生成分为两个 Go 包，以便控制面和公开 API 各自维护 ServerInterface 与路由。所有生产路由和集成测试使用这些生成注册器。鉴权包裹生成的参数解码器，匿名请求即使 query 格式错误也先返回 401。Go handler 实现生成的 ServerInterface；不另维护同一组手写路由。

模板使用 Gin + strict-server。Isodo 当前使用标准库 `net/http` 的 oapi-codegen 适配器，保留现有 HTTP 中间件和测试；没有仅为使用模板而更换 Web 框架。请求和响应使用生成模型，语义校验仍在 handler 中完成。当前没有运行时 OpenAPI schema 全量校验器，不应将代码生成等同于自动完成了业务校验。

模板的 Orval 生成 TanStack Query hooks。Isodo 此次生成 Fetch 客户端；现有 T3 工作台尚未引入 React Query Provider，暂不为未使用的 hooks 增加运行时依赖。后续需要服务器状态缓存、分页和 mutation invalidation 时，可在同一 Orval 配置中启用 React Query；接口类型和传输策略继续共用。

## 模板中其他常用技术栈

| 领域 | keel 模板 | Isodo 当前情况与接入点 |
| --- | --- | --- |
| Go / 数据库 | Go 1.26.9、pgx v5、PostgreSQL | 已采用；tenant 范围必须在 SQL 查询条件中落实 |
| SQL 生成 | sqlc `v1.31.1`，schema 来自 migrations | 模板 Registry 已使用命名 SQL 和 sqlc 生成 pgx 类型，统一经 make generate；旧 Sandbox/auth store 继续保留原查询与隔离测试 |
| 数据库迁移 | Goose `v3.29.0` | 当前为编号 SQL + Makefile；后续改为有迁移版本记录的 Goose，已有数据库须设计基线接管，不能直接重跑建表 |
| 身份认证 | Casdoor、go-oidc、OAuth2 | 已采用 GitHub-only Casdoor、服务端授权码与 HttpOnly 会话 |
| Session / CSRF | scs/pgxstore、nosurf | 当前为数据库会话摘要、显式 Origin + CSRF；是否替换应按 Cookie/失效/CSRF 行为对照测试决定，不能并行维护两套会话 authority |
| 前端 | React、TypeScript、Vite、Tailwind、TanStack Query | Isodo 继承 T3 的 React/TypeScript/Vite+、Tailwind、TanStack Router 与 Base UI；OpenAPI 调用已生成 |
| 后台任务 | River + PostgreSQL | 尚未接入；可用于控制面耐久任务，VM 调度仍遵守 Node/Nomad 规格，不让两套调度器同时拥有实例生命周期 |
| 运行态 Registry / 路由 | Redis | Runtime 必需依赖；Control Worker 写运行中 Sandbox record 和 tenant active index，Node Orchestrator 写 Sandbox→Node routing record。Casdoor session、API Key、持久 Sandbox owner/desired state 仍以 PostgreSQL 为准。 |
| 对象存储 | AWS SDK v2 / S3 插件 | SeaweedFS S3 私有 bucket 保存模板工件；AWS SDK v2 Transfer Manager 负责分片上传，Node 校验 digest 后发布本地缓存，产品快照另按规格实现 |
| 功能开关 | OpenFeature + flagd | 当前未接入；需要灰度能力时接入，不将授权规则交给客户端开关 |
| 可观测性 | OpenTelemetry，包含 trace/metric/log | 当前只有 slog；后续补 HTTP 请求、OAuth 错误类别、数据库和 Node operation 链路，禁止记录凭证 |
| 本地基础设施 | Compose、Air、Vite HMR、Caddy | Compose 启动 PostgreSQL、Redis、Casdoor、SeaweedFS；Go API、Node Orchestrator 和 Web 在宿主机运行；开发通过 Vite proxy 同源，ngrok 暴露 Casdoor 回调 |
| 验证与质量 | Go tests、Testcontainers、Vitest、MSW、Biome、生成检查 | 当前使用隔离临时 PostgreSQL + race、Vite+ tests/typecheck、生成检查；尚未整体迁移模板 lint/MSW/Testcontainers 配置 |

优先顺序：先完成本次 OpenAPI 契约生成与真实 HTTP 密钥验证；Runtime 接入前冻结 PostgreSQL 持久 Sandbox 模型、Redis active registry/routing schema 和 Node gRPC，再实现 Node 与 Control Worker。River、S3 和 flagd 随实际模块需求接入；Redis 属于 Runtime 必需基础设施。

## API Key 验证边界

数据库只保存密钥摘要。用户关闭一次性明文弹窗后，无法从数据库还原该密钥进行测试。

自动集成测试使用独立临时数据库和真实本地 HTTP listener：通过测试身份创建真实 API Key，随后仅携带 `X-API-Key` 发起 Sandbox Create/List/Get/Patch/Delete，检查读权限不能写、跨租户返回 404、API Key 不能管理密钥、撤销和过期后返回 401，以及并发创建的数量上限。HTTP 路由、鉴权、摘要比较和 PostgreSQL 均是真实实现；测试 Bearer 身份为隔离桩，不将其描述为真实 GitHub 登录测试。

真实 GitHub 登录和页面创建已由用户验证；没有取回或打印用户的完整密钥。这组检查也不证明 Sandbox VM 已运行。
