# 交接：沙箱工作台接线现状（2026-10-25 03:30）

本文件描述"打开沙箱后界面不对"这一遗留问题的完整现状，供接手者快速定位。
不重复产品设计，只写实现事实、已验证行为、失败尝试和可执行的下一步。

## 1. 运行形态

同一台机器上有两个 Node 运行时，不要混淆：

| 运行时 | 启动方式 | 监听 | 状态目录 |
|---|---|---|---|
| 控制面 Node | `make node-local-t3` | 0.0.0.0:9090（mTLS gRPC） | Go `UserConfigDir`/Isodo/node |
| 桌面 sidecar | Electron 主进程自动拉起 | 127.0.0.1:随机端口（HTTP） | `app.getPath("userData")/node` |

- `isodo-api`（8080）通过 gRPC 连前者，服务云端沙箱（`/v1/sandboxes`）。
- Electron 通过 HTTP + 随机 bearer token 连后者，服务本地沙箱。
- sidecar 以 `ISODO_LOCAL_ONLY=true` 启动，不监听 9090、不要求 mTLS。

开发启动：

```bash
# 1 基础设施
make infra-up
# 2 API（issuer 从 .env 的 CASDOOR_ORIGIN 读，见 Makefile:API_CASDOOR_ISSUER）
make api
# 3 控制面 Node（云端沙箱必需；否则 API 每 10s 报 node unavailable）
make node-local-t3
# 4 Web（**必须** full surface，见下）
make web
# 5 Electron（脚本会先重编 isodo-node）
cd app && pnpm dev:desktop
```

两个关键构建期变量：

- `VITE_BOUNDLY_WEB_SURFACE=full`：`app/apps/web/src/webSurface.ts` 默认返回 `"sandbox"`（沙箱内运行形态）。
  只有显式 `full` 才会启用 `/` 聊天首页，否则所有聊天路由被重定向回 `/sandboxes`。
  `make web` 已带此变量；脱离 Makefile 手动跑 dev 时必须自己加。
- `ISODO_WEB_URL`（默认 `http://127.0.0.1:5734`）：桌面窗口加载的开发地址，必须与
  `.env` 的 `ISODO_WEB_ORIGIN` 同源（`127.0.0.1`，不要用 `localhost`，否则 cookie 域不同）。

## 2. 已验证可用的部分

以下是我用真实 sidecar + Lima 跑通、有实际输出的部分：

- 本地沙箱创建：`POST /sandboxes` → 操作 27s 完成 → `Lima Guest Agent is ready`。
- T3 自动启动：取 app link 前先探活，不通则拉起；页面 `HTTP 200` 且含 `id="root"`。
- T3 自愈：guest 内杀掉 T3 后再取 app link，能重新起来并 200。
- 删除：`DELETE /sandboxes/{id}` → `204` → 列表清空。
- 本地沙箱列表：`GET /sandboxes`（索引持久化在 `userData/node/sandboxes.json`，用 `InspectSandbox` 取真实状态，`deleted` 自动剔除）。
- 本地模板：`GET /templates`、`POST /templates/{id}/build`，内置 base + Python，可通过 `ISODO_LOCAL_TEMPLATES_FILE` 扩展。
- 创建弹窗：运行位置（本地/云端）+ 模板选择 + 名称，错误信息已剥离 Electron IPC 前缀。
- 桌面窗口：无标题栏（`titleBarStyle: "hidden"`）、红绿灯内嵌、isodo 图标与 bundle 名。

## 3. 遗留问题（核心）

### 3.1 打开沙箱后，左侧边栏不是沙箱形态

**现象**：进入会话后，侧栏可能是
(a) isodo 列表形态（创建沙箱 / 我的沙箱 / 凭证库），或
(b) 上游 T3 形态（Search threads or PRs / All projects / No threads yet）。
期望是原型的沙箱形态（沙箱卡片 / 新任务 / 最近任务 / 文件·安全连接·快照·安全）。

**已改但未生效的四处尝试**（都在 `app/apps/web/src`）：

1. `features/isodo/CloudWorkspace.tsx`：用 mock 存储判断本地沙箱 —— 真实沙箱不在其中，失败。
2. 同上：用 `useLocalRuntimeSandbox`（问桌面运行时）—— 修正了"沙箱不存在"，但没解决侧栏。
3. `features/isodo/CloudSidebar.tsx`：从环境 `displayUrl` 解析沙箱 id —— 命中不稳定。
4. 同上 + `components/AppSidebarLayout.tsx`：用 `useActiveEnvironmentId()` → URL 路径 →
   `localStorage` 映射（`features/isodo/sandboxEnvironmentMap.ts`，配对时写入）→ 单沙箱兜底。
   目前 `AppSidebarLayout` 已改为 `const isIsodoRoute = true;`（设置页除外），
   即**任何路由都用 isodo 侧栏**；侧栏内部再决定列表/沙箱形态。

**关键未知**：会话路由下 `pathname`、`useActiveEnvironmentId()`、环境 `displayUrl` 三者的实际值。
我改了三轮都靠猜，缺一次实测。

**最快的下一步**：在 `CloudSidebar` 里临时把这三个值渲染到侧栏底部（仅 `import.meta.env.DEV`），
刷新一次即可确定映射为何没命中；或者用 DevTools 执行：

```js
location.pathname
JSON.parse(localStorage.getItem("isodo.sandbox-environments") ?? "{}")
```

### 3.2 打开沙箱瞬间白屏

**现象**：点击打开后空白，手动刷新即正常。

**已定位的代码事实**：`app/apps/web/src/routes/_chat.index.tsx` 的 `IndexDraftLanding` 有两处 `return null`：

```tsx
if (!bootstrapped) return null;               // 环境 shell 未 bootstrap
if (mostRecentProject !== null) return null;  // 正在自动建会话并跳转
```

即"环境目录未收录 / shell 未 bootstrap / 路由登录门禁未就绪"三者任一未完成，`/` 就是纯白。

**已做的缓解**：`CloudWorkspace.connect()` 配对成功后等环境进入目录（最多 10s），然后
`window.location.replace("/")` 整页进入 —— 等价于手动刷新。是否彻底解决尚未确认。

### 3.3 主区域仍是上游工作台

顶部有 `+ Add action` / `Initialize Git` / `Open`，标题是 `Boundly Sandbox / New thread`。
这部分在 `ChatView` 外层，未被替换。用户曾表示"这样也可以"，因此未改。

### 3.4 环境名 "Boundly Sandbox"

这不是界面文案，而是**配对时写进 T3 环境配置的环境名**。源码里的 `Boundly` 已全部改为 `isodo`
（24 个文件），但已存在的环境仍保留旧名。需要改配对入口（`connectPairing` / `registerPairing`
是否接受 label）或迁移已存环境配置。

### 3.5 四个工作台面板未实现

`文件 / 安全连接 / 快照 / 安全` 现在只是链接，跳到 `/sandboxes/{id}?pane=xxx` 后会重新连接并进入会话，
没有实际面板。文件面板最容易落地：Guest Agent 已有 `ListDirectory` / `ReadFile` RPC。

## 4. 相关文件

```
根仓库
  internal/node/localapi/server.go        本地 HTTP API（sandboxes/templates/operations）
  internal/node/localapi/sandboxes.go      沙箱索引（id/name/revision，持久化）
  internal/node/localapi/templates.go      模板目录 + 构建
  internal/node/orchestrator/t3.go         T3 启动、自愈（ensureT3Serving）、EXECUTION_EXPIRED 重试
  internal/node/lima/base.go               base provisioning（就绪前校验 guest agent 存活）
  internal/node/lima/templates.go          TemplateBuilt
  cmd/isodo-node/main.go                   local-only 模式、模板 builder 注入
  Makefile                                 API_CASDOOR_ISSUER 从 .env 读；web 带 full surface
app/
  apps/desktop/src/main.ts                 sidecar 生命周期、IPC、窗口外观、bundle 名
  apps/desktop/src/preload.ts              受限桥（本地沙箱/模板/操作）
  apps/web/src/features/isodo/CloudWorkspace.tsx   沙箱入口 + 配对
  apps/web/src/features/isodo/CloudSidebar.tsx     侧栏（列表/沙箱两形态）
  apps/web/src/features/isodo/CloudPages.tsx       沙箱列表（合并本地+云端、删除）
  apps/web/src/features/isodo/CreateSandboxDialog.tsx  创建弹窗
  apps/web/src/features/isodo/usePlatformSandbox.ts    平台/本地沙箱解析 + app link
  apps/web/src/features/isodo/sandboxEnvironmentMap.ts environmentId → sandboxId
  apps/web/src/components/AppSidebarLayout.tsx     外壳选择（现恒为 isodo）
  apps/web/src/webSurface.ts                       full/sandbox 形态
```

## 5. 已知环境约束

- 磁盘只剩约 2Gi，Lima 实例是 APFS 写时复制，但沙箱内写入会实占空间。
- Casdoor 走 ngrok，域名变化会同时影响 `.env` 的 `CASDOOR_ORIGIN` 与 GitHub OAuth 回调。
- Electron 有自己的 cookie 容器，浏览器里登录不会共享到桌面窗口。

## 6. 后续修复与验证（2026-10-25）

- 侧栏通过路由匹配和草稿记录确定环境，Hook 无条件调用，并读取真实本地沙箱。
- 连接保留在应用运行期间；返回列表及切换沙箱不移除配对环境。列表提供“连接 / 进入 / 断开连接”，再次进入恢复上次任务；显式断开只关闭传输，保留配对及任务。
- 列表、侧栏与工作台使用共享 TanStack Query 缓存；返回列表先显示缓存，后台刷新不清空旧数据，重复请求合并，增删及启停更新缓存。
- 文件索引失败的已验证原因：Lima 解开外层运行时包后留下 `libfff_c.so.gz`，fff-node 需要未压缩的 `libfff_c.so`。Lima 启动解包脚本现兼容压缩库，后续打包直接携带未压缩库；现有运行时包及两个本地 Guest 的库已修复。
- 文件验证：两个 Guest 的真实索引扫描、测试文件查找和读取均通过；桌面文件面板已列出验证文件并在预览中读回内容。初次失败的索引缓存通过正常文件写入协议失效；没有重启沙箱，验证文件及临时诊断代码已清理。
- 校验：前端相关 28 项测试通过，Web 类型检查通过；Go 解包测试覆盖 ARM64 / x64 压缩库及未压缩库。镜像未重新构建。
