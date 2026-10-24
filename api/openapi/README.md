# HTTP API 契约

修改对应 OpenAPI 文件后，在仓库根目录运行 `make generate`。它会更新 Go/TypeScript 绑定和 Fumadocs API 页面。不要手动修改生成文件；运行 `make generate-check` 检查生成结果是否与契约一致。

| 文件 | 边界 | 生成位置 |
| --- | --- | --- |
| `openapi.yaml` | 控制面 API：网页登录、会话、API Key 管理、健康检查和后续内部管理接口 | Go: `internal/api/admin/`；Web: `app/apps/web/src/api/admin.generated.ts` |
| `openapi-public.yaml` | 面向用户和 SDK 的公开 API：Sandbox CRUD。调用仍需会话、Casdoor Bearer Token 或有对应权限的 API Key | Go: `internal/api/public/`；Web: `app/apps/web/src/api/public.generated.ts` |

公开指 API 契约面向用户或 SDK，不表示匿名访问。控制面契约不作为公开 SDK 的接口承诺。所有业务校验、租户隔离与鉴权仍由对应 handler 和中间件执行，生成代码只提供路由与类型绑定。

OpenAPI 代码生成配置位于 `../../tools/openapi/`；Fumadocs 站点配置位于 `../../docs-site/`。公开 API Reference 从 `openapi-public.yaml` 生成到 `../../docs-site/content/docs/api-reference/endpoints/`。控制面契约不进入用户文档站。前端共享传输策略由 `../../app/apps/web/src/api/transport.ts` 实现，接口路径、请求和响应类型来自生成产物。

根目录 `make generate` 是所有代码生成器的统一入口。后续新增 protobuf/gRPC 生成时，增加 `generate-proto` 子目标并纳入该聚合目标；不要让生成步骤散落在进程入口或人工操作中。
