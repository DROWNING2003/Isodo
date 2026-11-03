# Isodo 应用工程

- 实现、评审或准备交付前，阅读 [参赛要求](../docs/competition-requirements.md)。产品术语见 [CONTEXT](../docs/product-context.md)；当前客户端与云端边界以 [架构说明](docs/isodo-reuse.md) 为准。
- Web remains a client of cloud sandboxes. Electron may start and supervise the local `isodo-node` sidecar; Agent processes, workspace files, and T3 Runtime remain inside the selected sandbox. The renderer never starts Lima or local agents directly.
- `apps/server` 是在沙箱中运行的 T3 Runtime。修改它时验证相应服务端模块；客户端构建不依赖其启动或构建。
- 复用 T3 现有组件、状态与远程协议。新增 Isodo 业务放在两端的 `features/isodo`，模拟控制面放在 `packages/sandbox-client/src/cloud`；mock 明确标识，不接收真实密钥。
- 代码发现优先使用 codebase-memory-mcp：`search_graph` → `trace_path` → `get_code_snippet` → `query_graph`。尚未索引时先 `index_repository`。字符串、配置、非代码文件或图结果不足时使用 `rg`。
- 按改动范围运行类型检查与相关测试。不要默认运行全仓检查。浏览器或模拟器交互验证先取得用户明确授权。
- 当前仓库使用独立工作分支，并遵守根目录 Git 授权规则；提交前分别核对 staged / unstaged，并确认目标仓库。来源提交仅作为代码出处记录。
- 开发进程使用本工程目录；停止进程时仅操作启动时记录的 PID / session。用户已有的 Agent、T3 安装与数据库属于独立环境。
