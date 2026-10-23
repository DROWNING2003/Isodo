# isodo 开发入口

- 开始实现、评审、提交或准备演示前，必须阅读 [参赛要求与评分标准](docs/competition-requirements.md)，并遵守其中的截止时间、PR 工作流、开发过程留痕和 codebase `v0` 基线要求。
- 产品概念与关系见 [产品概念与术语](docs/product-context.md)；调整交互前阅读 [产品决策](docs/product-decisions.md)。
- 交付时按参赛要求逐项核对源代码、运行说明、架构设计（含模块规格与分工）、demo 视频和演示环境；如实说明 mock、已实现和未验证的能力。

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
