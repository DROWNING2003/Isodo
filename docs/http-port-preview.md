# Sandbox HTTP 端口预览

远程 CHV 沙箱中的 HTTP 服务可通过 `https://<port>-<sandboxID>.<preview-domain>/` 访问。外部入口配置为 18443 时，地址包含 `:18443`；该端口与 Guest 内部端口无关。Guest 管理端口 31024、出站代理端口 31080、工作台端口 38080 不允许使用此入口。

完整的客户端脚本与排错见[开发者指南](../docs-site/content/docs/guides/http-port-preview.mdx)。

## 默认公开与主动私有

通用 HTTP 端口未配置时默认 `visibility=public`，直接打开端口 URL，不需要 Token/Cookie。只有指定为 private 时才需要 `authorization_url`。T3 工作台的授权机制保持不变。

```http
PUT /v1/sandboxes/<sandboxID>/previews/3000
X-API-Key: <key-with-sandboxes:preview>
Content-Type: application/json

{"visibility":"private"}
```

`GET` 同一路径读取策略（需要 `sandboxes:read`）；PUT `public` 恢复公开。策略绑定 Sandbox ID + 端口，Stop/Start 保留，Delete 清理。修改只允许所有者并保持会话 CSRF 检查。private 授权绑定策略 revision，切换后旧令牌失效。新请求每次读取数据库；当前 Gateway 在修改响应前取消旧流，其他实例的存量流在至多下一次 100ms 检查关闭，均不缓存过期的公开权限。

Public 限流每 Gateway、每端口 10 rps / burst 20、8 个并发连接；全局 100 rps / burst 200、256 个连接，最多保留 2048 个端口桶，空闲桶一分钟后可回收。超限返回 429 + Retry-After: 1。限流在 Node inspection/relay 前执行，SSE/WS 占用连接预算。多 Gateway 的预算分别计数。

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

默认 public 响应 HTTP 201：

```json
{"visibility":"public","url":"https://3000-<sandboxID>.drowning.top:18443/"}
```

如果该端口已经设为 private，响应另含授权 URL 和到期时间：

```json
{
  "url": "https://3000-<sandboxID>.drowning.top:18443/",
  "visibility": "private",
  "authorization_url": "https://3000-<sandboxID>.drowning.top:18443/__isodo/port-auth#<short-lived-secret>",
  "expires_at": "2026-11-02T04:00:00Z"
}
```

在浏览器打开 `authorization_url`，页面会移除 URL 片段，通过同源 POST 换取 host-only、HttpOnly Cookie，再跳转到服务首页。授权有效期固定为五分钟，过期后重新请求。HTTPS 使用 Secure Cookie，SameSite=Strict。Private 的普通 `url` 可以收藏，但地址本身不授予访问权；Public 的 URL 可以直接访问。

程序客户端可取 `authorization_url` 中的片段作为 `X-Isodo-Preview-Token` 请求头访问 `url`。这是 bearer capability，请勿分享、记录到日志或提交仓库。服务器不通过查询参数或 URL 路径接收令牌。生成的 TypeScript Public API 客户端提供 `createSandboxPreview(sandboxID, { port })`，Private 返回的 `authorization_url` 用于完成授权；Public 返回 `url`，不返回令牌或到期时间。

API Key 的读、写、命令执行权限均不能代替预览权限。预览可以调用应用的写接口，因此创建密钥时需单独勾选“允许预览沙箱 HTTP 端口”。撤销签发密钥阻止新授权；已签发的独立 bearer capability 最长存活五分钟，沙箱生命周期变化会提前撤销。

## 边界与转发

链路为 Public API → Preview Gateway → mTLS Node RelayPort → Guest PortForward → Guest 内部 `127.0.0.1:<port>`。不创建每沙箱宿主监听端口，不增加 VM 网卡，不在宿主机执行用户命令。

令牌绑定用途 `http-port`、账号、签发主体、沙箱、Node、ownership generation、revision、boot ID、Guest epoch、端口和到期时间。两种模式每次请求及 WebSocket 握手都重新查询 Control 的所属 Sandbox、策略、运行状态和 Node 身份；private 额外校验签发账号与授权；Node 再检查 journal、当前 Guest identity、VM 存活及 busy 状态。T3 授权与通用端口授权使用不同 Cookie 和签名用途，不能互换。

停止意图、删除、重新分配、revision 或 Guest 启动身份变化会使旧授权失效。持续连接同时受到期时间和 Control/Node 每 100ms 的状态检查约束；健康存储与 RPC 下，在下一次检查后关闭。存储或 Node 检查失败时也关闭连接。当前实现为每个持续请求轮询，适合当前规模；大规模使用需要用生命周期通知替代高频轮询。

代理保留外部 Host、路径、查询和 WebSocket 子协议；写入 X-Forwarded-*，移除预览令牌头、预览 Cookie 和 Isodo 会话 Cookie。上游 HTTP Set-Cookie 的 Domain 被移除，Isodo 保留 Cookie 被丢弃。应用自己的 Authorization、Cookie 和响应内容正常透传。SSE 禁用代理缓冲；WebSocket 不解析应用帧。Private 的 Cookie 写请求和浏览器 WebSocket 必须携带精确同源 Origin；其他预览子域及 Web 应用域不能借 Cookie 发起跨源访问。

Vite 应在 Guest 里运行，并允许生成的完整预览主机名，例如在 `server.allowedHosts` 中明确列入该主机。外部使用非标准 HTTPS 端口时，若 Vite 未从当前页面正确推导 HMR 地址，应将 HMR 的协议配置为 wss、clientPort 配置为外部端口。不要使用另开的宿主端口绕开授权。

## 配置与发布

复用 `ISODO_PREVIEW_SECRET`、`ISODO_PREVIEW_DOMAIN`、`ISODO_PREVIEW_SCHEME`、`ISODO_PREVIEW_PUBLIC_PORT` 和现有 Node mTLS 配置。入口需将 `<port>-<UUID>` 主机转发到 API，支持 WebSocket Upgrade，并关闭 SSE 缓冲。域名/DNS、证书及入口配置需单独准备。

启用本策略前需将独立目标数据库迁移到 0015，再更新 API；原有 Node 的完整运行身份确认链路必须保留。本次未执行生产迁移或部署。新 Node 支持 RelayOpen 的 revision/boot_id/guest_epoch 字段；Gateway 要求 Node 在校验后回传完整 fencing 确认帧，旧 Node 缺少确认时拒绝转发 HTTP 内容。部署仍需配套升级 Node/API。Lima 暂不启用通用预览；Public API 合同和 RelayOpen 字段可供后续适配复用。原有 T3 38080 路径保持独立。

历史 private 模式演示记录：2026-11-02，已在 jfcs1024 演示环境升级 Node/API 和数据库 scope 约束，部署版本 `78c513d`。已备份原配置、Node journal 和数据库；未修改 Nginx。真实账号的临时 API Key 通过公开 API 创建新 CHV 沙箱，启动 Python 3.10.12 HTTP 服务并取得预览；HTTPS 入口与真实浏览器授权跳转、页面显示均通过。此结果与下面使用内存 Store 的隔离测试分别记录。

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

数据库持久化与删除竞态可用 `scripts/test-preview-visibility-postgres.sh` 在新建专用容器中复现，不读取 `.env`。生成客户端提供 Go/TS 的 GetSandboxPortPreview、SetSandboxPortPreview；POST CreateSandboxPreview 返回 visibility，private 才附带授权字段。

本次默认 public 策略的具体测试范围、原始运行标识和限制见[2026-11-02 验证记录](current-status.md)。

Guest 内部服务现使用五位数端口 31024 / 31080 / 38080，旧 1024 / 1080 / 8080 在迁移期也继续禁止通用预览。Node、Guest 与镜像需匹配，详见[内部端口说明](runtime-service-ports.md)。
