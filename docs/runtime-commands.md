# Sandbox Runtime Commands：CHV 非交互执行

> 状态：已按 PR #23 的 Review 重构为受限 Node Commands RPC。接口及测试已提交，但受当前环境 Go 1.26.9 工具链不可用限制，仍须在开发机验证编译、生成文件一致性及真实 CHV Guest 行为；不要直接合并或部署。

## API 契约

```http
POST /v1/sandboxes/{sandboxID}/commands/run
Authorization: Bearer <session-or-access-token>
Idempotency-Key: 123e4567-e89b-12d3-a456-426614174000
Content-Type: application/json

{
  "argv": ["/bin/sh", "-lc", "pwd && ls -la"],
  "cwd": "/workspace",
  "env": {"LANG": "C.UTF-8"},
  "timeout_seconds": 60
}
```

- Idempotency-Key **必须提供**，限 8–128 个 ASCII 字符 `[A-Za-z0-9_.:-]`。同一 Sandbox 运行实例的重试必须使用相同 Key 和完全相同的命令参数；不同参数会因 Guest 去重冲突返回 HTTP 409。由于 Guest 去重保留窗口有限（当前结果 30 秒、去重记录约 10 分钟），本 API 不承诺无限期历史结果可用。
- API Key 须显式拥有 `sandboxes:execute` scope（须先执行 Goose 0013）。原有 `sandboxes:write` 不授予命令执行权限。浏览器 session 仍遵守 Cookie/Origin/CSRF 校验。
- `argv` 为数组，不隐式套用 Shell。工作目录必须是 Guest 内有效的绝对路径。
- `timeout_seconds` 默认为 60，最大 300；0 也使用默认值。服务器 HTTP 截止时间比 Guest timeout 多 10 秒以允许清理和返回。
- 单次 stdout+stderr 原始输出合计上限 **128 KiB**：由于 Node mTLS gRPC 单条响应上限是 1 MiB，而文本在 JSON 序列化时可能扩大，不能允许完整 1 MiB 原始数据未经校验直接塞进一个响应。
- 返回 HTTP 200 的响应字段为 `execution_id`、`stdout`、`stderr`、`exit_code`（信号终止时 null）、`reason`、`output_incomplete`。非零退出码属于命令执行结果，仍是 HTTP 200。
- 429 表示 Guest 进程/去重容量耗尽；413 表示 API 输出大小上限；409 表示 Sandbox 状态冲突或幂等键冲突；502 表示 Guest 输出丢失；504 表示 RPC Deadline；501 表示未支持该后端。

## 架构与安全边界

```text
SDK / HTTP
    → Control HTTPS：认证、Scope、Tenant 归属、Sandbox Node/Revision 检查
    → Node mTLS：isodo.node.v1.CommandRunner/RunCommand（单用途 RPC）
    → CHV Node：核对 journal 的 generation/revision、running、Guest identity
    → CHV 私有 Vsock:1024：Guest StartProcess / SubscribeProcess / SignalProcess
    → 沙箱内部进程
```

Node Commands RPC 采用内置 Protobuf `google.protobuf.BytesValue` 承载限制大小的 JSON 请求/结果（`internal/runtimecommands`），是用于避免第一版引入任意 Guest RPC 透传的受限服务。**它仅暴露 RunCommand 一种受控操作**，不允许 Control 通过此 RPC 调用 Guest 的 BindRuntime、Files 或 PortForward；未来迁移为专用类型化 Proto 时需要保持协议兼容。

- Node 完全从自身 live map/journal 获取 VMM/Guest socket，客户端无法指定宿主机路径、Vsock 或 Node IP。
- `request_id` 由 Sandbox ID、generation、boot ID、Guest epoch 与客户端幂等键确定性散列产生；停机恢复后的旧请求无法命中新的执行实例。
- Node 收到请求取消、输出溢出、输出 gap 或未完整退出事件时，使用独立的 3 秒清理 Context 尝试 `SignalProcess(KILL)`。若 StartProcess 已在 Guest 成功但响应在网络中丢失，Node 可能暂时拿不到 ProcessRef；Guest 最大执行超时作为兜底。需要独立故障注入测试确认。
- 此 PR 没有添加宿主机 `exec.Command`，也没有开放 Guest TCP/Vsock 给 SDK。
- 初版只支持远程 CHV，不支持 Lima 的通用 Commands。PTY/stdin、异步执行状态查询及更大输出量通过后续独立 Runtime API 提供。

## 合并前检查

```bash
go version  # 必须 >= go1.26.9
go test ./internal/control/auth ./internal/control/sandbox ./internal/control/orchestrator ./internal/node/orchestrator ./internal/node/guesttransport
make generate
make generate-check
```

- [ ] 重新运行生成器生成 OpenAPI Go/TypeScript/Fumadocs 代码；本 PR 的 Generated 文件暂由仓库既有模式补写，**不能视为生成器一致性已证实**。
- [ ] 校验 PostgreSQL Goose 0013 Up/Down、已有 Key 无权限提升、API Key 只在显式 execute scope 时执行。
- [ ] 运行 Web typecheck，确认 API Key 权限标签完整。
- [ ] 通过隔离 CHV 测试 VM 校验 exit code、stdout/stderr、不同 Idempotency-Key、相同 Key 重试、相同 Key 不同参数拒绝、超时取消、输出限制及生命周期 revision fencing。
- [ ] 确认真实运行中的 Go 服务只注册了一个 Node Commands RPC，并在无该 RPC 的旧 Node 上正常返回 501。
- [ ] 不重启 jfcs1024 现有 Node/VM，不触碰 E2B、client-proxy 或公司原 Nginx。

完整 E2E 计划可在基础 Runtime API 齐全后集中推进，但正式合并仍要求最小可复现的 Guest 命令验证，避免仅凭 Mock 宣称命令可执行。
