# isodo 开发入口

- 开始实现、评审、提交或准备演示前，必须阅读 [参赛要求与评分标准](docs/competition-requirements.md)，并遵守其中的截止时间、PR 工作流、开发过程留痕和 codebase `v0` 基线要求。
- 产品概念与关系见 [产品概念与术语](docs/product-context.md)；调整交互前阅读 [产品决策](docs/product-decisions.md)。
- 交付时按参赛要求逐项核对源代码、运行说明、架构设计（含模块规格与分工）、demo 视频和演示环境；如实说明 mock、已实现和未验证的能力。

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
