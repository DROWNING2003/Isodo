# Isodo

基于 T3 Code 的云沙箱客户端。Web 与 Electron 复用 T3 的 React、TanStack Router、侧栏、编辑器、Markdown 和 UI 组件；移动端沿用 Expo、React Native、原生导航与编辑器。

Agent、工作目录、命令和 T3 Runtime 运行在云端沙箱中。客户端负责沙箱选择、任务交互和服务授权。

## 本地查看

Node 24、pnpm 11.10.0：

```sh
pnpm install --frozen-lockfile
pnpm dev
```

打开 [Web](http://localhost:5733/sandboxes)。开发命令只启动静态 Web 客户端，默认使用 mock 数据，无需本机 T3 服务、Agent CLI 或 API Key。

可体验：创建与休眠云沙箱、选择 Agent、发送任务、审批写入、查看文件、模拟终端、逐沙箱授权／撤权、创建快照并恢复为独立沙箱。刷新页面会重置 mock 数据。界面持续显示 Mock；终端没有真实命令输入。

## 桌面与移动

```sh
pnpm build:desktop       # Web 与 Electron 客户端
pnpm dev:desktop         # 另一个终端保持 pnpm dev 运行
pnpm dev:mobile          # Expo；使用配套原生开发客户端
```

桌面主进程仅管理窗口，preload 只标记桌面表面；不提供本机进程、文件系统、SSH、WSL 或 Agent 接口。移动端的 Home 进入云沙箱 mock；原生应用编译和真机流程仍需本地开发工具。

## 沙箱 Runtime

`apps/server` 保留 T3 Runtime 及 Agent 适配器，供后续部署到沙箱。它与客户端有明确的构建和运行边界：

```sh
pnpm build:sandbox-runtime
```

未来接入流程为：云端控制面授权并准备沙箱 → 返回沙箱内 T3 的地址及短期连接凭证 → 客户端使用原 T3 的远程认证、WebSocket 和 RPC。当前 mock 不调用真实控制面，也不进行该连接。

模块对应与验证范围见 [架构说明](docs/isodo-reuse.md)。参赛交付要求见 [competition-requirements](../prototype/docs/competition-requirements.md)。

## 来源

来源 `DROWNING2003/fork.t3code` 的 `0a26a291a234e4bcfefc7ad9ce6ffd872052106c`。保留 MIT `LICENSE` 与第三方许可机制。

当前目录保留提取后的源代码，不包含来源仓库的 Git 元数据或 GitHub 配置。
