# Skill 市场真实部署与验收（2026-11-02）

## 环境与发布来源

- 服务器 jfcs1024（10.210.10.32），Web `https://web.drowning.top:18443/skill-market`，API 沿用 `https://api.drowning.top:18443`。
- API/Node 发布 `skill-20261102-v1`，Web 最终发布 `skill-20261102-web2`。API Nomad job `isodo-api-pr18-test`；Node systemd `isodo-node-pr18-test.service`。
- 工作分支 `drowning/skill-market`，基线 `7d5570af9ea8a1f992ffa2c4b7b380269fc9b574`。用户授权部署测试；部署发生在 Git 提交之前，发布来源由工作区源码归档和校验清单记录，后续提交与 PR 状态以仓库为准。
- 初次部署产物及源码差异归档在 `/opt/isodo/skill-prepare-20261102`，`skill-checksums.json` 记录构建文件及源码 SHA-256；Web 接线修复另存 `web2-checksums.json` 与对应源码归档。
- 部署前数据库、Nomad job、受保护 API/Node 配置、Node drop-in 和 Web 指向备份在 `/opt/isodo/backups/before-skill-20261102-v1/`。真实凭证不入库、不进入聊天或日志。
- migration `0018_skill_market.sql` 和应用角色授权已应用。沿用原 Guest 模板、Nginx、HTTPS、域名和网络，没有新建公网端口或 VM 网卡。Node 更新前确认该 Node 没有活动 Sandbox。

## 专用资源

Sandbox ID：`af7345ba-8b84-40c6-9e4e-fbbb1e8bc5be`，名称 `skill-web-e2e-20261102`，真实 CHV，模板 build `a13ad294-3127-45a4-b4ba-404001a4f14a`，Codex 0.130.0。

Skill ID：`2b7f24ae-2d7e-4e7c-9911-07b3706b3e8e`，名称 `isodo-skill-e2e`，个人私有指令。通过 Web 表单创建并自动收藏。正文包含固定返回标记；发给模型的测试提示只引用 Skill 名称，没有包含该标记。

用户单独确认该 Sandbox 使用 fenno / gpt-6.1-sol 测试，GitHub / DeepWiki MCP 均未启用。模型请求沿用已有沙箱外凭证注入；不修改 Casdoor 登录或 GitHub 授权。

## 实际测试结果

以下各阶段均使用上述同一 Sandbox ID。生命周期、文件检查和清理经公开 Runtime API；账号级制作、收藏、选择与任务操作经真实 Web 会话。没有 Mock Node 或模拟 VM。

| 测试阶段 | 耗时 | 结果与证据 |
| --- | --- | --- |
| API 创建并等待 running | 24.871s | created `2026-11-02T13:19:52.928020Z`，guest_ready `13:20:17.799265Z`，revision 1 |
| Web 制作并自动收藏 | 未单独计时 | “我的 Skill”出现私有 `isodo-skill-e2e` |
| 市场收藏与取消收藏 | 未单独计时 | 收藏 code-review 后出现在“我的 Skill”；取消后消失，未改变沙箱选择 |
| 工作台选择并统一保存 | 未单独计时 | Skill 勾选刷新后保留，状态为 applied；GitHub 未勾选 |
| Guest 文件校验 | 0.058s | execution `5e673206f50de959abee58ec151d5524`，298 bytes；SHA-256 `0c75542f3590f372826c84dcfaa40c1daa587144f3a5ac6d761860f210de031f` |
| Stop | 0.700s | revision 2，`13:21:28.623313Z` → `13:21:29.323577Z`，stopped |
| Start | 2.861s | revision 3，`13:21:56.591824Z` → `13:21:59.453037Z`，running |
| 重启后文件保留 | 0.086s | execution `5ce06612a11a0d7392d4e718de9c06d7`，哈希与初次完全相同 |
| 第一次 Web 任务使用 | UI 显示 1m 2s | thread `16465d79-0b4f-4ffa-9774-24bbead52d6a`；工具记录读取目标 SKILL.md，最终仅返回 `ISODO_SKILL_LOADED_7C91` |
| Codex 原生发现 | 0.311s | execution `c8b3e24a4a363f69190fdb6d3de3f8da`；真实 `skills/list` 返回该 name/path、scope=repo、enabled=true，无错误 |
| 自动补全与 Skill 标签 | 未单独计时 | 输入 `$isodo` 出现 Isodo Skill E2e，点击后插入现有 Skill 标签 |
| 新任务使用标签 | UI 显示 1m 3s | thread `2e5f422b-a9f9-4c1a-896f-703ead11c197`，模型 gpt-6.1-sol，最终同样仅返回固定标记 |
| 取消勾选并统一保存 | 未单独计时 | 数据库 version=3，applied_version=3，sync_status=applied，skill_ids={} |
| Guest 移除检查 | 0.066s | execution `3782c713fe147e92b05e4ca8aa83ddaf`，托管 Skill 目录不存在 |
| 新任务菜单移除 | 未单独计时 | 新草稿输入 `$isodo` 显示 No skills found；没有继续调用模型来声称抹除旧上下文 |
| API Key 隔离 | 未单独计时 | 真实 `/v1/skills` 返回 HTTP 403 / SESSION_REQUIRED，Sandbox API Key 不能管理账号技能 |
| 删除 Sandbox | 0.864s | requested `13:34:11.469132Z`，completed `13:34:12.333183Z`，revision 4，cleanup_done/succeeded |

服务端时间戳为 UTC（对应北京时间 21 点）；UI 任务耗时是界面实际显示，未单独计时的阶段不补造耗时。文件/原生发现的 elapsed 是公开 Commands API 整体耗时。

## 测试中修复的问题

首轮 Web 任务已能找到并使用指令文件，但 `$` 自动补全为空。直接在 Guest 调用 Codex 的 skills/list 能发现该 Skill，说明安装正确，缺口在工作台 provider 元数据接线。

补充 `useManagedSkills` / `mergeManagedSkills`，只将 Control 确认已应用的远程 CHV Skill 接入现有 Codex 菜单与标签；账号切换、失败/待应用状态及取消选择不保留托管元数据。保留其他手工 Skill，不修改现有 T3 鉴权、MCP Broker 或 Codex 协议。仅更新 Web 发布，未再次重启 Node。30 文件 / 146 项相关前端测试、TypeScript 检查和生产构建通过后重新真实验证。

## 清理与边界

- Sandbox 已删除；Node journal 为 deleted，运行状态目录中不存在该 ID 的 VM/磁盘路径，sandbox_skills 记录为 0。
- 临时 Runtime API Key 已撤销，服务器明文文件已删除。
- code-review 收藏测试已恢复；个人 `isodo-skill-e2e` 作为无凭证的可读示例保留，未绑定任何本轮沙箱；不宣称账号示例记录也已删除。
- 没有操作业务 VM，没有启用 GitHub MCP，也没有改动用户原有 fenno 配置内容。
- 此轮完成远程 Web → Control → Node → Guest → Codex 的基本闭环。Claude、OpenCode、Lima、Electron 桌面端本轮未验收；停止期间变更仍需启动后显式保存。
- Skill 不是可撤销的秘密；已读指令可能留在旧会话中。移除证明文件和新任务选择入口消失，不证明旧会话遗忘。
- 自动化 Go/race/vet/generate-check、独立 PostgreSQL（Runtime fixture）和真实 CHV 证据分别记录，不能互相替代。应用回退所需备份已确认存在，本次未执行破坏性数据库回滚。
