# 登录、API Key 与第三方授权

Isodo 使用 Casdoor 管理网页登录会话，GitHub 可作为其身份提供方。GitHub MCP 使用独立 GitHub App 的 OAuth 授权；Notion 使用独立 MCP OAuth 流程，均不修改原登录配置。

公开 API 支持契约指定的会话或 API Key，API Key 通过 X-API-Key 发送。沙箱读取、管理、命令执行和端口预览使用独立 scope；sandboxes:write 不隐含 sandboxes:execute。错误 INSUFFICIENT_SCOPE 不能靠放宽服务端权限修复，应签发具备明确所需权限的凭证。

API Key 服务端保存摘要；账号级秘密使用加密存储。OAuth 保留 State、PKCE 和回调校验，敏感配置留在受保护服务器环境中。Guest、Renderer、命令行和 Skill 不应获得长期 Client Secret 或第三方 Token。

MCP 的收藏、账号连接与沙箱绑定分别管理。撤销应阻断 Broker 后续访问；已有 Agent 会话可能需要新建任务加载配置。会话写操作继续保留相应 CSRF 和所有权检查。

具体路由、认证方式和作用域以 OpenAPI 为准，使用说明见[开发者认证指南](../docs-site/content/docs/guides/authentication.mdx)。
