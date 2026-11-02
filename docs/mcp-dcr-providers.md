# Notion 官方 MCP

本轮在既有 MCP 市场、收藏、沙箱绑定和 Control Broker 链路上增加 Notion 官方 Provider；GitHub 继续使用预注册 Client，Casdoor 登录不变。

| Provider | 官方端点 | 限制 |
| --- | --- | --- |
| Notion | https://mcp.notion.com/mcp | 固定读取与搜索工具白名单 |

## 使用流程

收藏服务后进入详情连接账号。完成授权后，在沙箱安全连接中主动勾选并保存。收藏和连接不会自动给沙箱授权。Stop/Start 保留绑定，删除沙箱清理绑定，断开账号阻止后续调用。

当前沿用每个 Isodo 租户、每个 Provider 一个有效连接的模型，不提供同一 Provider 多外部账号同时连接。Notion workspace 信息仅来自可靠的 OAuth 响应；缺失时不推断。

## OAuth 与凭证边界

从官方 HTTPS 端点的 401 challenge、Protected Resource Metadata 和 Authorization Server/OIDC Metadata 发现配置，再使用 RFC7591 动态注册与 Authorization Code + PKCE S256。仅接受固定官方来源，校验 resource、issuer、回调 iss、state、有效期和单次消费；缺少 DCR 或不兼容配置明确失败。不使用 PAT 或 CIMD 回退。

注册信息按 Provider + issuer + redirect URI 缓存，过期或 invalid_client 后失效。Client Secret、Access Token、Refresh Token 使用既有 AES-GCM 密钥加密存储；PostgreSQL 行锁串行化刷新及 Token 轮换，断开连接和回调写入保留版本校验。生产密钥仍由受保护的服务器配置读取，不写入仓库。

Guest 和 Codex 配置只获得沙箱内 loopback Broker 地址与占位认证值；真正的上游凭证留在 Control。工具列表与调用分别执行白名单检查。网络连接保留公共地址检查、TLS 验证、固定来源及禁止重定向。

## 配置与上线准备

- 新增可选 ISODO_MCP_OAUTH_REDIRECT_URI；未设置时沿用 ISODO_GITHUB_OAUTH_REDIRECT_URI。两者同时设置必须一致。
- 当前正式回调路径为 /v1/mcp/oauth/callback，部署入口为 https://api.drowning.top:18443/v1/mcp/oauth/callback。
- Notion 无需手填伪造 Client ID；注册由 Control 执行。GitHub 原有配置不变。
- 上线需执行 0019_mcp_dcr.sql，并按 deploy/sql/grant-mcp-market.sql 更新专用角色的最小权限；随后发布匹配的 API、Node 和 Web。
- 本轮没有执行上述上线操作，也没有修改 DNS、现有 VM 或生产数据库。

## 验证

自动化与真实验收状态见 [验收记录](validation/mcp-dcr-20261103.md)。真实脚本 scripts/test-official-mcp-chv.py 需要专用授权资源，成功路径尚未真实运行。

脚本所需环境变量：ISODO_MCP_E2E_API、ISODO_MCP_E2E_HEADERS_FILE、ISODO_MCP_E2E_MODEL_RULES_FILE、ISODO_MCP_E2E_MODEL_BASE_URL、ISODO_MCP_E2E_MODEL、ISODO_MCP_E2E_NOTION_PAGE、ISODO_MCP_E2E_TEST_RESOURCES_APPROVED=1；可用 ISODO_MCP_E2E_REPORT 指定报告路径。缺少条件时输出 BLOCKED，不创建资源。

Headers 文件使用已登录账号的认证头 JSON；模型规则文件使用既有 injections API 的对象格式（顶层 rules 数组，且只含一个测试模型规则）。两者须权限 0600，仅在本机或受保护服务器保存；禁止把真实凭证放入聊天或日志。脚本创建和删除自己的测试沙箱，不操作已有 VM。

官方资料：[Notion 客户端接入](https://developers.notion.com/guides/mcp/build-mcp-client)、[MCP 授权规范](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization)。

已执行的0019迁移保持原校验和；0020撤下 Supabase，删除该 Provider 的收藏、授权、事务、注册及绑定。旧 project_ref 列保留用于数据库兼容，不再用于公开接入。回滚0020只恢复目录，不恢复已撤销凭证。
