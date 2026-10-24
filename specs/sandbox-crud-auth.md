# Sandbox CRUD 与 Casdoor 鉴权规格 R-01

状态：实现中（`feat/sandbox-crud-auth`）。本规格限定 R-01：Casdoor 身份认证、Casdoor 身份到 Isodo tenant 的映射、用户自助 API key 管理，以及 Sandbox 元数据 CRUD 的租户隔离。它不代表 Cloud Hypervisor VM 已创建或运行。

## 1. 目标与边界

- 所有 Sandbox 查询和变更必须限制在认证主体所属 tenant；跨 tenant 的 ID 猜测不能读取、修改或删除资源。
- 身份认证由 Casdoor 提供。客户端在 Casdoor 完成 OAuth 2.0/OIDC 授权码流程，并向 Isodo API 发送 `Authorization: Bearer <Casdoor access token>`。
- 此 Isodo Casdoor Application 的唯一用户登录入口是 GitHub OAuth；Casdoor 本地密码/验证码/WebAuthn 等登录方法，以及 Google 等其他 OAuth Provider 均关闭。
- Isodo API 通过固定的 OIDC discovery 得到 UserInfo endpoint，并将 bearer token 发给该 endpoint 验证；不得从请求参数接受 issuer、UserInfo URL、tenant ID 或身份声明。
- 本地身份键为配置 issuer 与 Casdoor `sub`。email、用户名、显示名和组织字符串均不用于身份唯一性或租户授权。
- 首次认证时自动创建个人 tenant；首版一个 Casdoor `(iss, sub)` 对应一个 Isodo tenant，不支持组织成员/邀请。
- 用户先用 GitHub OAuth 登录官网，再由官网以该 Casdoor 用户身份创建 API key；Sandbox API 可用 Casdoor access token 或 API key 调用，两者最终映射到同一 tenant。
- Create 只登记控制面 Sandbox 资源记录，状态为 `requested`；Node 调度、真实 VM 创建、配额、幂等 operation、Start/Stop 和 Delete 清理由后续生命周期实现负责。
- 本文定义的是 R-01 先行交付契约；接入 T4.1 Node operation 后，`POST /v1/sandboxes` 将按生命周期规格调整为异步 `202 + operation_id`，不得把此阶段的 `201 requested` 当成最终 VM 创建语义。

## 2. Casdoor 接入

### 2.1 配置

| 环境变量 | 必填 | 说明 |
| --- | --- | --- |
| `ISODO_CASDOOR_ISSUER` | 是 | OIDC issuer，例如 `https://auth.example.com`；启动时读取 `/.well-known/openid-configuration` 并精确匹配 discovery 的 `issuer` |
| `ISODO_CASDOOR_CLIENT_ID` | 是 | 此 Isodo API client 的 audience |
| `ISODO_DATABASE_URL` | 是 | PostgreSQL DSN；不得提交真实凭证 |
| `ISODO_LISTEN_ADDR` | 否 | 默认 `127.0.0.1:8080`；正式部署可绑定服务网段，但必须由受信 TLS ingress 终止 HTTPS 后转发 |

数据库先应用 `migrations/0001_control_api.sql`，然后设置以上环境变量运行 `go run ./cmd/isodo-api`。Isodo API 本身当前只提供 HTTP；默认仅监听 loopback。公网部署必须置于受信 TLS ingress 后，不能直接暴露明文 HTTP。Casdoor issuer 与 UserInfo endpoint 正式环境必须 HTTPS。

启动时验证 issuer URL 为 HTTPS（本机测试可使用 loopback HTTP），发现文档有效且含 `userinfo_endpoint`。Discovery 文档与 UserInfo 响应大小均有限制；HTTP 请求设置超时。UserInfo endpoint 只能来自可信 issuer 的 discovery，不能使用客户端提供的 URL。

### 2.2 Bearer 请求认证

1. 只接受单一 `Bearer` Authorization header；缺失/格式错误返回 `401 UNAUTHENTICATED`。
2. 通过 TLS 向 discovery 配置的 UserInfo endpoint 发送 Bearer token；不把 token 放入 URL、日志、错误正文或数据库。
3. UserInfo 的 `sub` 必须非空，`iss` 必须等于配置 issuer，`aud` 必须包含配置的 Casdoor client ID。任一不满足按无效凭证处理。
4. UserInfo 401/403 映射为 401；Casdoor 网络超时、5xx 或响应不符合协议时返回 `503 IDENTITY_PROVIDER_UNAVAILABLE`，不把上游错误体回传客户端。
5. 以 `(issuer, subject)` 原子解析/创建本地 identity 与个人 tenant。数据库故障返回 503，不降级为匿名或使用用户提交的 tenant。
6. 每个请求都经 UserInfo 验证，不在首版缓存 access token 身份，以免自行延长撤销窗口。后续若加缓存，必须单独定义 TTL、撤销延迟和密钥轮换语义。

Bearer token 由客户端取得和刷新；Isodo 不代理 Casdoor 登录页、不保存 Casdoor 密码或 refresh token。Casdoor SDK 可用于客户端登录，服务端只依赖标准 OIDC discovery/UserInfo 协议，不使用 `keelctl new` 覆盖 Isodo 项目结构。

Casdoor 应用只启用需要的授权模式；Web 客户端使用精确登记的 redirect URI、authorization code、`state`、OIDC `nonce` 和 PKCE，禁止把 client secret 放进浏览器。Implicit/password grant 不作为此登录流程。CLI/device-code 客户端后续单独定义。

### 2.3 GitHub-only 登录配置

- Casdoor 中为 Isodo Application 只关联一个可登录 Provider：GitHub；该 Provider 同时允许 sign-in 和 sign-up，以匹配首次认证时自动创建 Isodo identity/个人 tenant 的行为。是否允许用户进入 Isodo 仍由 Casdoor 的 GitHub Provider 与 Application 注册策略共同控制。
- 关闭此 Application 的 Casdoor 本地登录方法（例如 Password、Verification code、WebAuthn）以及其他 OAuth Providers。新增登录方式属于认证策略变更，需同步更新本规格与安全验收。
- 使用 GitHub App 配置 Casdoor 的 GitHub Provider。GitHub App 的 callback URL 指向 Casdoor 的 `/callback`；Casdoor Application 登记的 redirect URL 则指向 Isodo 客户端的 OIDC callback。两个 callback 属于不同跳转阶段，不能互换。
- GitHub OAuth Client ID/Secret 只保存在 Casdoor Provider 配置中；Isodo 不保存 GitHub 凭证，也不直接向 GitHub 换取用户 token。Isodo 只接受配置 issuer 与 client ID 对应的 Casdoor access token。
- Isodo API 的认证身份仍为 `(Casdoor issuer, sub)`，不以 GitHub username、email 或 GitHub ID 拼接本地身份。GitHub-only 限制由 Casdoor Application 的 Provider 配置保证；未经审核不得在该 Application 上增加 Provider 或本地登录方法。

### 2.4 官网 API key

- 官网用户先经 Casdoor GitHub OAuth 登录。官网通过服务端授权码流程建立 HttpOnly 会话后调用 API key 管理接口，写操作要求 Origin + CSRF 校验；兼容有效 Casdoor access token。API key 不能创建、列出或撤销 API key。具体登录流程见 [网页登录与 API Key](web-auth-api-keys.md)。
- API key 是同一 tenant 的程序化调用凭证，不是新的用户身份。key 由已认证的 `(issuer, sub)` 解析出 tenant 后签发，不接受客户端指定 tenant。
- key 管理接口只接受官网会话或 Casdoor Bearer token；Sandbox 用户 API 接受官网会话、Casdoor Bearer token 或 `X-API-Key`。一次请求必须且只能提供一种凭证；同时提供或重复提供凭证时拒绝，不能按优先级回退。
- API key 使用高熵随机秘密，格式为 `iso_<key-id>_<secret>`；`key-id` 用于定位记录，`secret` 至少 32 个随机字节并以无填充 Base64URL 编码。创建成功时明文仅返回一次；数据库只存 SHA-256(secret) 摘要，不存明文。key 的显示名/前缀可供页面识别，不可用于认证。
- 初版 key scope 为 `sandboxes:read`（List/Get）和 `sandboxes:write`（Create/Patch/Delete）。创建时必须显式选择 scope；授权上下文由 key 记录中的 tenant 与 scope 生成。超出 scope 返回 `403 INSUFFICIENT_SCOPE`。
- key 可设到期时间，并支持随时撤销。初版每个 tenant 最多 10 个未撤销 key；轮换先创建新 key、迁移调用方，再撤销旧 key。过期/撤销 key 立即失效。
- API key 只通过 TLS 传输；服务端、反向代理、APM、审计和错误日志必须对 `X-API-Key` 脱敏。服务端不得回显 key 或把它放进 URL/query/body。
- `X-API-Key` 的请求不访问 Casdoor；仅做本地摘要验证、有效期/撤销检查和 tenant/scope 解析。因此 Casdoor 暂时不可用不影响已经签发的有效 API key，已有网站会话仍可管理 key；新登录和 Bearer 鉴权需要 Casdoor。

## 3. 租户数据模型与授权规则

- `user_identities` 以 `(issuer, subject)` 唯一；每个身份映射到一个 tenant。
- 新增 `api_keys`：`id`、`tenant_id`、`name`、`secret_hash`、`prefix`、`scopes`、`created_at`、`expires_at`、`last_used_at`、`revoked_at`。`secret_hash` 保存固定长度摘要，不存 key 明文；通过 `tenant_id` 与 active/revoked 状态索引列表和校验。
- key 创建时从当前 OAuth 身份查询 tenant；请求中的 tenant 字段一律忽略/拒绝。每次 API key 鉴权按 public `key-id` 定位记录，验证摘要、有效期、撤销状态，并从记录导出 tenant 与 scopes。
- API key 使用记录只能更新非秘密元数据（如 `last_used_at`）；不得记录 secret 或原始认证头。
- `sandboxes.tenant_id` 是资源所有权的唯一授权依据，由服务端认证上下文写入。
- HTTP body、query、path、header 中出现的 `tenant_id` 一律不参与授权；未知字段拒绝。
- 所有 SQL 资源访问都在数据库语句中同时限制 `id` 与 `tenant_id`，列表还必须以 `tenant_id` 为固定过滤条件。
- GET/PATCH/DELETE 的不存在 ID 与其他 tenant 的 ID 均返回相同 `404 SANDBOX_NOT_FOUND`，不得先全局查询 ID 再比较 owner。
- 不允许管理员/维护接口绕过此策略；Node/服务身份属于单独信任边界，不在此 API 范围内。

## 4. HTTP API

除 `GET /healthz` 外，所有路由均须认证。用户路由可接受官网会话、Casdoor Bearer 或 `X-API-Key`，凭证类型和权限见 2.4；API key 管理路由接受官网会话（写入需要 Origin + CSRF）或 Casdoor Bearer。JSON request body 上限 64 KiB，拒绝未知字段和尾随 JSON 文档。

### `POST /v1/sandboxes` — 201

```json
{"name":"dev","metadata":{"purpose":"development"}}
```

服务端生成 ID 和 tenant。响应资源 `desired_state=stopped`、`observed_state=requested`，不表示 VM 已分配或已启动。`name` 必填，去除首尾空白后 1–128 字节；metadata 为可选 JSON object，默认 `{}`。

### `GET /v1/sandboxes?limit=50&offset=0` — 200

只列出当前 tenant。默认 limit 50、最大 100；offset 必须为非负整数。排序稳定为 `created_at DESC, id DESC`。响应包含 `items`、`limit`、`offset`、`total`。

### `GET /v1/sandboxes/{sandboxID}` — 200 / 404

仅返回当前 tenant 的记录。其他 tenant 的 ID 返回与不存在资源相同的 404。

### `PATCH /v1/sandboxes/{sandboxID}` — 200 / 404

允许局部更新 `name` 和/或整个 `metadata` 对象；至少提供一个字段。Sandbox ID、tenant、模板、资源规格、网络策略和状态不可在此路由修改。SQL 更新必须带 `id`、`tenant_id` 和 `deleted_at IS NULL` 条件。

### `DELETE /v1/sandboxes/{sandboxID}` — 204 / 404

仅软删除当前 tenant 的记录；之后 Get/List 隐藏该记录。重复删除返回 404。此 R-01 元数据删除尚未触发 Node 资源清理；完整异步 Delete 由生命周期模块定义。

### API key 管理

以下接口使用官网 OAuth 登录后建立的服务端会话，或有效 Casdoor access token；`X-API-Key` 不得访问这些路由。

#### `POST /v1/api-keys` — 201

请求包含 `name`、非空 `scopes`、可选 `expires_at`。服务端从 OAuth 身份解析 tenant，生成 key，并在响应中仅一次返回完整明文：

```json
{"id":"key_...","name":"ci","scopes":["sandboxes:read","sandboxes:write"],"api_key":"iso_<key-id>_<secret>","expires_at":"2027-01-24T00:00:00Z"}
```

官网必须提示用户立即复制保存；后续列表、读取都不返回 `api_key` 或 secret。

#### `GET /v1/api-keys` — 200

只返回当前 tenant 的 key 元数据：ID、名称、不可用于认证的前缀、scope、创建/到期/撤销/最近使用时间。已撤销 key 可展示为历史记录。

#### `DELETE /v1/api-keys/{keyID}` — 204 / 404

撤销当前 tenant 的 key。其他 tenant 的 ID 与不存在 ID 一样返回 404；撤销后后续请求立即拒绝。接口不提供读取完整 key、恢复撤销 key 或更新 scope；需变更权限时创建新 key 再撤销旧 key。

Sandbox API 调用示例：

```sh
curl https://api.example.com/v1/sandboxes \\
  -H 'X-API-Key: iso_<key-id>_<secret>'
```

## 5. 数据库约束

- PostgreSQL 13+；`tenants`、`user_identities`、`sandboxes` 使用 UUID 主键；`sandboxes.tenant_id` 外键引用 tenants。
- 通过 tenant 和创建时间索引支持分页；所有软删除资源查询带 `deleted_at IS NULL`。
- Sandbox 记录保存 `name`、`metadata JSONB`、`desired_state`、`observed_state`、`created_at`、`updated_at`、`deleted_at`。
- Identity 首次创建需并发安全；同一 `(issuer, subject)` 的并发首次请求最终只能有一条 identity 和一个 tenant。

## 6. 验收

- 无 token、无效/过期 token：受保护路由返回 401；Casdoor 故障返回 503。
- 用户 A 创建 Sandbox 后，A 可列出、读取、修改、删除；用户 B 的 list 不含 A 的资源，且 Get/Patch/Delete A 的 ID 全部返回同样 404。
- 伪造 tenant ID、email、Casdoor `sub`、其他 header/body 字段不能更改授权上下文；未知字段拒绝。
- Casdoor Application 配置审查确认 GitHub 是唯一可 sign-in/sign-up Provider，且本地登录方法与其他 OAuth Provider 均关闭。
- 并发首次身份映射不产生重复 tenant；sandbox ID 随机且不被视为授权凭据。
- 测试必须覆盖 HTTP handler 与真实 PostgreSQL SQL 行为。没有真实 Node/CHV 时不得把本规格验收描述成 Sandbox VM 生命周期完成。

验证命令：`go test -race ./...`；PostgreSQL 集成测试先将 `migrations/0001_control_api.sql` 应用到隔离测试库，再设置 `ISODO_TEST_DATABASE_URL` 并运行 `go test ./internal/control/postgres -run TestPostgresSandboxTenantIsolation -count=1`。

## 7. 参考与实现选择

- [Casdoor 接入概览](https://casdoor.ai/docs/how-to-connect/overview/)建议新应用使用 OAuth 2.0/OIDC；[OAuth 2.0 文档](https://casdoor.ai/docs/how-to-connect/oauth/)定义授权码、token 与 UserInfo endpoint。本服务按标准 discovery/UserInfo 接入。
- [Casdoor GitHub Provider 指南](https://casdoor.ai/docs/provider/oauth/github/)说明 GitHub App 的 callback 配置；[Casdoor 登录方式](https://casdoor.ai/docs/application/signin-methods/)与 [Application 配置](https://casdoor.ai/docs/application/config/)分别说明登录方法、Provider 与客户端 redirect URL 的配置边界。
- `keelctl` 的 Casdoor scaffold 可用于了解 Go 应用的模块装配和配置方式，但它生成完整 Go/React/Compose 应用，和 Isodo 现有模块边界冲突；Isodo 只实现此处所需协议与控制面逻辑。
- `specs/network-security.md` 中原先的“管理员发放 Bearer API key、OAuth 延后”已被本规格替换；API key 的签发与认证按本文 2.4 和 [网页登录与 API Key](web-auth-api-keys.md) 实现。
