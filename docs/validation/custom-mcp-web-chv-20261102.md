# 自定义 MCP Web / CHV 真实验收（2026-11-02）

## 环境与版本

- 功能 PR：[#32](https://github.com/DROWNING2003/Isodo/pull/32)，已合并；main 合并提交 `18407217f49ad60fc1211422f4bc1e5e13a62d86`。
- jfcs1024 的隔离演示 API / Node 与 Web 版本：`custom-fd0f022`。
- Web：`https://web.drowning.top:18443`。沿用 HTTPS、Nginx、既有 Node 状态与模板，没有新增公网监听、VM 网卡或修改 Casdoor 登录。
- 专用 CHV Sandbox ID：`03e681ff-98f9-45b4-b3e7-d1ee9b324374`，名称 `custom-mcp-e2e-20261102`。
- 模板 build：`a13ad294-3127-45a4-b4ba-404001a4f14a`；Guest Codex `0.130.0`。
- 自定义服务：DeepWiki，`https://mcp.deepwiki.com/mcp`，无需认证，仅访问公共仓库 `openai/codex`。
- 用户明确授权该测试沙箱使用既有 fenno 模型连接；本次没有启用 GitHub 私有仓库 MCP。

## 实际执行

以下均为真实远程 CHV，不是 fixture。Commands 经公开 HTTP API、临时 API Key 调用，MCP 与模型连接通过 Web 账号会话选择并统一保存。

| 测试 | Sandbox ID | 耗时 | 结果与证据 |
| --- | --- | --- | --- |
| 创建并等待 running | `03e681ff-98f9-45b4-b3e7-d1ee9b324374` | 24.929s | created `12:43:08.595157Z`，running `12:43:33.524508Z`；首次产物获取约 16.9s |
| Web 添加服务并发现工具 | 同上（添加为账号配置） | 未单独计时 | initialize / tools/list 验证成功，显示已连接；添加本身未绑定沙箱 |
| Web 统一保存 | 同上 | 未单独计时 | DeepWiki 与 fenno 勾选并持久化，刷新后仍选中，GitHub 未勾选 |
| Guest 工具发现 | 同上 | 4.815s | loopback → Node → Control → 公网 DeepWiki 返回 `ask_wiki_question`、`read_wiki_contents`、`read_wiki_structure` |
| Codex 真实调用 | 同上 | 28.834s | execution `062c9dfde1ece5ad7f321c6833c9d872`；`read_wiki_structure({"repoName":"openai/codex"})` 的工具事件 `status=completed`、`error=null`；最终回答 Overview / Core Concepts；exit_code=0，output_incomplete=false |
| Web 取消勾选并保存 | 同上 | 未单独计时 | 撤销后使用原 loopback URL 再请求 tools/list |
| Guest 撤销验证 | 同上 | 0.105s | execution `8b4041ce86b5b670e10fb05f1e4c5821`；返回 HTTP 403 `MCP is not bound` |
| 删除并清理 | 同上 | 1.217s | requested `12:50:21.121730Z`，completed `12:50:22.338354Z`；API cleanup_done/succeeded，Node journal deleted，无该 ID 的运行文件目录 |

时间戳为 UTC。Codex 测试仅对该服务设置 `default_tools_approval_mode="approve"`，保留 read-only sandbox；没有关闭 TLS、身份校验或使用危险绕过标记。测试进程从既有受保护的 Guest proxy.env 加载代理，不输出其内容。

## 发现的限制与失败记录

1. 新 Sandbox 通过 Commands 直接启动 Agent 时，首次工作台连接尚未建立出站桥：proxy.env 不存在，直接 loopback 请求被重置。首次进入 Web 工作台后代理就绪，上述真实调用成功。因此本轮验证的是 **Web 初始化后的 Agent 链路**；尚未证明全新 Sandbox 无需工作台初始化即可纯 API 调用 MCP。应补充该初始化时序的独立修复和回归。
2. 最初缺少代理环境的 Codex 尝试不计为成功；成功依据是后续完整工具事件，而非进程退出码。
3. 真实第三方 Bearer Token 服务未提供测试凭证，仅通过 PostgreSQL / HTTP fixture 与安全组件测试；不能宣称已完成真实 Token E2E。
4. 此报告没有新增 Electron 交互验收、跨账号真实浏览器测试或真实第三方恶意服务验收。账号隔离、旧 generation、在途撤销由自动化组件 / PostgreSQL 测试覆盖。

## 清理与保留

- 本轮专用 Sandbox 已直接删除，不保留磁盘；旧演示 Sandbox `93e00130-b7db-448c-b809-d4a1d564ec94` 也已完成删除。
- 临时 Runtime API Key 已撤销，服务器明文文件已删除。
- 账号内保留无认证的 DeepWiki E2E 示例配置，未绑定任何本轮沙箱。没有删除用户原有 GitHub 账号连接或 fenno 配置。
- 部署前数据库、API/Node 配置及 Web 版本位置已备份；备份和凭证均不入库。

## 自动化质量检查

功能提交前已通过 `go test ./...`、相关 `go test -race`、`go vet ./...`、`make generate-check`、Web TypeScript 检查、相关前端测试（29 文件 / 164 测试）、Web 生产构建、Linux API/Node 构建及真实临时 PostgreSQL 的迁移、回滚和权限验证。PostgreSQL 测试中的 Node / 上游是 fixture，不能替代本报告的 CHV 验收。此次仅新增验收文档，执行 Markdown diff 与 `git diff --check`。

## 后续修复跟踪

上述首次工作台初始化问题保留为当时部署版本的历史证据。后续代码与独立 CHV 验证见 [CHV Ready 出站验证](chv-ready-egress-20261102.md)；该记录区分组件验证与公开 API / 真实模型端到端验收。
