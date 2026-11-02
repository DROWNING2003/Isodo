# Sandbox Runtime Commands：CHV 非交互执行

> 状态：受限 Node Commands RPC 已通过 Go 1.26.9 编译、目标包 race 测试、正式生成器一致性检查、执行 scope 的 PostgreSQL 集成测试及隔离 CHV 最小命令验收。未部署到现有 Node，仍保留 Draft 等待剩余验证。

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
- 此 PR 没有添加宿主机 `exec.Command`，也没有开放 Guest TCP/Vsock 给 SDK。
- 初版只支持远程 CHV，不支持 Lima 的通用 Commands。PTY/stdin、异步执行状态查询及更大输出量通过后续独立 Runtime API 提供。

## 合并前检查

```bash
go version  # 必须 >= go1.26.9
go test ./internal/control/auth ./internal/control/sandbox ./internal/control/orchestrator ./internal/node/orchestrator ./internal/node/guesttransport
make generate
make generate-check
```

- [x] `make generate`、`make generate-check`；提交正式 Go/TypeScript 绑定和 Commands API 文档。
- [x] `go build ./cmd/isodo-api ./cmd/isodo-node ./cmd/isodo-guest`。
- [x] `go test ./...`，默认未启用外部数据库/CHV 的测试按自身条件跳过；外部验证结果另列。
- [x] auth/sandbox/control orchestrator/node orchestrator/guesttransport 目标包 `go test -race`。
- [x] Web TypeScript 检查（Node 24；存在仓库已有 suggestion 提示，退出码 0）。
- [x] 独立 PostgreSQL 18 上 Goose 0013 Up→Down→Up；存在 execute scope 的 Key 时 Down 以 SQLSTATE 23514 拒绝，事务保留原约束；移除测试 Key 后 Down/Up 成功。
- [x] `TestPostgresCommandKeyScopes`：真实数据库保存/认证 Key，并由 HTTP 中间件验证 write-only Key 403、execute Key 204、五种 scope 组合可保存。
- [x] 真实 Node→Guest gRPC 组件测试覆盖非零退出码、双输出流、输出 gap、缺失退出事件、128 KiB 上限、取消及 deadline 的独立 KILL 清理。
- [x] 旧版 Node 没有 Commands RPC 时返回 Unimplemented 的 bufconn 测试；HTTP 将其映射为 501。输出上限 413、进程容量 429、幂等冲突 409 等另有路由测试。
- [x] jfcs1024 独立无网卡、RAM-root CHV Guest：双输出流/exit7、重试去重/参数冲突、Guest timeout、RPC 取消后进程组未继续写文件、输出上限、停机/重启/删除准入隔离。
- [ ] 完整 Control 生命周期 worker→持久化 Node journal→CHV Stop/Start/Delete 联合验收。本轮 CHV 测试真实重建 VM，但用测试构造的 journal/live map 驱动命令准入，不等于该联合链路已验证。
- [ ] StartProcess 响应丢失的真实网络故障注入；当前保留 Guest timeout 兜底的已知边界。

上述隔离测试阶段没有重启 jfcs1024 已有 Node/VM，没有修改其 API、Nginx、E2B 或 client-proxy。独立测试无 NIC、无生产数据库连接。初始化模块和 kernel 只读复用测试资产，所有写入位于专用目录及随机 /tmp 目录。

## 重现隔离 CHV 验收

在 Linux AMD64/KVM 主机上安装 Go 1.26.9、静态 BusyBox、cpio 和 gzip，指定匹配的 kernel 与 Vsock 模块：

```bash
ISODO_CHV_BINARY=/path/to/cloud-hypervisor \
ISODO_GUEST_KERNEL=/path/to/kernel \
ISODO_GUEST_MODULES=/path/to/matching-modules \
bash scripts/test-runtime-commands-chv.sh
```

脚本构建当前 Guest 与 initramfs，`TestRealCHVRuntimeCommands` 自己启动 VMM、生成 Sandbox UUID 与 boot/epoch，只清理自己创建的进程和临时文件；失败时保留 VMM/serial 日志。也可在开发机交叉编译测试二进制和 Guest 后复制到隔离主机，设置 `ISODO_CHV_INTEGRATION=1`、`ISODO_CHV_BINARY`、`ISODO_GUEST_KERNEL`、`ISODO_GUEST_INITRAMFS` 运行测试。本轮采用后一方式，在独立目录 `/opt/isodo/pr23-command-validation` 运行，测试耗时约 7 秒。

## 验证限制

完整 postgres 包集成测试未全绿：已有 `TestPostgresCredentialsLifecycle` 和 `TestPostgresSandboxTenantIsolation` 在创建沙箱时报告 `TEMPLATE_NOT_FOUND` / `template not found or not ready`。这是本轮实际观测到的模板夹具问题，未修改这些测试来放宽断言；本 PR 新增的执行权限数据库测试独立通过。没有据此声称所有集成测试通过。

完整 E2E 计划可在基础 Runtime API 齐全后集中推进，但正式合并仍要求最小可复现的 Guest 命令验证，避免仅凭 Mock 宣称命令可执行。

## 后续真实 API 演示（2026-11-02）

演示环境 Node/API 已部署 `78c513d`。通过真实账号的临时 API Key 调用 Control API，worker 创建 CHV 沙箱后，Commands API 成功执行 `python3 --version`（Python 3.10.12），再启动 Guest loopback 的 Python HTTP 服务。返回 exit_code=0，随后通过公开 HTTPS 预览及真实浏览器看到页面。创建、命令和 HTTP 预览在这次演示中使用真实数据库、Node、Guest 和 Nginx。

这补充了正常路径的真实运行证据，不代表上面列出的完整生命周期组合与网络故障注入已经通过。可复现客户端脚本见[HTTP 服务预览指南](../docs-site/content/docs/guides/http-port-preview.mdx)。

当前 Guest 内部端口为 31024 / 31080 / 38080，Node 与镜像需匹配升级，详见[迁移说明](runtime-service-ports.md)。历史测试记录保留原部署版本的范围。
