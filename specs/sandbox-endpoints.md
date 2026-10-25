# Sandbox 应用链接与端口预览契约

状态：2026-10-25，契约草案。已落地 `POST /v1/sandboxes/{sandboxID}/app-link` 的 JSON 响应、Lima 的 `http://8080-<sandboxID>.localhost:<lima-port>` 主机形状、CHV Node 侧的 loopback relay + Vsock PortForward + T3 pairing，以及 API 内的 signed-cookie Preview Gateway（`ISODO_PREVIEW_DOMAIN`/`ISODO_PREVIEW_SECRET` 启用；CHV 通过 Node `RelayPort`，Lima 仍走同宿主 direct target）。`t3-base-005`（4 GiB rootfs，含 virtio-net）已在 jfcs1024 的真实 CHV 上通过 `probe-t3`（T3 HTTP 200、pairing credential 含 `terminal:operate`）、`probe-node-t3`（真实 `CHVServer.ApplyOperation` → `CreateSandboxAppLink` → relay → T3 HTTP 200/pairing token → stop/delete）和 `probe-api-t3`（真实 `sandbox.Handler` + `preview.Publisher` + `httpapi`：`POST /app-link` → signed cookie → Preview Gateway → T3 200 → capabilities `["terminal","files"]` → stop/delete）；`t3-base-002` 另完成双 VM `probe-bound`。生命周期还验证了 Stop 保盘与 Start 冷启动（新 boot ID/guest epoch，T3 重新 200）；CHV 出网验证 `1.1.1.1` 301、`www.cloudflare.com` 200。Registry 已在 jfcs1024 的临时 SeaweedFS/PostgreSQL 上发布 `t3-base-005` 并通过 `probe-node` 从 S3 拉取；isodo-test 的 8G 发布尝试因主机 I/O 未计为成功。多宿主 Node `RelayPort`、公开 `/start`/`/stop`、CHV 出网已实现；`POST /preview`、generation、Connection Gateway 凭证注入仍未实现。本文固定公开形状和未实现边界，避免在实现中再造一套 URL/授权语义。

## 1. 结论

- 公开 API 只返回**不透明 URL**，客户端不拼域名、不解析端口、不直连 Node。
- URL 形状采用 E2B/T3 已有的 `<port>-<sandboxID>.<domain>`；本地当前用 `<port>-<sandboxID>.localhost:<lima-app-port>`，后续收敛为单一 gateway 端口。每端口独立 origin，避免 T3 cookie 跨 Sandbox 串用。
- 授权分两层：Isodo session/API key 只用于**签发**链接；链接本身是短期 bearer（T3 pairing token 在 fragment，通用 preview token 首跳换 host-scoped cookie）。
- `generation` 是不透明运行时版本，Stop/Start/Restore 后变化；旧链接必须失效。
- 不引入 envd，不做 E2B wire 兼容；只借鉴 E2B 的 URL 形状、Cocoon 的签名 preview token 模型。
- PortForward 是数据面能力，不是 Node 生命周期 RPC；Node 只负责把 Guest TCP 端口桥到内部 target，公开入口由 API/Preview Gateway 负责。

## 2. 接口

### 2.1 `GET /v1/sandboxes/{sandboxID}/app`

浏览器便捷入口：签发 T3 pairing 链接并 `303` 跳转。保留现有行为。T3 pairing credential 放在 URL fragment，不进入查询参数、日志或 Referer。

### 2.2 `POST /v1/sandboxes/{sandboxID}/app-link`

桌面端/SDK 入口，返回结构化链接，不跳转。

```json
{
  "sandbox_id": "…",
  "service": "t3",
  "url": "http://8080-<sandboxID>.localhost:43123/",
  "pairing_url": "http://8080-<sandboxID>.localhost:43123/pair#token=…",
  "status": "ready",
  "expires_at": "2026-10-25T01:30:00Z",
  "generation": "…",
  "capabilities": ["terminal", "files"]
}
```

- `url` 是应用 base，客户端可自行拼接同源路径；`pairing_url` 是可直接打开的完整链接。
- `generation` 可选；未接运行时版本时省略，客户端不得推断。
- 仅允许交互式 session/Bearer；API key 返回 403。跨租户与不存在都返回 404。
- Sandbox 未运行返回 409；后端不支持返回 501；Node 返回非法链接返回 502；应用服务未配置/暂不可用返回 503。

### 2.3 `POST /v1/sandboxes/{sandboxID}/preview`（未实现）

通用端口预览，沿用 Cocoon 的模型：

```json
{"port": 3000, "ttl_seconds": 600}
```

返回 `{url, port, expires_at, generation}`。任意 Guest HTTP 端口，但必须由控制面签发；relay 不接受客户端直接指定端口。

### 2.4 `GET /v1/sandboxes/{sandboxID}/ports/{port}`（未实现）

raw TCP Upgrade，供 CLI/本地 `port-forward` 使用；与 preview 共用路由/授权，不暴露 Node 地址。

## 3. URL 与路由

```text
Browser / Desktop
  https://<port>-<sandboxID>.<preview-domain>/
    → Preview Gateway (TLS, Host parse, generation check)
    → API/Node relay
    → CHV Vsock PortForward(port)
    → Guest 127.0.0.1:<port>
```

- Host 最左 label 为 `<port>-<sandboxID>`；端口必须在 1–65535。
- 本地 Lima 现在返回 `<port>-<sandboxID>.localhost:<lima-app-port>`；后续改成一个 gateway 端口按 Host 路由，Lima 静态转发只作内部 target，不把宿主端口暴露给客户端。
- 云端需要 wildcard DNS/TLS（`*.<preview-domain>`，`<port>-<sandboxID>` 是单层 label）。没有 wildcard 时只能退化为同源路径代理，必须记录 T3 cookie 串用风险。
- Gateway 转发前删除 Isodo 自己的 `Cookie`/`Authorization`；T3 app 走独立 origin，保留 T3 自己的 cookie。通用 preview 删除全部浏览器凭证，避免 Isodo session 泄露给 Guest 代码。

## 4. 授权与 generation

- `app-link`/`preview` 的调用者必须是 Sandbox owner（session/Bearer），API key 不签发应用链接。
- Node 只接受 Control 的 mTLS 调用；客户端不能提供宿主路径、CID、Guest 端口或 Node 地址。
- T3 pairing token 由 Guest 内 T3 CLI 生成，TTL 5 分钟，放 fragment。
- 通用 preview token claims：`{sandbox_id, ownership_generation, boot_id, guest_epoch, port, exp, nonce}`，HMAC 签名；首跳 `/_isodo/auth?token=…&next=…` 换成 host-scoped HttpOnly cookie，再 302 到 clean path。token 不留在地址栏/日志。
- `generation` 由 `ownership_generation + boot_id + guest_epoch` 派生；Gateway 每次请求校验当前实例。Stop/Delete/Replace 后旧链接返回 410/403，已建立的 WebSocket 断开。
- TTL 取请求值、Sandbox 剩余租约和 pairing TTL 的最小值；默认 5 分钟（T3）/10 分钟（preview）。

### 4.1 启用 Preview Gateway

`ISODO_PREVIEW_DOMAIN` 为空时 Gateway 关闭，API 返回 Node-local 链接（只适合同宿主浏览器）。启用后 `ISODO_PREVIEW_SECRET` 必填：

```sh
# 本地：API 与 Node 同机，浏览器解析 *.localhost
ISODO_PREVIEW_DOMAIN=localhost
ISODO_PREVIEW_SECRET=$(openssl rand -hex 32)
ISODO_PREVIEW_SCHEME=http

# 云端：*.sandbox.example.com 的 wildcard DNS/TLS 指向 API
ISODO_PREVIEW_DOMAIN=sandbox.example.com
ISODO_PREVIEW_SECRET=<shared-random-secret>
ISODO_PREVIEW_SCHEME=https
```

当前 Gateway 把 Node link 的 `127.0.0.1:<port>` 当作 API 同宿主 target。API 与 Node 分机时，必须先增加 Node relay RPC，不能把宿主 loopback 当成远端地址。

## 5. 后端职责

| 层 | 职责 |
| --- | --- |
| Control API | 租户鉴权、Sandbox 查询、签发 app-link/preview、生成 URL、记录 generation |
| Preview Gateway | Host 解析、token/cookie 校验、generation 校验、HTTP/WS 反代、剥离凭证 |
| Node Orchestrator | 管理 VM/relay target；CHV 已实现 loopback relay + Vsock `PortForward`，Lima 通过 loopback target；不暴露公网 |
| Guest Agent | 执行 `PortForward(port)`，只连 Guest `127.0.0.1`；校验 RuntimeIdentity；限制并发/帧大小 |
| T3 | 在 Guest 内提供 `/ws` 和 pairing；不感知 Isodo 的 preview token |

## 6. 未实现与验收

尚未实现：

- 多宿主 Node relay：CHV 已使用 `RelayPort` bidi gRPC；Lima 仍走同宿主 direct target。尚未启动两个独立 API/Node 进程做 mTLS E2E。
- `POST /preview`、raw TCP `/ports/{port}`。
- `t3-base` 的真实构建与发布（配方在 `images/t3-base`；当前使用 `/opt/isodo/t3/bin.mjs`，需要 Linux/amd64 Docker 构建和 CHV 验收）。
- `generation` 的真实注入与校验。
- 真实 CHV T3 端到端（需要 Linux/KVM + `t3-base`）。

验收矩阵：

| 场景 | 期望 |
| --- | --- |
| Lima Sandbox 打开 `/app` | 303 到 T3 pairing，浏览器可用 |
| 桌面端调 `/app-link` | 200 JSON，`url`/`pairing_url` 可直接使用 |
| API key 调 `/app-link` | 403，不签发 pairing |
| 跨租户/不存在 | 404，不泄露存在性 |
| CHV 调 `/app-link` | 200；T3 未就绪时 503；不返回伪造 URL |
| 两个 Sandbox 同用 8080 | 两个独立 origin，cookie 不串 |
| Stop/Start 后旧链接 | 410/403，WebSocket 关闭 |
| 通用 preview 转发 | 删除 Cookie/Authorization；token 过期/签名错误拒绝 |
| 非法 Host/端口/路径 | 400/404，不解析为内部 target |
