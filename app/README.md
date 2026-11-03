# Isodo 客户端与 Agent 工作台

本目录包含基于 T3 Code 的 Isodo Web、Electron 和沙箱工作台实现。完整运行说明与当前能力以[仓库 README](../README.md)和[验收状态](../docs/current-status.md)为准。

Web 连接远程 CHV，Electron 还可启动本地 Lima Node。Agent、工作目录、终端和 T3 Runtime 在选中的 VM 内运行；Renderer 不直接运行宿主命令。

使用 Node 24 与 pnpm 11.10.0，在本目录安装依赖：

```sh
pnpm install --frozen-lockfile
```

从仓库根目录使用 `make web`、`make electron` 启动对应入口；先按根 README 准备 Control、Node、Guest 和工作台制品。开发服务器并不等于可用的 VM 运行时。

市场 MCP/Skill 当前优先远程 CHV，不能将远程验收自动算作本地或移动端验收。Mock transport 仅用于明确标记的开发测试，不代表实际部署行为。

工作台来源、组件及内部协议文档保留在本目录，具体贡献和许可证以源码、LICENSE 与 Git 历史为准。
