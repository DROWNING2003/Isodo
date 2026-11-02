# GitHub MCP 市场、OAuth 与沙箱绑定

## 实现与验证边界

分支 `drowning/github-mcp-market`。第一版只内置 GitHub 官方 Streamable HTTP，只读端点为 `https://api.githubcopilot.com/mcp/readonly`。不使用 DCR，不改变 Casdoor 登录，不使用已有 PAT 验收。当前 GitHub App 已注册，但尚无服务器 Client Secret，也未安装到仓库、部署新 Callback 或执行真实 GitHub + Codex + CHV E2E，**不能标记 GitHub MCP 已接入**。

市场、收藏、OAuth 授权和 SandboxBinding 是四类记录。账号当前由唯一 `tenant_id` 对应一个 `user_identity`，所有账户查询均使用认证上下文的 tenant，不接受请求指定 tenant。取消收藏不撤销 OAuth；断开 OAuth 不删除收藏；删除沙箱只清理绑定。账号操作拒绝普通 Sandbox API Key。

## 已注册的 GitHub App

- 设置页：https://github.com/settings/apps/isodo-mcp-drowning2003
- 公共页：https://github.com/apps/isodo-mcp-drowning2003
- 名称：`Isodo MCP DROWNING2003`
- App ID：`5576530`
- Client ID：`Iv23li6a6bUavtOTqZnJ`（公开客户端标识，不是 Secret）
- Homepage：`https://web.drowning.top:18443/`
- Callback：`https://api.drowning.top:18443/v1/mcp/oauth/callback`
- Callback wildcard：关闭
- Expire user authorization tokens：开启
- Request OAuth during installation：关闭，由 Isodo 市场发起
- Device Flow：关闭
- Setup URL：空，Redirect on update：关闭
- Webhook Active：关闭；Webhook URL / Secret：空
- Repository permissions：Contents、Issues、Pull requests 只读，Metadata 必需只读
- Organization、Account、Enterprise permissions：No access
- 安装范围：Any account。首次验收时选择 Only select repositories，仅选择 `DROWNING2003/Isodo`

GitHub 注册提示安装前需生成 private key。注册动作未生成 private key 或 Client Secret，未安装。当前 Control 走 user access token 的 Authorization Code + PKCE，不用 installation token，不读取私钥；如安装流程要求私钥，应由管理员在服务器安全保存，不能提交到 Git。

## 当前入口的只读核对（2026-11-02）

服务器 `jfcs1024`：`10.210.10.32`。独立 Nginx 配置 `/opt/isodo/nginx/nginx.conf`，仅绑定 `10.210.10.32:18443`。`api.drowning.top` 转发到 `10.210.10.32:5004`；`web.drowning.top` 的 `/v1/` 同样转发到该 Control。Nomad 作业 `isodo-api-pr18-test` 正在运行。

已使用真实 TLS 校验、定向解析检查：Web 首页 HTTP 200；线上 `/v1/mcp/oauth/callback` HTTP 404，说明新代码尚未部署。公共 Google DNS-over-HTTPS 与当前 DNS 查询 `api.drowning.top` 均为 NXDOMAIN；没有证据证明公网可访问，服务器地址也不是公网地址。浏览器 OAuth 回调由用户浏览器完成，当前入口要求用户连 VPN 并能解析该域名。它可以用于受控 VPN 演示，但不是公网产品入口。

没有修改 DNS、Nginx、Nomad、数据库、现有 Node、VM，也未重启服务。注册 URL 的确定不代表部署已完成。

## 安全部署配置（需另行授权实施）

Control 环境：

```dotenv
ISODO_GITHUB_OAUTH_CLIENT_ID=Iv23li6a6bUavtOTqZnJ
ISODO_GITHUB_OAUTH_REDIRECT_URI=https://api.drowning.top:18443/v1/mcp/oauth/callback
# 仅通过服务器隐藏输入设置真实值：
ISODO_GITHUB_OAUTH_CLIENT_SECRET=
```

沿用 `ISODO_INJECTION_KEY` / `ISODO_INJECTION_KEY_FILE` 加密设施，保证所有 Control 副本读取同一密钥并备份。现有 Nomad 将 `/root/zkp/isodo-pr18-test/api.env` 挂载为 `/run/isodo/api.env:ro`；实测源文件为 root:root、0600。可以在服务器终端隐藏输入后写入此文件，禁止在聊天、命令行参数、源码、Electron 配置或日志中传递 Secret。修改配置和服务重启均需要后续授权。

Node 环境：

```dotenv
ISODO_MCP_BROKER_URL=https://api.drowning.top:18443
```

这是管理员固定的 Control origin，不能由 Guest 指定；Node 必须能解析并以受信任 TLS 连接它。Nginx 的 api 全路径代理已覆盖 `/internal/mcp/{bindingID}`；不应在 web 静态站公开内部路由。内部 Broker 必须通过独立高熵绑定 capability 验证；capability 只经现有 Control→Node mTLS 下发，Control 仅保存 SHA256，Node 使用现有 AES-GCM injection 存储加密落盘。

还需：迁移 `0016_mcp_market.sql`；若迁移角色与 API 运行角色不同，执行 `psql -v app_role=API运行角色 -f deploy/sql/grant-mcp-market.sql`，仅赋予目录 SELECT 与账号状态表 CRUD 权限；匹配版本的 API/Node/Guest 与五位端口模板、Guest Python 3.11+（标准库 tomllib）或 Python 3.10 + python3-tomli、已安装支持 HTTP MCP 的 Codex。不能把旧生产镜像当作已升级。

## 授权与实际调用链路

1. 收藏只写 `mcp_favorites`，不启动 OAuth。
2. Electron 使用系统浏览器打开 Control 返回的 GitHub 授权 URL，通过事务状态轮询通知完成。
3. Control 从真实 401 challenge 发现 protected-resource metadata，严格限定 GitHub resource/issuer/端点，发现 RFC8414/OIDC 元数据；无 DCR 回退。Authorization Code + PKCE S256，五分钟一次性 state，账号绑定、取消、重放拒绝。
4. 固定 HTTPS Callback 校验 Host、路径、state、issuer 与事务；服务端换票并执行 initialize→initialized→tools/list。未通过协议校验不保存为 connected。
5. Token 与 refresh token 使用既有服务端 AES-256-GCM 加密，仅在 Control 解密。PostgreSQL 行锁跨副本串行刷新；轮换 refresh token 原子写入。不存在 refresh token 时按真实有效期要求重新授权。刷新不自动重放 tools/call。
6. 用户显式绑定沙箱；创建表单只展示收藏，默认未选中，未连接不能选。配置失败返回 `202` 和 pending/failed，不报告全部成功；重试复用已创建沙箱。
7. Node 在现有 Guest 出站 proxy 通道处理 `/isodo-mcp/github`，逐请求校验 RuntimeIdentity、generation/revision、boot_id、guest_epoch、running。不会新增宿主机监听端口或扩大默认 Guest 出网范围。
8. Guest Codex 使用 `http://127.0.0.1:31080/isodo-mcp/github` 与 `Bearer isodo-mcp-placeholder`；Node 使用仅有绑定权限的 capability 调用 Control Broker；Control 再加入 GitHub Token 调用固定 readonly endpoint。Guest 不获取真实 Token、Secret 或上游带令牌 URL。
9. Broker 再查 tenant、Sandbox、绑定、Node assignment、generation/revision、运行状态和连接状态；逐消息方法/只读工具 allowlist；只返回过滤后的工具。该入口不是任意 URL 代理，不是 Public HTTP Preview，不涉及 T3 Preview 鉴权。
10. 撤销先提交数据库，再同步 Node。后续请求立即被拒；在途请求每 200ms 检查数据库与 Node boot fence，结果返回前再检查。快速断开/重连通过 grant version 防止旧请求复活。

每个 tools 请求新建独立上游 MCP Session，支持 JSON/SSE 响应、协议版本、MCP Session ID、分页、超时和上游 401/403。向 Guest 提供 sessionless Streamable HTTP JSON 响应；不向 Guest转发未经校验的上游 stream 或密钥。暂不支持 server-initiated sampling/elicitation 或持续 SSE 订阅。

## Codex 配置与应用

真实 T3 `CODEX_HOME` 为 `/workspace/.codex`。Node 仅合并自有 `mcp_servers.isodo_github` 段到 `config.toml`，保留模型与其他 MCP。使用标准库 tomllib 校验完整前后文档、目录 FD/O_NOFOLLOW 拒绝符号链接、文件锁、原子替换和私有备份；与手工同名配置冲突时拒绝覆盖。相同配置不重复写入备份。

配置只含内部 URL、工具列表和无上游权限的占位符。绑定保存在 Node 加密存储，Stop/Start 保留；建立出站环境时恢复配置。运行中可在“安全连接”页添加、移除或“应用配置”。UI 明确提示重启当前 Codex 会话；尚未实现自动重启已有 Codex 会话的按钮，不能声称热加载已验证。撤销不依赖 Agent 重启。

旧静态 Header MCP 凭证仍保留在凭证库/API；创建选择区的 MCP 改为收藏目录。Agent 和 HTTP 凭证选择保持原有链路。

## API

OpenAPI 的 MCP 分组生成 Go/TypeScript 客户端和端点文档：catalog 列表/详情，favorites 列表/PUT/DELETE，connections 状态/DELETE/authorize/tools，事务状态/取消，browser callback，沙箱绑定列表/PUT/DELETE/sync。`market_mcp_id`、`oauth_connection_id`、`sandbox_binding_id` 是不同 ID，接口不返回 Token 或 capability。

## 验证与后续扩展

见 [验收记录](validation/github-mcp-market-20261102.md)。只有真实 OAuth、真实 CHV Codex 发现及只读调用、撤销和 Guest 无密钥扫描全部完成，才能标记正式接入。

新增供应商时新增经过审查的 catalog seed、固定 metadata/issuer/network allowlist、最小工具权限及 OAuth 客户端注册配置，然后复用 favorites/connection/binding 表与 Node capability 链路。不能仅添加卡片就标记可连接，不接受 Guest 任意 URL，不将 DCR 作为通用必选前提。

参考：[GitHub remote server](https://github.com/github/github-mcp-server/blob/main/docs/remote-server.md)、[Host integration](https://github.com/github/github-mcp-server/blob/main/docs/host-integration.md)、[GitHub App 注册](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app)、[Codex MCP](https://developers.openai.com/codex/mcp/)。参考 AIofCage 的 discovery/PKCE、串行 refresh 和 Vault 边界，未照搬 DCR 必选、loopback callback 或 Colima 架构。

Web 验收也可从现有 Web 页面发起授权：点击时同步创建空白浏览器窗口，完成发现后导航到固定 GitHub 授权页，避免异步操作被弹窗策略拦截。Electron 继续使用系统浏览器。
