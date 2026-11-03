# isodo 开发入口

- 当前维护与路演先读 [文档索引](docs/README.md)、[能力状态](docs/current-status.md) 和 [冻结检查](docs/roadshow.md)。过时方案与逐次验收已按用户要求移除，以 Git 历史追溯；当前文档不得把历史计划当作现状。

- 开始实现、评审、提交或准备演示前，必须阅读 [参赛要求与评分标准](docs/competition-requirements.md)，并遵守其中的截止时间、PR 工作流、开发过程留痕和 codebase `v0` 基线要求。
- 产品概念与关系见 [产品概念与术语](docs/product-context.md)；调整交互前阅读 [产品决策](docs/product-decisions.md)。
- 交付时按参赛要求逐项核对源代码、运行说明、架构设计（含模块规格与分工）、demo 视频和演示环境；如实说明 mock、已实现和未验证的能力。

## drowning.top Web 发布

- 构建或发布 jfcs1024 Web 前，先读 [Web 预览配置与发布检查](docs/web-preview-release-checklist.md)。
- 从仓库根目录运行 `bash scripts/build-drowning-top-web.sh`；不要直接 `vp build` 手工拼环境变量。必须固定预览域名和完整工作台模式，否则首页可用但沙箱连接失败。
- 上传前及切换 Web 软链接前，分别对待发布目录运行 `python3 scripts/check-drowning-top-web-build.py <目录>`；缺少清单或摘要不符即停止。
- 首页 HTTP 200 不代表沙箱工作台可用；交付时分别记录构建配置检查、网关认证、工作台连接/文件/终端验证结果，未执行项明确标为未验证。

## jfcs1024 Node / MCP 发布

- 发布前读取 [Node 到 MCP Broker 的域名解析](docs/web-preview-release-checklist.md#node-到-mcp-broker-的域名解析)。先保存并比较 `systemctl cat/show` 的有效配置；不要用仅包含 ExecStart/EnvironmentFile 的新片段覆盖已有 drop-in。
- 升级必须保留 PrivateMounts、BindReadOnlyPaths、hosts 映射及其他非版本设置。仅替换本轮需要的二进制/环境文件路径，核对 Broker URL 和证书路径；未授权不得中断运行中的沙箱。
- 在 Node 实际运行命名空间内检查 Broker 域名解析和 HTTPS 健康，再从专用 Guest 验证 MCP initialize/tools/list。账号 connected、绑定 applied、API健康或 Web首页200，均不能替代此验收。
- Agent 启动时 MCP 失败后，还需验证新任务工具加载；resources/templates 为空不能据此判断 tools 不可用。用户主动移除的连接不可自行重新绑定。

## Git 提交、推送与 PR

- 提交变更前先阅读 [git-commit 技能](.agents/skills/git-commit/SKILL.md)，按其中的流程检查工作区、暂存区和提交历史；只暂存本次明确涉及的文件，发现疑似密钥或凭证文件时先停止并提醒用户。
- **本地 commit、推送远端分支、创建 PR 是三个独立动作。** 用户只说“提交/commit”时，只能创建本地 commit；不得据此推送或创建 PR。每个动作都必须有用户明确授权。
- 用户要求 PR 草稿时，只准备标题和描述，不创建 PR、不推送。用户明确要求创建 PR 时，使用工作分支，将 PR 指向确认过的目标分支；创建 PR 所需的分支推送也必须属于用户对该 PR 操作的明确授权。
- 默认分支（如 `main`）不得直接推送。PR 工作应推送到专用工作分支；只有用户明确要求直接推送默认分支时才可例外。
- 不得擅自 amend、跳过 Git hooks / 签名、修改 Git 配置，或运行破坏性 Git 命令；确需执行时先取得用户对该具体操作的明确授权。
- 完成后分别报告本地 commit、远端推送和 PR 的实际状态；没有执行的动作不得描述成已完成。

<!-- codebase-memory-mcp:start -->
## 代码发现

优先使用 codebase-memory-mcp；项目尚未索引时先运行 `index_repository`。

1. `search_graph`：定位函数、类、路由与变量。
2. `trace_path`：追踪调用关系。
3. `get_code_snippet`：读取已定位的符号源码。
4. `query_graph`：查询复杂关系。
5. `get_architecture`：了解整体结构。

搜索字符串、配置、非代码文件，或图工具结果不足时，使用 `rg` 等文件搜索。
<!-- codebase-memory-mcp:end -->
