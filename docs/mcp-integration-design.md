# MCP 与凭证库接入

## 当前结果

已将侧栏名称统一为 Skill，并增加灰色禁用的 MCP 入口。凭证库现支持独立 MCP 类型的新增、编辑、删除，URL 与 Header 凭证按账号加密保存。沙箱安全连接和创建表单可选择授权，运行时转换为 HTTP injection。Agent 的 MCP 配置自动同步、OAuth 登录与刷新尚未实现。

`internal/node/injection/mcp_test.go` 使用真实本机 TLS 服务和真实 injection Proxy，客户端请求只携带测试占位符，验证 MCP initialize、initialized 通知、tools/list、tools/call、会话及协议版本头、SSE 工具结果。上游保持流开启，客户端必须在流结束前收到事件。删除规则后新请求得到 401。它是受保护的 MCP 协议 fixture，不是第三方服务，也没有在 Guest 或 Codex CLI 中执行。

## 产品流程与存储目标

1. 凭证库新增 MCP 配置：名称、传输方式、Endpoint、认证方式、凭证引用、适用 Agent。
2. API Key/Header 认证使用已有加密凭证；OAuth 显示“连接账户”，由沙箱外完成第三方授权。
3. 在沙箱的 MCP 页面选择账号中的配置并显式同步，与 Skill 的选择同步逻辑一致。
4. 为各 Agent 生成相应 MCP 配置片段，真实凭证留在沙箱外，Guest 只保存 URL 和不可充当上游密钥的占位符。
5. 撤销沙箱授权停用该绑定，不删除账号 MCP 配置；账号断开 OAuth 时撤销关联授权并清除令牌材料。

账号配置与沙箱绑定在服务端保存。MCP 配置建议单独建类型化记录，包含 transport、endpoint、authKind、credentialRef；OAuth grant 另存 issuer、resource、client registration、scope、expiresAt 和加密 token 材料。当前静态 Header MCP 使用 injection_rules 的 mcp 类型保存；不使用 credential_documents 扩展。OAuth grant 与多传输配置仍需独立模型。

## 认证与传输边界

| 接入方式 | 当前基础 | 后续工作 |
| --- | --- | --- |
| Remote HTTPS + API Key/Header | 已有真实 HTTP 注入链路，MCP fixture 验证通过 | Agent 配置合并与真实客户端验收 |
| Remote HTTPS + OAuth | injection 可消费有效 access token；没有 OAuth grant 生命周期 | 浏览器授权、发现、客户端注册、刷新、撤销、重新授权及 Node 取短期令牌接口 |
| stdio 无凭证工具 | 无远端 HTTP 请求需要注入 | 沙箱中安装/运行 MCP、适用 Agent 的 command/args 配置 |
| stdio 需要密钥 | HTTP injection 不替换进程环境变量或 stdin | 沙箱外 adapter，或单独变更安全承诺；当前不把 Key 放进 Guest |

Streamable HTTP 可能使用 JSON 或 SSE 响应，并在后续请求携带 MCP-Session-Id 与协议版本头，不能只测试 GET URL 返回 200。[MCP 传输规范](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)

## OAuth 方案

Isodo 的账号登录和第三方 MCP 授权是两份不同授权。建议由 Isodo 在沙箱外充当 MCP OAuth client：

- 根据 401 challenge/Protected Resource Metadata 发现授权服务器，校验 resource 与原始 Endpoint，防止把令牌交给其他服务。
- 客户端注册根据上游能力采用预注册、Client ID Metadata Document 或动态注册；不保证每家服务都支持自动注册。使用 authorization code + PKCE S256。
- state 绑定当前账号、MCP 草稿、issuer、resource，短期且一次性；回调严格匹配。第三方授权与 token 请求的 URL 需要专门的 SSRF、重定向及 DNS 校验，不能任意服务端抓取输入 URL。
- refresh token、client secret 与 PKCE verifier 保存在服务端受保护状态，不能进入 Guest。持久 token 使用现有加密基础设施，但 OAuth 字段和流程需新增实现。
- Node 在具备该沙箱授权的前提下向服务端解析短期 access token；代理在明确 Endpoint 与占位符条件命中时注入。现有规则存的是静态材料，尚无此动态解析接口。
- 到期前刷新，串行保护 refresh token rotation；授权失效时标记需要重连。不能盲目重放可能产生副作用的 tools/call；长连接与撤销时主动终止旧流也须另验。
- 使用目标 MCP 服务签发的令牌。禁止把 Isodo 会话 token 当上游凭证。规范所禁止的 token passthrough，是未验证令牌签发目标就接受并转交下游，不能用“已经做了 injection”替代该校验。

依据：[MCP 授权规范](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization)、[MCP 安全实践](https://modelcontextprotocol.io/docs/2025-11-25/tutorials/security/security_best_practices)。以上是方案，未宣称 OAuth 已实现。

## 验收顺序

先以一个真实 remote HTTPS MCP + API Key，从 Agent 完成发现工具与调用，再接一个确定支持的 OAuth 服务，验证授权、刷新、撤销和重新登录。服务凭证由用户在应用中填写；不在聊天、日志或测试产物保存真实 Key。当前用户没有指定第三方目标，因此本轮只用本地 fixture 验证代理协议链路。


验证命令：`go test ./internal/node/injection -count=1 -timeout=30s` 通过；侧栏 11 项测试及 Web 类型检查通过。测试中全部凭证都是专门生成的 fixture 值，未接触真实第三方授权。
