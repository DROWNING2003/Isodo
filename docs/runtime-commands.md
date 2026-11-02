# Sandbox Runtime Commands（首个纵向切片）

> 状态：待代码审查和真实 CHV 验收。此 PR 不部署到 jfcs1024，不触及 E2B/client-proxy。公共 OpenAPI 代码生成与 TS/Python SDK 方法属于本 PR 的未完成项，不应宣称 SDK 已支持。

## 本次实现

提供非交互式、同步等待完成的命令执行：

```http
POST /v1/sandboxes/{sandboxID}/commands/run
Authorization: Bearer <session-or-api-key>
Content-Type: application/json

{
  "argv": ["/bin/sh", "-lc", "pwd && ls -la"],
  "cwd": "/workspace",
  "env": {"LANG": "C.UTF-8"},
  "timeout_seconds": 60
}
```

响应是 200 JSON：`execution_id`、`stdout`、`stderr`、`exit_code`（允许 null）、`reason`、`output_incomplete`。即使命令返回非零 exit code，HTTP 状态仍为 200，调用方需检查 `exit_code`；HTTP 4xx/5xx 表示 API/运行时错误。

- 只接受 `argv`，不会隐式经过 Shell。需要 Shell 时显式提供 `["/bin/sh","-lc", "..."]`。
- 最多 256 个 argv 项，请求体最大 64 KiB；Guest 还限制 argv/env 字节及 cwd。
- 允许 1–300 秒的命令超时；未指定使用 60 秒。这里 `timeout_seconds: 0` 同样使用默认值，不提供无限时命令。
- stdout+stderr 累计上限 1 MiB；输出丢失或溢出时返回错误并尽力终止进程。
- HTTP 取消/断开会尽力发送 Guest `SignalProcess(KILL)`；Guest 的命令超时仍作为兜底。
- 无 PTY、交互 stdin、后台运行、执行历史查询或重连；这些需要单独的 Runtime API 设计。

## 安全链路

```text
SDK / curl
  → Control HTTPS（认证、sandboxes:execute 权限、归属、运行状态）
  → Node mTLS（Sandbox ID + ownership generation）
  → CHV Node journal / 当前 Guest identity 检查
  → 仅 Node 内部可达的 Vsock :1024 gRPC 流
  → Guest StartProcess / SubscribeProcess / SignalProcess
```

沙箱程序始终由 Guest 内 `isodo-guest` 执行，Control 和宿主机不调用 Shell。

本次给 Node 内部可信 mTLS 调用增加了 `guest-rpc` relay 类型，仅允许显式请求 Vsock 1024、非零 generation 和当前运行实例。由于它承载原始 Guest gRPC，安全审查需要重点确认 Node 证书/Control 身份、服务权限和以后拆分最小权限 RPC 的方案。外部用户不能直接访问这条 relay。

API Key 执行需要专门的 `sandboxes:execute` scope，`sandboxes:write` 不默认授权执行。已新增 PostgreSQL Goose 0013 migration；现有 Key 不会自动提升权限。当前 UI 创建 API Key 没有添加执行权限选项，因此首版需通过已有 API Key 创建接口显式选择该 scope；如需查询列表，额外指定 `sandboxes:read`。

## 需要补齐后才能标记为验收

1. 在满足 Go 1.26.9 的环境执行 `go test ./internal/control/auth ./internal/control/sandbox ./internal/control/orchestrator ./internal/node/orchestrator ./internal/node/guesttransport`。
2. 验证 migration Up/Down、`make generate` / `make generate-check` 的生成文件一致性。
3. 将 commands/run 纳入 `api/openapi/openapi-public.yaml` 并运行生成，移除临时手工路由；生成 SDK 类型后再发布正式对外契约。
4. 使用独立 CHV 测试 VM 验证 stdout/stderr、exit code、timeout、断开取消、输出上限、跨租户/Key scope 拒绝、Stop/Start 后旧 identity 失效。
5. 审查 Node 原始 Guest gRPC relay 是否应改为更严格的方法级受限代理。
6. 本版未实现 Lima 本地命令 API；不应声称 Lima/CHV 功能对齐。

## 后续建议

先使单次命令完成真实 CHV E2E，再将进程创建、查询、流式输出、Signal 拆成标准 Executions API。同步设计 Guest PTY/stdin 和 Lima 后端，而不是重造 envd 或暴露 Agent 端口。
