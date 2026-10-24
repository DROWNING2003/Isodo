# T3 server、WebSocket 与认证复用研究

本笔记为源码静态研究，未启动服务或运行端到端认证；未读取 `.env`、`.t3` secrets 或运行时凭证值。`app/` 是本地 T3 参考 fork，不是当前 Isodo 交付源码，见[参赛要求:44](../docs/competition-requirements.md#L44)。本文遵守根目录与 `app/AGENTS.md`；所有“拟议”均未实现。

## 结论

**T3 已支持无需人工配对的认证。** 最小首版可以在 Guest 中使用现有 CLI 签发短 TTL bearer，由宿主 ingress 托管，再复用 T3 HTTP、`/ws`、Effect RPC 和 `wsTicket`。已有受信任 bootstrap seed 自动兑换机制；已有 T3 Connect 云身份链路。它们不意味着 Isodo Casdoor/API Key 可直接作为 Guest 凭证。

## 已实现：server 与 RPC/事件

认证服务组合 policy、pairing grants、session store、secret store，采用 Effect service/layer：[EnvironmentAuth.ts:595](../app/apps/server/src/auth/EnvironmentAuth.ts#L595)。

`GET /ws` 先 `authenticateWebSocketUpgrade`，再 `RpcServer.toHttpEffectWebsocket(WsRpcGroup)`，使用 `RpcSerialization.layerJson`；连接生命周期调用 markConnected/markDisconnected：[ws.ts:3063](../app/apps/server/src/ws.ts#L3063)、[ws.ts:3086](../app/apps/server/src/ws.ts#L3086)、[ws.ts:3127](../app/apps/server/src/ws.ts#L3127)。这是 Effect RPC，不宜称为通用 JSON-RPC 2.0。

RPC 的 effect/stream 包装按 `requiredScopeForRpcMethod` 授权：[ws.ts:637](../app/apps/server/src/ws.ts#L637)。shell/thread 订阅采用 snapshot、sequence replay 和 live buffer，需沿用客户端已有去重及重连逻辑：[ws.ts:1449](../app/apps/server/src/ws.ts#L1449)、[ws.ts:1622](../app/apps/server/src/ws.ts#L1622)。配置流先 snapshot 后 updates：[ws.ts:2947](../app/apps/server/src/ws.ts#L2947)；服务生命周期按 sequence 过滤：[ws.ts:2954](../app/apps/server/src/ws.ts#L2954)；访问管理流合并 pairing/session changes：[ws.ts:2976](../app/apps/server/src/ws.ts#L2976)。

## 已实现：配对、session、WebSocket 全链路

1. 配对 credential 使用随机 12 字符 token，并写入 repository；普通默认 TTL 5 分钟，dev startup 特例 24h；可携带 scopes/subject 与 DPoP thumbprint：[PairingGrantStore.ts:248](../app/apps/server/src/auth/PairingGrantStore.ts#L248)、[PairingGrantStore.ts:375](../app/apps/server/src/auth/PairingGrantStore.ts#L375)。启动 URL 使用 `/pair#token=...` fragment：[EnvironmentAuth.ts:954](../app/apps/server/src/auth/EnvironmentAuth.ts#L954)。
2. Cookie 路径：browserSession handler 消耗 bootstrap，签发 browser-session-cookie 并设置 cookie：[http.ts:267](../app/apps/server/src/auth/http.ts#L267)、[EnvironmentAuth.ts:697](../app/apps/server/src/auth/EnvironmentAuth.ts#L697)。
3. Bearer/DPoP 路径：token handler 接收 `subject_token`，校验可选 DPoP proof：[http.ts:295](../app/apps/server/src/auth/http.ts#L295)。请求 scope 必须是 grant 的子集；DPoP session 绑定 jkt 且 TTL 1 小时：[EnvironmentAuth.ts:733](../app/apps/server/src/auth/EnvironmentAuth.ts#L733)。
4. HTTP credential 优先级为当前 cookie、Bearer、DPoP、legacy cookie；DPoP token 必须同时有匹配 proof：[EnvironmentAuth.ts:566](../app/apps/server/src/auth/EnvironmentAuth.ts#L566)、[EnvironmentAuth.ts:630](../app/apps/server/src/auth/EnvironmentAuth.ts#L630)。
5. session 校验签名、expiry、数据库记录与 revokedAt：[SessionStore.ts:718](../app/apps/server/src/auth/SessionStore.ts#L718)。已认证 principal 可 POST websocket-ticket；upgrade 从 `wsTicket` 查询参数还原 session：[http.ts:369](../app/apps/server/src/auth/http.ts#L369)、[EnvironmentAuth.ts:984](../app/apps/server/src/auth/EnvironmentAuth.ts#L984)。
6. **wsTicket 不是一次性票据。** 默认 TTL 5 分钟，签名 payload 含 sid/iat/exp；verify 只检查 ticket/session expiry、存在及撤销，没有 consume：[SessionStore.ts:418](../app/apps/server/src/auth/SessionStore.ts#L418)、[SessionStore.ts:780](../app/apps/server/src/auth/SessionStore.ts#L780)、[SessionStore.ts:816](../app/apps/server/src/auth/SessionStore.ts#L816)。一次性 pairing bootstrap 与 wsTicket 必须区分。

## 已实现：无需人工配对的入口

**直接签发 bearer。** CLI `auth session issue` 已有 `--ttl`、`--json`、`--token-only`，直接调用 issueSession：[cli/auth.ts:53](../app/apps/server/src/cli/auth.ts#L53)、[cli/auth.ts:162](../app/apps/server/src/cli/auth.ts#L162)。注意 CLI 当前默认 `AuthAdministrativeScopes`，不是窄权限签发。底层接口已接受 scopes、subject、ttl，是后续收窄切点：[EnvironmentAuth.ts:862](../app/apps/server/src/auth/EnvironmentAuth.ts#L862)。客户端已能校验 descriptor environmentId、用 bearer 换 ticket 并为 HTTP 保留 Bearer：[authorization/service.ts:145](../app/packages/client-runtime/src/authorization/service.ts#L145)、[authorization/service.ts:172](../app/packages/client-runtime/src/authorization/service.ts#L172)。

**宿主注入 bootstrap 自动兑换。** server 经 `bootstrapFd` 读取 `DesktopBackendBootstrap` envelope：[cli/config.ts:229](../app/apps/server/src/cli/config.ts#L229)。读取器读取 fd 上一行 JSON，默认 1 秒超时：[bootstrap.ts:74](../app/apps/server/src/bootstrap.ts#L74)。预设 seed 注册为 desktop-bootstrap、admin scopes、24h、unbounded uses：[PairingGrantStore.ts:312](../app/apps/server/src/auth/PairingGrantStore.ts#L312)。这不是直接 bearer，仍需兑换；desktop bearer 兑换替换旧同 subject/method session：[EnvironmentAuth.ts:754](../app/apps/server/src/auth/EnvironmentAuth.ts#L754)。Web 已从 desktopBridge 取 seed，在无 session 时自动调用 browser-session 建 cookie：[primary/auth.ts:168](../app/apps/web/src/environments/primary/auth.ts#L168)、[primary/auth.ts:221](../app/apps/web/src/environments/primary/auth.ts#L221)、[primary/auth.ts:310](../app/apps/web/src/environments/primary/auth.ts#L310)。这证明自动兑换机制已有；用于 Isodo Guest 尚需适配。

**E2B `envdAccessToken` 不是 T3 WebSocket 凭证。** E2B 创建沙箱的响应会带 `envdAccessToken`；官方 JS SDK 将它作为 `X-Access-Token` 加到 envd RPC/HTTP 请求，用于文件、进程等沙箱管理操作：[E2B Sandbox SDK](https://github.com/e2b-dev/E2B/blob/main/packages/js-sdk/src/sandbox/index.ts)、[E2B Sandbox API schema](https://github.com/e2b-dev/runtime/blob/main/spec/openapi.yml)。T3 `/ws` 认证走 T3 自己的 session/bearer/DPoP 或 `wsTicket`，不会把 E2B token 当成有效 T3 session。T3 当前 E2B 启动器也把两个职责分开：envd token 用于上传/启动；服务启动后在 Guest 中调用 T3 的 pairing CLI 生成一次性配对 URL：[sandbox-client/t3-orchestrator.ts](../app/packages/sandbox-client/src/t3-orchestrator.ts)。因此 Isodo 应保留 envd token 在 Node 控制面；若要免手工配对，应由可信宿主为 T3 签发窄权限 bearer，或者签发一次性 T3 bootstrap，再按客户端类型换成浏览器 cookie 或 WebSocket ticket。不要把 envd token直接转交前端或复用为 T3 scope。

**云身份。** 客户端读取 cloudSession Clerk token，调用 relay.connectEnvironment 并检查返回 environmentId：[authorization/service.ts:251](../app/packages/client-runtime/src/authorization/service.ts#L251)。Guest mint handler 验 relay 签名、issuer、环境 audience、environmentId、linked cloud user、DPoP jkt、有限 lifetime、environment:connect scope，再消费 jti/nonce 防重放，生成 2 分钟 proof-bound bootstrap：[cloud/http.ts:945](../app/apps/server/src/cloud/http.ts#L945)、[cloud/http.ts:973](../app/apps/server/src/cloud/http.ts#L973)、[cloud/http.ts:999](../app/apps/server/src/cloud/http.ts#L999)、[cloud/http.ts:1012](../app/apps/server/src/cloud/http.ts#L1012)。之后复用标准 token exchange。它依赖 T3 专属 relay/密钥安装/linked-user 信任，不是任意 JWT adapter；直接复用完整云链路比代理托管 bearer 更重，是本次研究判断。

## 当前 Isodo：主 agent 独立核实的事实

以下由主 agent 读取当前交付源码提供，笔记作者未重复读取这些文件：

- cookie/Bearer/X-API-Key 互斥认证：[credentials.go:42](../internal/control/auth/credentials.go#L42)。scopes 仅 sandboxes/read/write、templates/read/write，不能当 T3 exec 授权：[credentials.go:104](../internal/control/auth/credentials.go#L104)。
- session cookie 为 HttpOnly、Secure、SameSiteLax：[web.go:80](../internal/control/auth/web.go#L80)。GetSandbox 按 tenant 查询：[http.go:122](../internal/control/sandbox/http.go#L122)。网络安全仍属草案：[network-security.md:3](network-security.md#L3)。
- 在 `internal/**/*.go` 对 websocket/gateway/proxy/upgrade 的检索未定位 ingress 实现；不能据此断言整个仓库绝对不存在。
- 客户端 `endpoint.ts:3–8` 通过 `url.pathname = pathname` 覆盖 base path，因此同源 sandbox path 前缀方案需要 endpoint adapter，不能只改 baseUrl（主 agent 核实，合并时补精确链接）。

## 拟议：最小集成切点

1. Isodo 同源 ingress 复用平台 session，每次 HTTP/WS 连接校验 tenant、sandbox 归属与状态。平台 API Key 仅用于平台 API，不直接充当 Guest 执行权限。
2. 宿主通过私有通道至 T3。首版 Node 使用已有 CLI issue 短 TTL bearer，由宿主托管；明确现有 CLI 默认 admin，后续在 issueSession 添加窄 scopes 签发接口。保留 T3 HTTP/Effect RPC 和 wsTicket。
3. 平台身份/资源授权、Guest 机器身份（mTLS/vsock 等，具体待定）、外部服务凭证代理分别建模。不要把 Casdoor access token 或 Isodo API Key 传 Guest。
4. 重连重新校验；停止、撤销或替换 runtime 时关闭关联 active WS，不能依赖 ticket 过期。现有校验主要发生在 upgrade，active socket 是否立即被关闭未端到端验证。
5. 同源 path 前缀需 endpoint adapter；cookie Path、signed asset URL、路由重写、DPoP canonical URL 都是集成待验项。本文路径方案是示意，不是现有 ingress 路由。
6. 可选直连短期 sandbox/runtime scoped ticket 需新增 T3 验证 adapter，绑定 issuer/aud/资源/scopes 并考虑重放；尚非现有实现。任何 30–60 秒建议是新设计，不是已有 5 分钟 wsTicket 默认值。

## 未知

CLI 与运行服务共享数据库时的部署方式、并发轮换、代理路径/CORS/DPoP URL、active socket 撤销、跨端完整重连均待验证。未读取凭证存储，不能推断实际云链接/有效 token 已配置。此次无代码修改、无提交、无运行时认证测试。
