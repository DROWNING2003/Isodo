# 网页登录与 API Key

状态：已实现服务端授权码登录、持久会话和 API Key；真实 GitHub 登录与页面创建已由用户在本机验证。隔离 PostgreSQL 集成测试覆盖密钥权限与撤销。运行说明见下文。

## 身份与边界

- GitHub Client ID / Secret 只配置到 Casdoor GitHub Provider。Isodo 后端持有独立的 Casdoor Application Client ID / Secret，向 Casdoor 兑换授权码；前端不持有这两类 Secret。
- Casdoor Application 归属 `isodo` 组织。只允许 GitHub sign-in/sign-up；Password、Verification code、WebAuthn、Face ID 的 rule 均为 None。`built-in` 管理台的管理员登录配置独立保留。
- Isodo 以经签名验证的 `(issuer, sub)` 映射个人 tenant，不以显示名或邮箱授权。
- 网页使用服务端会话；程序化调用使用 `X-API-Key`。Casdoor Bearer token 的既有 API 用法保留。API Key 不能管理其他 API Key。

## 登录流程

1. 浏览器访问 `GET /auth/login`。后端生成随机 state、nonce 和 S256 PKCE verifier；state 只以 SHA-256 摘要存入 `oauth_states`，nonce/verifier 保留 10 分钟以完成授权码交换。
2. 浏览器取得 host-only、HttpOnly、SameSite=Lax 的 state Cookie，跳转 Casdoor 授权端点。`provider_hint=isodo-github` 让 Casdoor 直接跳转 GitHub，不再显示第二个登录选择页。
3. GitHub 回调到 `${CASDOOR_ORIGIN}/callback`；Casdoor 完成 GitHub 授权码交换，再回调 `${ISODO_WEB_ORIGIN}/auth/callback`。
4. Isodo 检查 URL state 与 Cookie 绑定，原子消费一次性 state；携带自己的 Application Secret 和 PKCE verifier 向 Casdoor 换取 token。
5. 使用 go-oidc 验证 ID token 的签名、issuer、audience、有效期，并比较 nonce。认证失败不创建会话，不向浏览器回显上游 token 或错误细节。
6. 创建随机会话，数据库仅保存 token 摘要、tenant、CSRF token 和到期时间。HttpOnly Cookie 的有效期取 12 小时与 ID token 剩余有效期的较短值，页面返回 `/sandboxes`。

所有外部端点要求 HTTPS；开发时只对 loopback URL 允许 HTTP。Cookie 的 Secure 属性由明确配置的 `ISODO_WEB_ORIGIN` 决定，不信任请求中的转发头。上线 HTTPS 时 Cookie 自动带 Secure。本机 Cookie 绑定 `127.0.0.1`，请统一使用配置的网页 origin。

`GET /auth/me` 返回账号显示名、CSRF token 和本地会话到期时间。`POST /auth/logout` 要求有效会话、匹配的 Origin 和 `X-CSRF-Token`，立即删除本地会话并清除 Cookie。退出 Isodo 不撤销 GitHub/Casdoor 的单点登录会话，也不撤销 API Key。Casdoor 侧禁用账号不会主动推送到现有本地会话；本地会话在退出或上述到期时失效。

登录入口按直连客户端 IP 限制每分钟 20 次。反向代理部署应评估共享直连 IP 的限流影响；本地 Vite 代理不信任任意客户端提供的 IP 头。

## API Key

网页创建弹窗只填写名称和到期时间，默认签发沙箱读写权限；服务端继续支持受限 scope。创建成功后在独立弹窗显示一次完整密钥并提供复制按钮，关闭即从页面状态清除；列表仅显示脱敏前缀。列表滚动时表头固定。

- `POST /v1/api-keys`：接受 `name`（去首尾空白后 1–64 字节）、非空且不重复的 `scopes`，以及可选的未来 `expires_at`。当前支持 `sandboxes:read` 和 `sandboxes:write`。
- key 格式为 `iso_<32 位十六进制 ID>_<32 字节随机秘密的 Base64URL>`。成功创建时仅一次返回 `api_key`，浏览器只在当前页面内存中展示，离开页面即丢弃；不写 localStorage。
- `api_keys` 仅保存 SHA-256(secret)、公开前缀、tenant、scope、创建/到期/最近使用/撤销时间。列表从不返回 secret 或摘要。
- 每个 tenant 最多 10 个未撤销 key，过期但未撤销的记录也占用数量。数据库事务按 tenant 加 advisory lock，防止并发超额。
- `GET /v1/api-keys` 只返回当前 tenant 的元数据；`DELETE /v1/api-keys/{id}` 撤销自己的 key。其他 tenant 和不存在的 ID 均返回 404；撤销重复执行幂等。
- key 管理接受网站会话（写操作必须 Origin + CSRF）或 Casdoor Bearer。携带 `X-API-Key` 的管理请求一律拒绝。
- Sandbox API 接受网站会话、Casdoor Bearer 或 `X-API-Key`。重复或混用凭证拒绝认证；不能降级回退到另一种凭证。
- API Key 的 List/Get/HEAD 要求 read；Create/Patch/Delete 要求 write。scope 不足返回 403；伪造、过期、撤销返回 401。
- 验证 key 时先恒定时间比较摘要，再通过带有效期和撤销条件的 SQL UPDATE 确认并更新 last_used_at，防止查询后发生撤销仍被授权。
- 已运行 API 处理 API Key 时不调用 Casdoor。启动时仍需 Casdoor discovery 成功。

## 本地运行

1. `make init` 生成被忽略的 `.env`。在其中填写 `GITHUB_CLIENT_ID` 和 `GITHUB_CLIENT_SECRET`。
2. `make infra-up` 渲染被忽略且权限为 0600 的 Casdoor runtime 配置，并启动 PostgreSQL/Casdoor。Application Secret 缺失时自动生成；不要把 GitHub Secret 填到 Application Secret 字段。
3. 本地直接访问 Casdoor 时 `CASDOOR_ORIGIN=http://localhost:8000`。使用 ngrok 时先运行 `ngrok http 8000`，将 `CASDOOR_ORIGIN` 改为实际 HTTPS 隧道地址，重启 Casdoor/API；GitHub App Callback URL 填 `${CASDOOR_ORIGIN}/callback`。
4. `ISODO_WEB_ORIGIN=http://127.0.0.1:5734`；Casdoor 的 Isodo Application Redirect URL 由脚本配置为此 origin 的 `/auth/callback`。
5. `make api` 应用迁移并启动 API。另一个终端 `make web` 启动网页；Vite 将 `/auth` 与 `/v1` 代理到 loopback API，同源 Cookie 不需要开放跨域。
6. 浏览器访问 `http://127.0.0.1:5734/sandboxes`，登录后在“开发者 → API 密钥”创建密钥。

```sh
curl http://127.0.0.1:8080/v1/sandboxes \
  -H "X-API-Key: $ISODO_API_KEY"
```

loopback HTTP 示例仅用于本机测试，公网 API 使用 HTTPS。`make infra-down` 保留数据库卷。

## 验证证据与验收

- `make test-integration` 创建临时隔离数据库，应用所有迁移，执行 `go test -race ./... -count=1`，结束后删除该测试库。
- OAuth 测试使用临时 RSA 签名的 OIDC provider，实际校验授权码交换时的 Client Secret/PKCE、ID token 签名、nonce、audience、过期、state 绑定与防重放，以及会话 Cookie/CSRF/退出。
- PostgreSQL 测试覆盖真实 HTTP key 创建、仅一次明文、元数据列表、scope、跨租户 404、伪造/重复/混用凭证、过期、撤销、并发数量限制、会话删除和 OAuth state 一次消费。
- 前端测试验证调用真实接口契约、携带 CSRF、处理服务器错误、退出后的并发刷新不会恢复旧账号状态。
- 真实浏览器验证：Isodo 点击 GitHub 后直达 GitHub；用户完成真实登录并创建了 API Key。数据库记录验证了对应身份、有效会话和仅含 32 字节摘要的密钥。
- 这些验证不代表 Sandbox VM 生命周期或其他原型工作区功能已经实现；它们不在本规格范围内。

参考：[Casdoor OIDC](https://casdoor.ai/docs/how-to-connect/oidc-client/)、[provider hint](https://casdoor.ai/uk/docs/how-to-connect/oauth/)、[go-oidc](https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc)、[oauth2 PKCE](https://pkg.go.dev/golang.org/x/oauth2#S256ChallengeOption)。

HTTP 契约与生成流程见 [工程技术栈](engineering-stack.md)；控制面契约为 `api/openapi/openapi.yaml`，Sandbox 用户 API 契约为 `api/openapi/openapi-public.yaml`。密钥集成测试使用真实本地 HTTP listener 与临时 PostgreSQL，覆盖 Sandbox 元数据 CRUD；测试登录身份为隔离桩。
