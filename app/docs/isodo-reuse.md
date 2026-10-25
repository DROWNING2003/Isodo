# 云端沙箱客户端边界

Web、Electron 与 Expo 负责云沙箱控制面和任务交互。Agent、工作目录、命令与 T3 Runtime 都属于云端沙箱。`apps/server` 保留为沙箱侧运行时，根开发命令和客户端构建不启动或打包它。

## 复用与裁剪

Web 保留 T3 的 AppSidebarLayout、Base UI、ScrollArea、Lexical ComposerPromptEditor 和 ChatMarkdown；Expo 保留原生导航、AppText、AppSymbol、ComposerEditor 和尺寸规则。新增 Isodo 页面位于两端的 features/isodo。

Electron 删除原 backend pool、本机 T3 启动、local auth、SSH／WSL、host 截图、浏览器自动化与 preload IPC。主进程仅创建隔离的窗口，preload 仅暴露桌面标记。客户端产物只有 main.cjs 和 preload.cjs。旧静态 t3-server-dist.bundle 已从 Web public 移除；沙箱 Runtime 构建输出改为 artifacts/sandbox，与客户端静态资源分离。

Web 删除本机平台发现、拓扑轮询、桌面 bearer bootstrap 与 SSH gateway 执行。mock 页面不启动持久连接恢复；旧 draft、welcome、项目入口返回云沙箱列表。保留的 desktopLocal 仅识别历史连接记录，hook 返回稳定空列表，不读取本机拓扑。

共享协议和原 T3 远程任务页面仍供实际 Runtime 接入。未来链路是：控制面授权并准备沙箱 → 返回沙箱内 T3 地址及短期凭证 → 客户端沿用远程认证、WebSocket 和 RPC。当前 mock 未与真实控制面串接。

## Mock 语义

两端共用 packages/sandbox-client/src/cloud 模型，各自在应用会话内持有状态，刷新后重置。

- 仅创建云端沙箱；休眠时无法创建任务。
- 监督执行需要批准后才生成产出；拒绝不修改文件。Agent 和权限选择均为模拟。
- 连接仅保存账号服务引用，不接收真实密钥。逐沙箱撤权不影响其他沙箱。
- 快照复制文件与资源配置；恢复创建独立沙箱，连接引用为空，不含运行内存。
- 终端仅显示模拟输出，不提供真实 shell 输入。

真实接入还需补齐异步控制面请求、失败与重连处理、凭证代理、快照传输和商业套餐。当前演示不能视为这些能力已实现。

## 来源适配

来源提交见根 README。初次提取修复了 expo-modules-jsi.patch 的 diff 格式与 lockfile 校验值，未改变补丁替换语义；另修正来源移动设置注册／旧主题引用，并补齐 Web 测试 fixture。

## 验证边界

已通过 3 个 mock 模型测试、33 个路由／静态客户端测试、Web／Electron／移动端及共享模型的类型检查、Web 与 Electron 客户端构建，以及本轮新增与改动模块的定向 lint。浏览器已通过任务审批、产出文件、撤权、快照恢复、四档尺寸与模拟终端检查，并确认未向本机 T3 发出 HTTP／WebSocket 请求；原生 iOS／Android、Electron 启动、真实云沙箱和 Agent E2E 未验证。

当前源代码保留在本地，已移除来源仓库的 Git 元数据与 GitHub 配置。

## 安全连接（2026-10-25）

- 账号级持久注入规则由 Isodo API 的 `/v1/injection-rules` 管理，密钥 AES-256-GCM 加密，列表和卡片只显示脱敏提示。
- 沙箱内的“安全连接”页面位于 `/sandbox-connections/$sandboxId`，从桌面 sidecar 读取脱敏规则、上传规则集合并展示注入统计。
- 本地 Lima 沙箱的真实注入由宿主侧 per-sandbox HTTP CONNECT 代理完成；Guest 只接收代理地址和 CA 证书，T3/终端继承 `HTTPS_PROXY` 与自定义 CA 环境。
- 单次规则保存在 sidecar 的加密状态中；持久规则引用只保存当前密钥快照，页面加载时会从账号 API 刷新材料。
- 云端 CHV 沙箱尚未接入注入代理，页面会显示明确提示。
