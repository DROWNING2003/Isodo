# GitHub MCP 市场验收记录 · 2026-11-02

分支：`drowning/github-mcp-market`，基于合并 PR #25 的 main。未创建 commit、推送或 PR，未部署，未修改已有服务/数据库/VM。

## GitHub App 注册（真实）

由用户登录 GitHub，并在最终创建时明确确认。GitHub 返回 Registration successful。

- App：Isodo MCP DROWNING2003，ID `5576530`
- Client ID：`Iv23li6a6bUavtOTqZnJ`
- 设置：https://github.com/settings/apps/isodo-mcp-drowning2003
- Callback：`https://api.drowning.top:18443/v1/mcp/oauth/callback`
- Contents / Issues / Pull requests：Read-only；Metadata：必需只读
- 无 Webhook、Device Flow 或 wildcard callback，允许 Any account 安装
- 未生成 Secret 或私钥，未安装到仓库
- 截图：本机 `/tmp/isodo-github-app-created.jpg`

## 自动化与组件证据

- `go test ./...`：通过（需要额外环境的集成测试仍会 skip，不能视为真实运行）。
- 相关包 `go test -race`：通过。
- `go vet ./...`：通过。
- Linux amd64 的 API、Node、Guest 交叉编译通过，产物仅保存在本机 `.tools/mcp-linux/`，未上传或部署。
- `make generate`、`make generate-check`：通过；包括 OpenAPI Go/TS、Node proto、SQLC 与文档站端点导航。
- Web 定向测试：23 项通过，覆盖市场收藏、未授权禁用、默认未勾选、侧栏以及 Agent 配置回归。
- Web 标准配置 `tsc --noEmit -p app/apps/web/tsconfig.json`：退出 0，通过；仍输出既有 Effect suggestion 诊断，无 `error TS`。未修改类型检查规则。
- Codex config 组件测试：Python tomllib 校验、保留模型/其他 MCP、重复应用、移除、手工同名配置冲突、符号链接拒绝。宿主组件测试替换临时 workspace 并跳过 UID 变更；不等于真实 Guest 验收。
- Node capability 加密存储恢复、HTTP 规则更新不丢 MCP、公开 injection View 不泄露 capability、删除清理：通过。
- Streamable HTTP fixture：initialize、initialized、工具 allowlist、JSON/SSE、Session ID、协议版本、拒绝 Token 重定向：通过。服务是测试 fixture，不是 GitHub。

## 隔离 PostgreSQL（真实数据库，合成上游）

命令：`ISODO_TEST_POSTGRES_IMAGE=aslan-spock-register.qiniu.io/postgres:17.4 bash scripts/test-mcp-market-postgres.sh`

测试：`TestPostgresMCPAccountOAuthBindingLifecycle`，0.58s。
数据库环境：本机 Docker 中新建 PostgreSQL 17.4，随机密码和随机 loopback 端口；没有读取生产 `.env` 或连接现有数据库。OAuth、MCP upstream、Node Sync 是明确的合成 fixture。

Sandbox ID：`18948323-de06-4423-aa22-1c9430af4c42`（仅专用数据库行，没有启动 VM）。

| 阶段 | 结果 |
| --- | --- |
| Seed / 收藏 / 独立客户端读取 / 账号隔离 | PASS |
| 普通 API Key 无权发起授权 | PASS |
| PKCE / state / 回调重放 / 取消 / 超时 / 错误码 | PASS |
| 加密持久化 Token / Secret 不出响应 | PASS |
| 六个并发调用只执行一次 refresh | PASS |
| 显式绑定 / 跨租户拒绝 / 只读工具拒绝写调用 | PASS |
| 移除绑定拒绝后续调用 | PASS |
| 断开 OAuth 取消在途调用 / 旧连接 version 拒绝 | PASS |
| Stop 保留绑定 / 删除意图清理绑定且保留账号授权 | PASS |
| 迁移 Down→Up | PASS |
| 删除测试行、停止并移除专用数据库容器、删除密码文件 | PASS |

日志：普通运行 `/tmp/isodo-mcp-market.WLpdmG/{test.log,migrations.log,postgres.log}`；race 运行 `/tmp/isodo-mcp-market.jaxLPb/`、总汇 `/tmp/isodo-mcp-postgres-race.log`。race 下同一流程 0.63s，通过；Sandbox ID 为 `13bfb7dd-30ec-4897-86d2-f99c21e11d57`，仍仅为数据库测试行。所有日志只有合成凭证，不含真实 GitHub Secret。

## 页面视觉验证

用真实 React 组件和本地预填查询数据，在浏览器检查市场卡片、收藏页、未配置 OAuth 的禁用反馈、创建选择区默认未选中和未授权禁用。截图 `/tmp/isodo-mcp-ui-fixture.jpg` 明确标注“本地 UI fixture：无真实 OAuth / Sandbox”。临时 HTML/TSX 已删除，未加入产品路由。

没有构建并运行新版 Electron 完整登录/创建流程；没有用上述 UI fixture 冒充桌面真实 E2E。

## 实际部署只读核对

`jfcs1024` 的 Nginx 监听私网 `10.210.10.32:18443`，api 转发 `:5004`，Nomad `isodo-api-pr18-test` running。真实 TLS、定向解析下 Web HTTP 200、未部署 MCP Callback HTTP 404。公共 DNS-over-HTTPS 和当前 DNS 均查询 api 域名为 NXDOMAIN。root 配置文件 `/root/zkp/isodo-pr18-test/api.env` 为 0600，只读取了 URL 类白名单字段及权限；未输出 Secret。

## 真实 GitHub + Codex + CHV

**BLOCKED / NOT RUN。** 尚缺安全保存的 Client Secret、GitHub App 安装、经授权部署匹配的 API/Node/Guest/迁移，以及用户浏览器可达的 Callback 与 Node 可解析的 Broker origin。真实 GitHub 的匿名 challenge 与公开 metadata 已读取，不能据此认为 OAuth 或工具调用成功。

下一次使用专用 CHV Sandbox 完成：Isodo 登录→收藏→系统浏览器 OAuth→真实工具发现→显式绑定→真实 Codex 只读调用并核对仓库数据→撤销拒绝→Guest 文件/env/argv/log 无真实密钥扫描→删除并验证清理。没有这些证据前，不得标记 GitHub MCP 已接入。

## 主要修改

- `internal/control/mcpmarket/`：目录、收藏、OAuth/PKCE/refresh、绑定及可信 Broker；`internal/mcpclient/`：MCP JSON/SSE 客户端。
- `migrations/0016_mcp_market.sql`：五类表、账号隔离约束、生命周期清理和绑定所有权触发器；SQLC 模型同步生成。
- `api/proto/isodo/node/v1/runtime.proto`：mTLS `SyncMCP`；Node `chv_mcp.go`、既有 injection/egress：可信 capability、身份校验与 Codex 配置合并。
- `MCPMarketPage.tsx`、`mcpMarket.ts`、侧栏、创建表单、安全连接页：市场、收藏、授权状态、显式选择、部分失败与应用操作；使用生成客户端类型。
- Public OpenAPI、Go/TS bindings、文档站端点和指南同步更新；配置入口为 `.env.example` 与 Makefile。

下一阶段 P0 是受控部署与真实 CHV/Codex OAuth 验收；P1 是已有 Codex 会话一键重启/重新连接体验和真实 Electron 自动化。其他供应商需独立审核 metadata、固定 issuer、权限和客户端注册，不以增加卡片代替实现。
