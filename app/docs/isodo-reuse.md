# Isodo 客户端复用与运行边界

Web、Electron 复用 T3 的 React 界面、工作台协议、编辑器、Markdown 和任务组件。Isodo 新增沙箱生命周期、账号与凭证管理、MCP / Skill 市场、授权及运行时适配。

Web 面向远程 CHV；Electron 主进程管理本地 Node sidecar，可运行 Lima 沙箱，并通过受控 IPC 提供本地文件与生命周期操作。Renderer 不直接启动 Lima、宿主命令或 Agent。

Agent 与 T3 Runtime 在所选 VM 内执行。Control 负责认证与资源所有权，Node 校验运行时身份并转发至 Guest。工作台通过经过授权的应用链接连接，不将普通开发 Mock 当作真实运行。

模型长期凭证由沙箱外代理注入；远程 MCP 的第三方 Token 留在 Control Broker。Skill 是用户选择后同步的可读指令文件，不能存放密钥或作为额外权限凭证。

当前真实验证与本地/远程能力差异见[能力状态](../../docs/current-status.md)，架构与模块见[架构说明](../../docs-site/content/docs/concepts/architecture.mdx)。移动端、任意模型兼容和正式签名发行不由桌面源码验收自动覆盖。
