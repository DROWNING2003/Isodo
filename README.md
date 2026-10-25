# Isodo

<p align="center"><img src="assets/brand/isodo-mascot.png" alt="Isodo 吉祥物" width="132" /></p>

**给 AI Agent 一个安全、持久、可以连接真实模型的工作空间。**

同一个 Isodo App，通过统一 Sandbox API 管理本地 Lima 和云端 Cloud Hypervisor。用户创建沙箱、授权模型连接、进入 Agent 工作台，让 Agent 执行任务，并检查文件和测试结果。真实模型密钥在沙箱外管理，Guest 配置使用占位符。

## 当前 MVP

2026-10-25 至 26 日已实测：

- App → Control → SSH/mTLS → 独立 Node → CHV → Guest Ready → Agent 工作台。
- Codex 选择 gpt-6.1-sol，实际创建 Python 文件并运行两个 unittest。
- 无网卡 CHV 通过 Vsock 出站代理完成模型请求；宿主注入计数增加，Guest 认证文件只包含占位符。
- 零字节、文本和二进制 Files 往返校验、禁止覆盖及工作区边界拒绝。
- 修复 devpts 挂载后，真实 App 终端可用，UID/GID 1000；保盘重启后任务和文件保留。
- 同一 App 创建新的 Lima 沙箱、应用 Agent 配置并连接工作台通过。本轮未据此声明完整本地模型回归已完成。

录屏主线和交付核对见 [Demo 录制指南](docs/runbooks/demo-recording.md)，架构、模块规格与分工见 [MVP 架构文档](docs-site/content/docs/concepts/architecture.mdx)。

## 运行

需要 Node.js 24.13.1、pnpm 11.10.0、Go 1.26.9、Docker Compose；本地沙箱需要 Lima，远端需要 Linux x86_64/KVM 和匹配的 Cloud Hypervisor。

```sh
make init
# 按 .env.example 配置 GitHub OAuth / Casdoor，秘密只保存在本地。
make infra-up
cd app && corepack pnpm@11.10.0 install && cd ..
go build -o isodo-node ./cmd/isodo-node
mkdir -p .tools
# Apple Silicon；Intel Mac 将 arm64 改为 amd64。
GOOS=linux GOARCH=arm64 go build -o .tools/isodo-guest-linux-arm64 ./cmd/isodo-guest
make t3-app-bundle
```

分别启动 Control、Web 和桌面端：

```sh
make api
make web
make electron
```

默认 App Web 地址 `http://127.0.0.1:5734`，Control 地址 `http://127.0.0.1:8080`。Electron 自己启动本地 Lima sidecar；独立 `make node-local-t3` 用于 Control 访问 Lima 的开发路径，不要为同一 journal 重复启动 Node。

双环境云端演示需要 Control 配置远端 Node 的 mTLS 与 SSH 转发，并发布 Ready 模板；不要将 Node RPC 地址作为 App API 地址。部署说明见 [云端运行手册](docs/runbooks/cloud-t3.md)、[基础镜像](images/base/README.md) 和 [Docker SeaweedFS](deploy/seaweedfs/remote-demo.md)。

## 本机 Preview 配置

App 与 Preview 使用同站点本地子域名，建立 HttpOnly Cookie 后配对。设置以下环境，并在 Casdoor 保留或增加精确回调 `http://app.isodo.localhost:5734/auth/callback`：

```dotenv
# Control
ISODO_WEB_ORIGIN=http://app.isodo.localhost:5734
ISODO_PREVIEW_DOMAIN=isodo.localhost
ISODO_PREVIEW_SCHEME=http
# ISODO_PREVIEW_SECRET 使用本地随机秘密，不提交 Git。

# Web 进程
VITE_ISODO_PREVIEW_DOMAIN=isodo.localhost

# Electron 进程
ISODO_WEB_URL=http://app.isodo.localhost:5734
```

Vite 仍监听 `127.0.0.1:5734`。以该 origin 打开 App 并登录。Guest 模型出站使用 Vsock，当前无网卡 CHV 演示不需要开启 bridge/TAP 或修改宿主防火墙。

## 架构

```text
Isodo App / SandboxClient
  ├─ Electron IPC → 本机 Node → Lima → Guest / Agent 工作台
  └─ HTTP → Control / PostgreSQL outbox
                → SSH + mTLS → 远端 Node → CHV / Vsock → Guest

App → Preview Gateway → Node Relay → Agent 工作台
Guest → Vsock 出站代理 / 宿主凭证注入 → 真实模型
Node → SeaweedFS S3 → 校验、缓存不可变模板工件
```

Control 管理账号、归属、模板来源与生命周期意图；Node journal 和运行身份约束实际 VM；Guest 提供受限工作区文件与进程能力。工作区、任务和 Agent 配置保存在实例持久盘。完整模块规格与维护分工见 [架构文档](docs-site/content/docs/concepts/architecture.mdx)。

## 验证与文档

```sh
go test ./internal/control/sandbox ./internal/control/preview ./internal/control/auth ./internal/node/localapi ./internal/node/orchestrator
npm ci --prefix docs-site
npm --prefix docs-site run generate:openapi
make docs-build
make docs
```

文档站入口为 `http://127.0.0.1:3000/docs`。公开 HTTP 契约在 `api/openapi`，Node/Guest gRPC 在 `api/proto`；完整生成入口是 `make generate`。生成文件不手改。

- [产品术语](docs/product-context.md) · [产品决策](docs/product-decisions.md)
- [MVP 验收记录](docs/validation/unified-agent-demo-20261026.md)
- [统一 API 规格](specs/unified-sandbox-api.md) · [目录职责](specs/directory-responsibilities.md)
- [安全连接](docs/secure-connections.md) · [文档站开发](docs-site/README.md)
- [参赛要求与交付核对](docs/competition-requirements.md)

## 范围与交付

公开 SDK、App 快照流转、Skill/MCP 一键接入及商业套餐仍不属于本次已验收的演示能力。完整跨节点恢复和生产安全验收也不在 MVP 完成声明内。

源代码、运行说明、架构与模块分工、真实过程记录和 demo 视频均需上传 GitHub。当前录屏仍需实际录制并上传；本地修改不等于已上传。现有 `v0` tag 保持指向 `05d0b8c`，后续变更按 PR 工作流提交。截止材料记为 2026-10-26 09:00，赛方时区仍需确认，截止后停止修改参赛仓库。
