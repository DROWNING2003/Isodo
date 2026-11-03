# Skill 市场与沙箱使用

## 使用方式

全局侧栏的 **Skill** 打开 `/skill-market`，包含市场、我的 Skill、搜索与详情。市场初始提供 Isodo 自编的代码审查、调试和测试编写三种指令 Skill；收藏只加入个人列表，不自动安装或授予沙箱权限。

点击“制作 Skill”，填写英文短横线名称、说明和指令正文，创建账号私有 Skill，并自动收藏。第一版只制作指令正文，不导入 GitHub 仓库、不上传附件、不执行安装脚本。内容不可原地修改；需要不同指令时制作新 Skill。私有内容仅该账号可见，名称不得与平台市场技能重名，每账号最多 100 个。

在远程 CHV 沙箱的 Agent 工作台进入“安全连接”，勾选收藏的 Skill，再点击右下角统一保存。保存一并处理凭证、MCP 和 Skill；取消勾选 Skill 后保存将移除该功能管理的文件，保留其他手工 Skill。取消收藏不会自动卸载已经选择的 Skill；要卸载，需在对应沙箱取消勾选并保存。

首版同步到 `/workspace/.agents/skills/isodo-<Skill ID>/SKILL.md`，由 Codex 的项目技能发现机制加载。工作台输入 `$` 可以搜索并选择已同步 Skill 的标签；新建任务后可通过 `$技能名称` 显式请求使用；也可由 Agent 按说明按需选择。安装成功并不保证每次任务都会触发该 Skill。目录和格式依据 [Codex 官方 Skill 文档](https://learn.chatgpt.com/docs/build-skills)。目前不宣称 Claude、OpenCode、Lima 或完整桌面端的同步已经支持和验收。

## 同步与生命周期

- `GET /v1/skills`：平台和当前账号可见条目，带收藏状态及正文。
- `POST /v1/skills`：制作不可变 v1 指令 Skill，字段 name、description、body。
- `PUT /v1/skills/{skillID}/favorite` / `DELETE`：收藏及取消收藏。
- `GET /v1/sandboxes/{sandboxID}/skills`：读取完整选择及同步状态。
- `PUT /v1/sandboxes/{sandboxID}/skills`：提交完整 `skill_ids` 列表；空列表显式移除所有托管 Skill。最多 32 个，序列化安装内容合计不超过 96 KiB。

账号接口不接受 Sandbox API Key，沿用现有会话、CSRF 和账号身份。Control 检查沙箱所有权、删除状态及收藏关系；保留中的既有绑定可在取消收藏后继续同步。沙箱行锁串行化选择保存和生命周期更新。运行中的匹配 Node 通过既有 mTLS Runtime `SyncSkills` 接收文档与 generation/revision，Node/Guest 继续验证当前 boot_id/guest_epoch 和运行身份。

响应状态 `applied` 只表示托管文件已经同步，新 Agent 会话加载更改。停止中、停止或 Node 不可用时保存为 pending；RPC 失败保存为 failed，HTTP 202，不伪称已安装。Start 后已安装文件随磁盘保留；**停止期间修改的选择不会自动执行，需要启动后重新保存**。删除沙箱会清理持久选择，VM 与磁盘仍由既有删除流程清理。

安装器是 Node 固定的 Python 命令，Skill 内容作为 JSON 数据参数传入，不拼入 Shell。目录遍历使用 openat 目录描述符和 O_NOFOLLOW；目标仅限托管 UUID 目录。文件按临时文件 + rename 写入，并验证已安装文件的内容哈希；遇到手工修改、额外文件、符号链接等冲突时拒绝覆盖或递归删除。每个文件原子替换，整组文件不是跨文件事务；进程中断可能留下需检查的冲突，响应不标记 applied。

Skill 是可读指令文件，会进入 Guest 磁盘及包含该磁盘的快照。不要填入凭证。它不使用 MCP 的 Token Broker，也不能获得额外工具权限；Agent 仍按自己的运行权限执行任务。移除文件不抹除已运行会话读过的指令，需新建任务。Guest 中拥有文件写权限的用户可以复制内容，Skill 移除不是对已读取信息的权限撤销。

## 部署与验收边界

需要 migration `0018_skill_market.sql`，并以迁移 owner 更新 `deploy/sql/grant-mcp-market.sql` 的应用角色授权，再使用匹配的 API、Node 和 Web。协议生成产物及 API 文档随代码更新。不需要 Guest RPC、Nginx、公开端口或 VM 网卡变化。实际部署版本见[当前状态](current-status.md)。

测试包括：真实临时 PostgreSQL 中的账号隔离、收藏、选择、同步结果持久化、停止准入与删除触发器；上游 Runtime 是 fixture。安装器组件测试在临时目录执行同一 Python 脚本，验证安装、重试、移除、手工文件保留、旧版本拒绝和符号链接拒绝。前端验证制作表单、收藏、选择恢复、失败状态、账号切换及统一保存。

上述自动化测试中的 fixture 不等同于 CHV。部署后已另外完成 [真实 Web / CHV / Codex 验收](current-status.md)，覆盖制作、收藏、工作台选择、Guest 文件及原生发现、任务使用、移除、Stop/Start 和删除清理。测试专用沙箱已直接删除。


### 工作台菜单接线

部分既有 Guest T3 的 provider 快照没有及时提供新增 Skill。Web 通过当前账号会话查询该 Sandbox 的已应用选择，将确认 `version == applied_version`、`sync_status=applied` 的元数据接入现有 Skill 菜单和标签，不更改 Codex 协议、不向其他 Agent 或本地沙箱展示。目录路径由受控 UUID 派生。待同步、失败、取消选择或账号查询失败时，不沿用缓存中的本功能托管项；其他用户手工技能保留。这是展示接线，不能作为清除已有 Agent 会话内容的权限边界。
