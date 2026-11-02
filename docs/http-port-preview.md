# Sandbox HTTP 端口预览

远程 CHV 沙箱中的 HTTP 服务可通过 `https://<port>-<sandboxID>.<preview-domain>/` 访问。外部入口配置为 18443 时，地址包含 `:18443`；该端口与 Guest 内部端口无关。Guest 管理端口 1024、工作台端口 8080 不允许使用此入口。

完整的客户端脚本与排错见[开发者指南](../docs-site/content/docs/guides/http-port-preview.mdx)。

## 使用

在运行中的沙箱内启动服务，例如：

```sh
python3 -m http.server 3000 --bind 127.0.0.1
```

使用沙箱所属账号的会话、Bearer token，或明确授予 `sandboxes:preview` 的 API Key 请求：

```http
POST /v1/sandboxes/<sandboxID>/previews
Content-Type: application/json
X-API-Key: <scoped-key>

{"port":3000}
```

响应为 HTTP 201：

```json
{
  "url": "https://3000-<sandboxID>.drowning.top:18443/",
  "authorization_url": "https://3000-<sandboxID>.drowning.top:18443/__isodo/port-auth#<short-lived-secret>",
  "expires_at": "2026-11-02T04:00:00Z"
}
```

在浏览器打开 `authorization_url`，页面会移除 URL 片段，通过同源 POST 换取 host-only、HttpOnly Cookie，再跳转到服务首页。授权有效期固定为五分钟，过期后重新请求。HTTPS 使用 Secure Cookie，SameSite=Strict。普通 `url` 可以收藏，但地址本身不授予访问权。

程序客户端可取 `authorization_url` 中的片段作为 `X-Isodo-Preview-Token` 请求头访问 `url`。这是 bearer capability，请勿分享、记录到日志或提交仓库。服务器不通过查询参数或 URL 路径接收令牌。生成的 TypeScript Public API 客户端提供 `createSandboxPreview(sandboxID, { port })`，返回的 `authorization_url` 才能完成授权。

API Key 的读、写、命令执行权限均不能代替预览权限。预览可以调用应用的写接口，因此创建密钥时需单独勾选“允许预览沙箱 HTTP 端口”。撤销签发密钥阻止新授权；已签发的独立 bearer capability 最长存活五分钟，沙箱生命周期变化会提前撤销。

## 边界与转发

链路为 Public API → Preview Gateway → mTLS Node RelayPort → Guest PortForward → Guest 内部 `127.0.0.1:<port>`。不创建每沙箱宿主监听端口，不增加 VM 网卡，不在宿主机执行用户命令。

令牌绑定用途 `http-port`、账号、签发主体、沙箱、Node、ownership generation、revision、boot ID、Guest epoch、端口和到期时间。每次请求及 WebSocket 握手都重新查询 Control 的租户所有权、运行状态和 Node 身份；Node 再检查 journal、当前 Guest identity、VM 存活及 busy 状态。T3 授权与通用端口授权使用不同 Cookie 和签名用途，不能互换。

停止意图、删除、重新分配、revision 或 Guest 启动身份变化会使旧授权失效。持续连接同时受到期时间和 Control/Node 每 100ms 的状态检查约束；健康存储与 RPC 下，在下一次检查后关闭。存储或 Node 检查失败时也关闭连接。当前实现为每个持续请求轮询，适合当前规模；大规模使用需要用生命周期通知替代高频轮询。

代理保留外部 Host、路径、查询和 WebSocket 子协议；写入 X-Forwarded-*，移除预览令牌头、预览 Cookie 和 Isodo 会话 Cookie。上游 HTTP Set-Cookie 的 Domain 被移除，Isodo 保留 Cookie 被丢弃。应用自己的 Authorization、Cookie 和响应内容正常透传。SSE 禁用代理缓冲；WebSocket 不解析应用帧。Cookie 写请求和浏览器 WebSocket 必须携带精确同源 Origin；其他预览子域及 Web 应用域不能借 Cookie 发起跨源访问。

Vite 应在 Guest 里运行，并允许生成的完整预览主机名，例如在 `server.allowedHosts` 中明确列入该主机。外部使用非标准 HTTPS 端口时，若 Vite 未从当前页面正确推导 HMR 地址，应将 HMR 的协议配置为 wss、clientPort 配置为外部端口。不要使用另开的宿主端口绕开授权。

## 配置与发布

复用 `ISODO_PREVIEW_SECRET`、`ISODO_PREVIEW_DOMAIN`、`ISODO_PREVIEW_SCHEME`、`ISODO_PREVIEW_PUBLIC_PORT` 和现有 Node mTLS 配置。入口需将 `<port>-<UUID>` 主机转发到 API，支持 WebSocket Upgrade，并关闭 SSE 缓冲。域名/DNS、证书及入口配置需单独准备。

先升级数据库到 migration 0014，再部署匹配的新 Node 和 API。新 Node 支持 RelayOpen 的 revision/boot_id/guest_epoch 字段；Gateway 要求 Node 在校验后回传完整 fencing 确认帧，旧 Node 缺少确认时拒绝转发 HTTP 内容。部署仍需配套升级 Node/API。Lima 暂不启用通用预览；Public API 合同和 RelayOpen 字段可供后续适配复用。原有 T3 8080 路径保持独立。

2026-11-02，已在 jfcs1024 演示环境升级 Node/API 和数据库 scope 约束，部署版本 `78c513d`。已备份原配置、Node journal 和数据库；未修改 Nginx。真实账号的临时 API Key 通过公开 API 创建新 CHV 沙箱，启动 Python 3.10.12 HTTP 服务并取得预览；HTTPS 入口与真实浏览器授权跳转、页面显示均通过。此结果与下面使用内存 Store 的隔离测试分别记录。

## 验证

```sh
go test ./...
go test -race ./internal/control/preview ./internal/control/sandbox ./internal/control/auth ./internal/control/orchestrator ./internal/node/orchestrator
make generate-check
```

Linux amd64/KVM 上运行真实链路测试：

```sh
ISODO_CHV_BINARY=/path/to/cloud-hypervisor \
ISODO_GUEST_KERNEL=/path/to/kernel \
ISODO_GUEST_MODULES=/path/to/matching/vsock/modules \
  scripts/test-http-port-preview-chv.sh
```

脚本构建当前 Guest 和仅绑定 Guest loopback 的 HTTP 测试程序，并启动一台无 NIC 的独立 VM。测试覆盖 Public API 签发、真实 mTLS Node 客户端/服务端、Guest PortForward、3000/5173/8000 GET/POST、SSE flush、WebSocket 帧、活跃连接撤销、真实 VM shutdown/delete/reboot 后旧授权拒绝。Control 账号及生命周期 Store 使用内存夹具；不经过生产数据库、worker、Nginx、DNS 或浏览器 UI。PostgreSQL 权限存储另有隔离集成测试。

隔离测试运行 HTTP/WebSocket 夹具，不运行 Python、Vite 构建器或 Electron。真实 Python 与浏览器授权已在上述演示部署中补测；完整 Vite 页面 HMR、Electron 及 worker 驱动的 Stop/Start/Delete 全部组合仍未验证。
