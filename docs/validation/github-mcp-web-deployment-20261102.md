# GitHub MCP Web 部署与真实验收 · 2026-11-02

本记录补充此前开发阶段验收，不替代历史记录。PR #26 已合并部署，PR #27 修复 Web OAuth 和运行角色数据库权限后已合并。GitHub macOS build 仍排队，不能视为已通过。

## 环境与边界

- 主机：jfcs1024（10.210.10.32），HTTPS 18443。
- Web：`https://web.drowning.top:18443`；Control：`https://api.drowning.top:18443`。
- Nomad API：`isodo-api-pr18-test`，Node：`isodo-node-pr18-test.service`。
- API/Node 版本：`cb235e2`；Web：`6ef16f1`，修正预览域名与 full 工作台构建参数。
- GitHub App：Isodo MCP DROWNING2003（5576530），用户自行安装至 DROWNING2003，安装 ID 177766136。用户实际选择 All repositories，只读；测试只访问 Isodo 仓库。
- Client Secret 由用户在服务器隐藏输入，保存在 root:root 0600 API env 文件；未打印、提交或传入 Guest。
- Node 私有 mount namespace 内解析 Broker 域名，没有修改系统 DNS、Nginx、VM 网卡或其他 Node。
- 已备份数据库、API job、env、Node 和 Web 发布位置至 `/opt/isodo/backups/before-mcp-cb235e2/`。

## 真实结果

| 测试 | Sandbox ID | 耗时 | 状态与证据 |
| --- | --- | --- | --- |
| Web 现有 GitHub/Casdoor 登录 | — | 未单独计时 | PASS，账号 DROWNING2003 进入沙箱页 |
| 市场读取、收藏、刷新持久化 | — | 未单独计时 | PASS，运行角色补齐表权限后显示 GitHub 已收藏 |
| Web GitHub App OAuth + PKCE | — | 含用户确认，未计时 | PASS，真实 GitHub Authorize 后市场显示已连接，非合成上游 |
| Web 创建 CHV、显式选择 GitHub MCP | 93e00130-b7db-448c-b809-d4a1d564ec94 | API 至 running 68.28s | PASS，匹配新模板，Guest 已运行 |
| Guest MCP initialize / tools/list / 读取 README | 93e00130-b7db-448c-b809-d4a1d564ec94 | 4.93s | PASS，通过公开 Commands API 执行 Guest Python MCP 客户端，13 个只读工具，读取真实 main README，commit 65aaac35e499fd17954641be8e3c4d8f36edf259 |
| 同模板缓存命中创建 | ec56a31e-f556-4342-8e69-734f9482abee | API 8.06s，Node 6.67s | PASS，artifact fetch 0ms、磁盘 4.708s、VMM 0.117s、Guest 1.824s |
| 删除性能对照专用沙箱 | ec56a31e-f556-4342-8e69-734f9482abee | 1.11s | PASS，公开 API 返回 deletion.completed_at，Node VM 目录已不存在 |

首个冷缓存启动 Node 阶段：获取并校验模板 58.490s，磁盘 5.200s，VMM 0.138s，Guest 2.515s；排队 3ms。模板根盘 4 GiB，下载约 25s，随后校验。不是每次重装，也没有把创建 API accepted 当作 ready。

## 发现并修复

1. 迁移以 postgres 建表，运行角色 `isodo_api_test` 缺少新表权限，市场读取失败。目录只赋 SELECT，其余账号状态 CRUD。
2. `sandbox_port_previews` 同样缺权限，Sandbox 删除触发器返回 500。补齐此表 CRUD 后，公开 API 删除与真实清理成功；没有绕过触发器或直接删 VM。
3. Web OAuth 以前只支持 Electron。现在点击同步预开 popup，固定 GitHub origin/path 校验，状态轮询；Electron 仍系统浏览器。
4. 手工发布漏传 `VITE_ISODO_PREVIEW_DOMAIN`，前端拒绝合法网关；缺 `VITE_BOUNDLY_WEB_SURFACE=full` 则配对后跳回列表。固定构建脚本 `scripts/build-drowning-top-web.sh` 防止遗漏，安全校验保持不变。

## 自动化检查

Web 相关 28 文件、153 项测试通过；Web TypeScript 检查、生产构建通过。专用 PostgreSQL 17.4 运行角色权限、账号生命周期与迁移 Down→Up 通过，容器及临时密码已清理。数据库 OAuth 上游仍为合成 fixture，不冒充 GitHub 真实调用。

工作台截图：本机 `/tmp/isodo-web-workspace-connected.png`；OAuth 成功截图：`/tmp/isodo-web-mcp-connected.png`。

## 尚待完成

- 已确认正确构建参数发布后，Web 进入真实云端任务界面（文件、安全连接和输入区）；Agent 模型配置验证仍进行中。
- 实际 Codex CLI 已验证：通过公开 Commands API 在专用 Guest 运行 gpt-6.1-sol，调用 isodo_github.get_file_contents 成功，50.224s，exit_code=0、output_incomplete=false，返回 README 标题 # Isodo 与 main commit 12e549861fff8aafd8755e9724b5284699905893。证据摘要 `/tmp/isodo-codex-mcp-evidence.json`。
- 直接 Commands 进程默认不继承出站环境，首次模型请求失败；使用现有 Guest proxy.env 后正常。非交互 Codex 的默认 MCP 工具确认造成取消，测试仅对已获用户授权、服务端限制为只读工具的 isodo_github 设置会话级 default_tools_approval_mode=approve；仍 read-only Sandbox，未关闭认证、TLS 或运行身份校验，未使用 bypass sandbox 参数。
- 真实解绑后 Guest MCP initialize 返回 HTTP 403（0.114s），Node 路径立即拒绝。
- 有限扫描 5500 个 Guest Codex/Isodo 文件（单文件上限 2 MiB）未命中 GitHub token 特征，模型与 MCP 配置均使用占位符。这不是对所有进程/文件的完整 Secret 审计。
- 首个 E2E Sandbox 为后续 Desktop 验证保留，尚未删除；不记为清理完成。
- 已按 Web→Desktop 顺序完成远程工作台和统一保存的基础接线验收；未验证安装包发布、Desktop 重新发起 OAuth 或完整 Electron 自动化。

下一步性能改进：部署时预热已校验模板缓存；UI 展示下载、校验、磁盘、Guest、工作台阶段；进一步测量发布后冷校验而不是降低完整性校验。

## MCP 勾选状态

安全连接页原来的 checkbox 只是新绑定选择，已绑定时仍为空，存在语义歧义。改为以服务器现有绑定回显；用户修改后显示未保存状态，保存时仅新增或撤销差异。刷新保持绑定，切换 Sandbox 不携带未保存选择；创建表单仍默认未选中。相关测试先观察失败，再通过修复。

## 安全连接统一保存

根据真实页面反馈，去掉 MCP 区块独立保存按钮、重复绑定列表和常驻成功说明，绑定状态直接放在服务卡片。统一使用右下角保存入口；MCP/Agent/HTTP 选择及临时配置修改都先暂存。保存优先撤销 MCP，再应用凭证、Agent 和新增 MCP。失败不会继续授予新 MCP 权限，保留待保存选择并报告部分成功；成功消息五秒收起。

回归覆盖统一入口、撤销优先、后续失败不新增授权、临时 Agent 仅全局保存后落盘、回显与跨 Sandbox 草稿隔离。没有改变 Control、Node、Guest 的权限或身份检查。

## 最终线上 UI 与权限验收（18:26 CST）

代码 PR #30 已合并并发布 Web，静态版本 `0c53438`。专用 Sandbox 仍为 `93e00130-b7db-448c-b809-d4a1d564ec94`，没有重建 VM。

| 测试 | 结果与耗时 |
| --- | --- |
| Web 取消 GitHub 勾选但不保存 | 卡片显示待停用；Guest initialize 仍 HTTP 200，0.150s |
| 点击唯一右下角保存 | 撤销先执行，Guest initialize HTTP 403，0.125s |
| 隔离 Desktop 连接同一远程 CHV | PASS，实际原生 Electron 窗口进入云端任务界面 |
| Desktop 安全连接读取 | PASS，能看到 Web 已保存的未启用状态 |
| Desktop 勾选并统一保存 | PASS，卡片显示已连接/已启用，Agent 同时保留 |
| Guest 再次 initialize | HTTP 200，0.142s |
| Web 刷新回显 Desktop 保存 | PASS，GitHub 勾选、已启用，只有右下角一个保存入口 |

截图：本机 `/tmp/isodo-web-unified-save.png`、`/tmp/isodo-desktop-unified-save.png`。自动化：相关 Web 28 文件、159 项测试、TypeScript、生产构建与 diff 检查通过。macOS GitHub Actions 仍排队，不能当作安装包验收成功。

Desktop 使用独立 userData `/tmp/isodo-mcp-e2e-profile` 和本轮构建的 Electron main/preload，复制 Electron bundle 仅为区分其他开发实例；未覆盖用户原有 App 或 Profile，没有创建本地 VM。它是开发实例验收，不是正式安装包验收。实际 Codex MCP 调用仍是前述公开 Commands API 驱动的 Guest CLI，不冒充从 Desktop 聊天框发起的 Agent 任务。

临时 Runtime API Key 已撤销，服务器 0600 明文文件已删除。缓存性能对照沙箱已删除并确认 VM 目录清理。主测试沙箱因用户正在页面使用而保留，GitHub OAuth/收藏和已授权 MCP 绑定保留，不把此项列为全部资源清理成功。
