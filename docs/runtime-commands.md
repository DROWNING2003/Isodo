# Sandbox Runtime Commands：CHV 非交互执行

远程 CHV 已接通公开 Commands，正常路径通过真实 API 与 Guest 验证。证据及未验证场景见[当前状态](current-status.md)。

## API 契约

```http
POST /v1/sandboxes/{sandboxID}/commands/run
Authorization: Bearer <access-token>
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

正式生成的 TypeScript 客户端通过 options.headers 传递幂等键；重试时复用同一个 key：

```ts
const key = crypto.randomUUID();
const result = await runSandboxCommand(sandboxID, {
  argv: ["/bin/echo", "hello"],
  timeout_seconds: 30,
}, { headers: { "Idempotency-Key": key } });
```

浏览器登录会话使用现有 Cookie/CSRF transport；API Key 使用现有认证 transport，不能把 session Cookie 当作 Casdoor Bearer token。

## 架构与安全边界

```text
SDK / HTTP
    → Control HTTPS：认证、Scope、Tenant 归属、Sandbox Node/Revision 检查
    → Node mTLS：isodo.node.v1.CommandRunner/RunCommand（单用途 RPC）
    → CHV Node：核对 journal 的 generation/revision、running、Guest identity
    → CHV 私有 Vsock:31024：Guest StartProcess / SubscribeProcess / SignalProcess
    → 沙箱内部进程
```

Node Commands RPC 采用内置 Protobuf `google.protobuf.BytesValue` 承载限制大小的 JSON 请求/结果（`internal/runtimecommands`），是用于避免第一版引入任意 Guest RPC 透传的受限服务。**它仅暴露 RunCommand 一种受控操作**，不允许 Control 通过此 RPC 调用 Guest 的 BindRuntime、Files 或 PortForward；未来迁移为专用类型化 Proto 时需要保持协议兼容。

- Node 完全从自身 live map/journal 获取 VMM/Guest socket，客户端无法指定宿主机路径、Vsock 或 Node IP。
- `request_id` 由 Sandbox ID、generation、boot ID、Guest epoch 与客户端幂等键确定性散列产生；停机恢复后的旧请求无法命中新的执行实例。
- Node 收到请求取消、输出溢出、输出 gap 或未完整退出事件时，使用独立的 3 秒清理 Context 尝试 `SignalProcess(KILL)`。若 StartProcess 已在 Guest 成功但响应在网络中丢失，Node 可能暂时拿不到 ProcessRef；Guest 最大执行超时作为兜底。需要独立故障注入测试确认。
- 该链路不使用宿主机 `exec.Command`，也没有开放 Guest TCP/Vsock 给 SDK。
- 当前只支持远程 CHV，不支持 Lima 的通用 Commands。PTY/stdin、异步执行状态查询及更大输出量通过后续独立 Runtime API 提供。

## 重现隔离 CHV 验收

在 Linux AMD64/KVM 主机上安装 Go 1.26.9、静态 BusyBox、cpio 和 gzip，指定匹配的 kernel 与 Vsock 模块：

```bash
ISODO_CHV_BINARY=/path/to/cloud-hypervisor \
ISODO_GUEST_KERNEL=/path/to/kernel \
ISODO_GUEST_MODULES=/path/to/matching-modules \
bash scripts/test-runtime-commands-chv.sh
```

脚本构建当前 Guest 与 initramfs，`TestRealCHVRuntimeCommands` 自己启动 VMM、生成 Sandbox UUID 与 boot/epoch，只清理自己创建的进程和临时文件；失败时保留 VMM/serial 日志。也可在开发机交叉编译测试二进制和 Guest 后复制到隔离主机，设置 `ISODO_CHV_INTEGRATION=1`、`ISODO_CHV_BINARY`、`ISODO_GUEST_KERNEL`、`ISODO_GUEST_INITRAMFS` 运行测试。## 验证边界

真实 Commands 与 Python HTTP 预览已运行。StartProcess 响应丢失、完整网络故障注入和所有恢复组合不能由正常路径推断为通过。复现测试时只创建隔离资源，记录 Sandbox ID、阶段耗时、输出完整性及清理结果。

当前 Guest 内部端口为 31024 / 31080 / 38080，Node 与镜像需匹配，见[端口策略](runtime-service-ports.md)。
