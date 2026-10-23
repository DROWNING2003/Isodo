# Cocoon 服务行为核对

核对日期：2026-10-24。状态：源码与同版本文档核对，未运行 Cocoon；以下不是 Isodo 已实现能力。桌面端与 Agent UI 不在本轮范围。

## 固定参考版本

- Sandbox / Go、Python SDK / silkd / sandboxd：`/Users/drowning/Desktop/qiniu/reference.cocoon-sandbox`，commit `2654727931532a37f952bf8b7c44bcb8ce8b9cff`。
- Engine：`/Users/drowning/Desktop/qiniu/reference.cocoon`，commit `4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5`。

本文的相对源码路径分别以这两个仓库为根；图索引后定位并读取了关键实现。许可证与引入方式仍以 [references.md](references.md) 为准。

## 网络：设备形态和出站权限是两个概念

Go `sdk/go/options.go:10` 定义默认 `NetNone`，表示没有 NIC，输入输出走 Vsock；`NetEgress` 表示 bridge/CNI NIC。服务 API 同版本文档 `docs/sandboxd-api.md:40` 规定未传 `net` 时为 `none`。`sandboxd/engine/engine.go:396` 的 `netArgs` 在无网冷启动时传 `--nics 0`，egress 才选择 bridge/CNI。

但 **`none` 不等于不能访问任何外部服务**。同版本 `docs/egress.md:10` 描述无 NIC Guest 通过本地 HTTP/SOCKS 代理、Vsock 和宿主代理访问获准服务，凭证在宿主注入。bridge egress 的 claim 则锁定 TAP，仅放行 DHCP，其余出站走同一代理；无策略也默认拒绝。CNI 与 guarded egress 的适用限制必须单独处理，不能把有 NIC 简化为 unrestricted Internet。

代理同时检查域名和解析后的 IP，默认拒绝内部地址、元数据地址、宿主地址，可显式配置例外。`docs/egress.md` 是此处部署与网络策略的证据，本轮没有逐条审计防火墙、DNS 重绑定和凭证注入实现，不能称为经过安全验证。

对 Isodo 的建议：将网络设备/传输模式和出站策略分字段；默认无 NIC 加受控代理可覆盖常见 API、HTTPS Git 和依赖下载，原生协议必须明确代理支持范围。授权目的地与使用凭证是不同权限。不要从 Cocoon 的 `none` 推导“所有联网一律禁止”。

## 执行：不能用一个断连规则覆盖全部操作

| 操作 | 已核对行为 | 关键证据（Sandbox 仓库） |
| --- | --- | --- |
| Go `Run` / `Exec` | 普通前台执行；`Exec` 包装 `Run`。ctx 取消且已收到 PID 时，SDK 另开连接 best-effort Kill，再返回错误；不能保证失联后远端必然终止 | `sdk/go/sandbox.go:110` |
| Python `run` | timeout watchdog 关闭流；知道 PID 时调用 `_kill_after_cut` | `sdk/python/cocoonsandbox/sandbox.py:130` |
| 原始 foreground exec 流断开 | silkd 输出写失败后中断 supervisor，`kill_on_drop(true)` 回收进程；安静进程可能继续直到下一次输出，不能把断开连接当确定取消 | `silkd/src/exec.rs:27`；`docs/sdk.md:564` |
| buffered HTTP exec | request context/timeout 关闭 Guest 流；收集失败且 PID 已知时另发 Kill；输出有 8 MiB 单次上限、64 MiB 节点预算 | `sandboxd/server/exec.go:18`、`:45` |
| `Spawn` | `Detach:true`，返回 PID；与原连接分离，忽略流读写配置，不支持 Session 参数 | `sdk/go/proc.go:15`；`silkd/src/exec.rs:106` |
| `Logs` / process `Attach` | replay 有界缓冲，Attach 随后订阅输出；不是完整历史日志，也不是可交互 PTY 重连接口 | `sdk/go/proc.go:54`；`silkd/src/proc.rs:15`（256 KiB ring） |
| `OpenPty` | 创建交互 shell；ctx/Close 控制生命周期。Guest 明确监听输入断连并 kill shell，resize 为 PID 定位的独立 RPC | `sdk/go/pty.go:38`、`:94`；`silkd/src/pty.rs:118` |
| persistent `Session` | 是保留 cwd/env/shell 状态的对象；Session 内命令不报告独立 PID，取消调用不等于终止命令；显式 Session.Close 结束 Session | `docs/sdk.md:560`、`:598`（本项按同版本官方文档，未完整追踪实现） |

`Spawn` 已退出记录保留 5 分钟、Session 空闲回收 30 分钟来自同版本 `docs/sdk.md:593`、`:612`，是参考实现的策略值，不是 Isodo 必须沿用的常量。

对 Isodo 的建议：明确 foreground、detached process、PTY 三种所有权，分别定义取消、Kill、断开订阅和终止终端。长任务用显式后台执行与日志查询；不要把“所有命令/PTy 断连继续并可重连”当行业统一语义。若 Isodo 选择持久可重连 PTY，需要自行规定有界回放、单写者、丢失提示、保留期限，并作为新增能力验收。

## 数据与生命周期：Engine Stop 和 Sandbox Release 不同

- Engine `hypervisor/stop.go:43` 的 `StopOneLocked` 停 VMM、清理运行态并设置 `stopped`；这是 VM 层操作，不应直接映射成 SDK 沙箱销毁。
- Sandbox `sandboxd/pool/claim.go:293` 的 `releaseResolved` 删除 claim 后清理 VM、hibernate snapshot、archive checkpoint；持久化失败会恢复 claim，已提交清理用 `context.WithoutCancel`，不会因为请求断开停止收尾。SDK 的 Release/Close 属于销毁语义。
- `sdk/go/sandbox.go:198` 描述 Hibernate 保存内存、进程、Session，下一次访问 Guest 自动唤醒；TTL 继续计时，过期按配置 destroy 或 archive。
- `sandboxd/pool/hibernate.go:61` 的 `hibernateLocked` 显式拒绝带 volume holds 的捕获，并拒绝 `NetEgress`：恢复时 Guest 先运行而新 TAP 尚未重新锁住，会产生绕过窗口。完成已开始的转移不受客户端取消影响，并先 journal pending snapshot，供重启 reconcile 接管。
- 外置 catalog volume 有独立生命周期和挂载协议，不能把 Release 清理 VM 推导为“所有外置卷都删除”；同版本 `docs/sdk.md` 的 volume 章节描述释放时的卸载、dirty 标记与恢复限制。

对 Isodo 的建议：先固定 Delete=销毁实例的可写层，Stop/Start=冷关机重启保留磁盘（若正式提供），Pause/Resume=保留内存状态，Snapshot=明确捕获内容。不要用 Stop 一词同时指代销毁与暂停。外置卷可以延后，但 spec 必须说明实例盘不是独立持久卷，以及节点丢失时的恢复承诺。网络恢复前的隔离必须是恢复流程前置条件；不能仅依据 CHV snapshot API 存在便承诺联网 VM 的安全恢复。

## 最值得补充的服务规格

1. 执行与终端契约：前台/后台/PTY 所有权、取消与超时、流控、退出码、有界回放、PID 重用防护。
2. 生命周期与数据契约：Stop/Delete/Pause 边界、TTL、并发与幂等、持久化先后、重启接管、清理失败重试。
3. 出站与凭证契约：设备模式、目的地策略、DNS/IP 检查、协议范围、凭证注入和撤销、恢复时隔离。

以上常规行为可以依据参考实现直接提出明确默认值。只有参考之间确有冲突、或与 Isodo 已承诺产品语义冲突的选择，才需要进入设计问答；不能把所有工程惯例都作为用户必须回答的问题。
