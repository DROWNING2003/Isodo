# Guest Agent 进程与 PTY 运行时规格 v0.1

状态：实现规格草案，尚未实现或在正式 Guest 镜像中验收。范围是首个实现模块 `cmd/isodo-guest`、`internal/guest` 中的进程管理、PTY、输出流和生命周期；文件 RPC 按 [Guest 协议](guest-protocol.md) 的后续阶段实现。桌面端、T3 会话和持久 Shell Session 不在本规格范围。

接口以 [guest.proto](../api/proto/isodo/guest/v1/guest.proto) 为准；字段、状态码、限额与安全边界以 [Guest 协议](guest-protocol.md) 为准。本文补足这些 RPC 在 Guest 内的实现结构、并发规则和故障处理。Cocoon 源码是行为参照，不是代码复用许可；许可证见 [参考仓库清单](references.md)。

## 1. Cocoon 实现核对

核对的是 `reference.cocoon-sandbox` commit `2654727931532a37f952bf8b7c44bcb8ce8b9cff` 的 `silkd`，不是单独的 Cocoon Engine。该版本 Guest daemon 是 Rust；`reference.cocoon-agent` commit 另有一个 Go Agent，但只覆盖基础 Exec/Reseed，不提供 PTY、进程 attach/replay 或 Isodo 的 gRPC 协议。

| 能力 | Cocoon 源码事实 | Isodo 采用方式 |
| --- | --- | --- |
| Go Cocoon Agent | `agent.Server` 每条连接处理一个请求；Exec context 受该连接驱动，peer EOF 会 cancel child。`runExec` 直接运行 argv、回传 stdout/stderr/exit，未发现 PTY 或持久进程 attach。它是小型 Go Vsock Agent 参照，不是 silkd 的 Go 版本。 | 只借鉴直接 argv、并行读写和退出回收；Isodo 用 gRPC 服务与 execution registry，断连不 cancel 已接受的执行。 |
| Exec 生命周期 | `exec::run` 用直接 argv 启动程序，记录 PID，stdout/stderr 分开读取。前台流写失败时 handler 中止 supervisor，Rust `Child` 因 `kill_on_drop=true` 被杀；但静默进程要等下一次输出或退出才会发现断连。SDK Run 的取消/超时会另行 best-effort 发 Kill。`detach=true` 则让执行脱离当前请求，进程表保留至退出后 300 秒。 | 不暴露两种隐式生命周期。所有成功返回 execution_id 的 Exec 都独立于 RPC 连接；用户显式 `SignalProcess` 才终止。 |
| Attach 与日志 | `attach` 在同一把 ring 锁下快照历史并订阅广播，避免 replay 与实时输出之间的竞态；ring 是最近 256 KiB，不是完整日志。 | 采用原子“回放后跟随”的语义、显式 seq/gap 和有界 ring；使用 `execution_id`，不以可复用 PID 作为公开句柄。 |
| PTY | `pty::open` 建立 PTY master/slave，Shell 的 stdin/out/err 接 slave；子进程通过 `setsid` 和 `TIOCSCTTY` 获得 controlling terminal。Resize 在 master 上做 `TIOCSWINSZ`。 | 采用相同 Linux PTY 机制；PTY 是进程句柄的一种。Agent 保留 master，RPC 断开仅关闭该订阅/写者，允许按 Isodo 协议重连。 |
| PTY 断连 | 当前 `pty::pump` 的输入 feeder 结束后通知 pump；pump 对 Shell 调用 `start_kill`，然后等待进程和排空尾部输出。因此 Cocoon PTY 不是可重连终端。 | 明确不采用此关闭行为。disconnect 不释放 master、不杀进程；Stop/Delete 或显式 signal 才结束运行。 |
| 退出与清理 | Cocoon 在 leader 退出后限时排空输出；普通 Exec 有 2 秒，PTY drain 也受 `POST_EXIT_DRAIN` 限制。普通 detached Exec 的结果按 shell 退出码映射；其测试验证了 attach 能拿到退出后回放及退出结果。silkd 的 Exec/PTY 路径未建立 workload cgroup，也不能据此认为整棵后代树会被清理。 | 保留真实 wait 状态、排空上限、结果先入表再通知。Isodo 需要 cgroup v2 管理整棵后代树，避免孤儿进程。 |
| 信号与 PID | Cocoon 的 `kill` 对未退出表项按 PID 发信号；代码注释明确指出 exited PID 可能复用，因此退出后不再 signal。signal 目标是直接 PID；Session 的 shell process group 是另一套明确实现。 | 不接受任意 PID；内部用 execution_id 映射进程和 cgroup，校验当前 RuntimeIdentity。必须避免 PID 复用误杀。 |
| 慢订阅者 | Cocoon foreground 有容量 256 的有界 channel，慢客户端会给读取链路施加 backpressure；attach 通过广播，可能报告 lag 后继续。 | 用户负载不由某个订阅者的网络速度控制。执行泵持续读取；共享 ring 淘汰时发 gap；慢订阅者达到协议超时后只断开自身。 |

源码：[`Cocoon Agent runExec`](https://github.com/cocoonstack/cocoon-agent/blob/624c167778f3ab0db24884715b62c33fedb872e9/agent/exec.go#L35-L115)、[`Cocoon Agent connection handler`](https://github.com/cocoonstack/cocoon-agent/blob/624c167778f3ab0db24884715b62c33fedb872e9/agent/agent.go#L27-L116)、[`silkd exec::run`](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/exec.rs#L27-L137)、[`pty::open` / `pump` / `resize`](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/pty.rs#L24-L218)、[`Proc` 表与 ring](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/proc.rs#L13-L244)、[`server::attach`](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/server.rs#L280-L305)、[PTY 断连 E2E](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/tests/pty_e2e.rs#L124-L148)、[Exec/Attach E2E](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/tests/exec_e2e.rs#L146-L339)。PTY 创建、Resize 和 controlling terminal 细节见 [`sysutil`](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/sysutil.rs#L126-L160)；silkd 普通 Exec 未设 per-command timeout 与 workload cgroup，可核对 [`ExecReq`](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/proto.rs#L192-L206) 和 [`Session` process group](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/session.rs#L51-L62)。

## 2. Isodo 实现边界

Guest Agent 在 VM 内以 root 启动，独占管理 Guest workload 的进程和 cgroup；用户命令固定降权到模板基线 UID/GID 1000。Node 是唯一可信宿主控制者，通过 Vsock/gRPC 调用 Agent。命令参数不经 Shell 拼接，不从工作负载继承 Agent 的宿主控制环境或平台凭证。

```text
cmd/isodo-guest
  └── internal/guest/server       gRPC 服务、Ready、identity/epoch 校验
      ├── process.Manager         execution 表、幂等、并发名额、退出状态
      ├── process.Launcher        argv/env/cwd 校验、降权、cgroup、exec
      ├── process.OutputHub       stdout/stderr pump、ring、订阅与 gap
      ├── process.InputWriter    单写者 token、stdin 顺序与 ack
      └── pty.Terminal            PTY fd、输入、resize、关闭与 drain
```

这些是逻辑职责，不要求一个文件对应一个包。`cmd/isodo-guest` 只装配启动参数、日志、Vsock listener、gRPC server 和关闭流程；不放进程语义。`internal/guest` 不访问宿主 CHV API、Node journal、systemd 或租户 API。

服务启动前检查：Linux、cgroup v2 可写、workload 根目录和 `/workspace` 已就绪、Guest identity 已注入、Vsock 监听可用。任一条件不满足时 Ready 返回 `UNAVAILABLE/GUEST_NOT_READY`，不得先接受 Exec 再补齐隔离条件。Guest Agent 重启生成新的 `guest_epoch`；新 epoch 开始接受 RPC 前终止并清理旧 workload cgroup，旧 execution_id 统一返回 `STALE_RUNTIME`。

## 3. 进程管理器

### 3.1 创建与登记

`StartProcess` 顺序固定如下：

1. 校验 RuntimeIdentity、request_id、argv/env/cwd、期限、PTY 参数和资源名额；对已登记 request_id 先执行同参重放/异参冲突判断。
2. 在锁内保留并发名额，生成不可复用的 execution_id，写入 `starting` 项和本 epoch 的幂等记录。不得在全局锁内等待子进程或网络 I/O。
3. 建立本次执行的 cgroup 与管道/PTY；在任何用户代码可运行前完成 cgroup 归属。不能保证这一点时启动失败并回收空 cgroup。
4. 以 UID/GID 1000、受控环境和直接 argv 启动进程组；PTY 模式建立独立 session/controlling terminal。启动 stdout/stderr/PTY reader、wait/reaper、超时任务以及有界输入入口。
5. 确认 PID/start time、cgroup、identity 和输出泵均登记后，将状态切为 `running` 并返回 ProcessInfo。返回前的任何失败必须 kill 已启动的子进程、等待回收、关闭 fd、删除临时 cgroup、释放名额并保存短期失败幂等结果。

如果客户端没收到 StartProcess 响应，Node 用相同 request_id 重试；Guest 返回同一 execution_id 或同一失败，不可再次 fork。该保证仅限当前 `guest_epoch` 与协议规定的墓碑期，不声称 Agent 崩溃时 exactly-once。

### 3.2 每个执行的状态与资源

每个表项至少包含：完整 RuntimeIdentity、execution_id、原始 argv、UID/GID、PID、进程 start time、cgroup 路径、`starting/running/exited/interrupted` 状态、是否 PTY、stdin 状态、PTY master（如有）、事件 seq/ring、开始与结束时间、不可变 ProcessExit 和活动订阅/写入者引用。

Guest 只允许经 execution_id 解析资源。PID 仅供诊断；过期 execution_id 不得 fallback 到同 PID 的新进程。Signal 在持有该执行生命周期锁且确认状态仍 running 时对执行进程组或 cgroup 执行；Stop/Agent shutdown 使用执行 cgroup 清理整个工作负载树。

每执行一个 cgroup，所有后代继承同一 cgroup。执行 leader 退出时，reaper 读取真实 wait 状态，终止 cgroup 中残留后代，等待 cgroup 空，再完成有限输出 drain 和保存 exit。实现必须证明不存在工作负载先执行、后移动到 cgroup 的窗口；cgroup 缺失、不可用或清理未完成时拒绝启动/报告未完成清理。

并发限制沿用 Guest 协议：最多 32 个执行、8 个 PTY、单 execution 最多 4 个只读订阅者；达到上限返回 `RESOURCE_EXHAUSTED`，不排队启动用户命令。短时 `starting` 名额同样计入限制。

### 3.3 普通 Exec 输出泵

- stdout 与 stderr 各自独立持续读取，任何时候都不能等待某一个订阅者后停止读取另一路。每个读取块进入同一 execution 的递增事件序列和有界 ring，再通知订阅者。
- 输出 ring 和事件格式使用 [Guest 协议第 4 节](guest-protocol.md#4-输出事件回放与流控) 的 1 MiB/最多 256 条预算、seq、gap、exit 及 30 秒保留值；本规格不另造第二套缓冲限制。
- stdout/stderr 各自保持读取顺序，不宣称两个 pipe 之间存在真实全序。客户端从 `after_seq` 重连时先取 ring 快照，再接实时事件，快照订阅必须原子切换，不能漏掉两者之间的 chunk。
- 写入共享 ring 不能等网络发送。每个订阅者有独立有界发送队列；追不上时按 seq 返回 gap。超过协议规定的 10 秒无发送进展后，仅关闭订阅连接，执行保持运行。
- 用户程序输出超过 ring 容量时继续运行并丢弃最旧事件，明确发送 gap；不因输出爆量无限占用 Guest 内存，也不把丢失内容表示成完整日志。

这是对 Cocoon 256 KiB tail 的扩展：Cocoon 的 `Ring::trim` 按字节淘汰；它没有 seq/gap 供消费者识别丢失。Isodo 的 gap 是新的协议责任，不能仅复用“继续广播”的行为。

### 3.4 输入、信号和执行期限

- 非 PTY stdin 按协议的单写者 token 和连续帧序号写入单一 stdin pipe；队列容量有界。ack 只表示 Guest 已接受字节，不表示应用已读。断开写入 RPC 释放 token，不关闭 pipe；只有显式 `close_stdin` 才产生 EOF。
- PTY 输入写入同一个 PTY master；流断开只释放写者 token。断连期间不接收键盘输入，但 master 与 Shell 仍被 Agent 持有，新的 WriteInput 可重新取得该 execution 的写者 token。
- 超时仅由 Guest Agent 的持久 execution timer 负责，不绑定 Start/Subscribe/WriteInput 的 RPC deadline。到期时先原子确定 timeout 退出原因，再发 TERM，等待 2 秒，最后 KILL execution cgroup；实际 wait 状态仍要保存。
- `SignalProcess` 必须通过 execution_id 和 RuntimeIdentity 找到目标，检查尚未退出后返回“信号已提交”；已退出记录按 Guest 协议视为幂等 no-op。信号成功不代表进程已退出，调用方随后订阅/查询结果。
- Guest Agent 的 SIGTERM/SIGINT 优雅关闭顺序：停止接受新的 StartProcess → 关闭订阅和输入 admission → 依策略终止所有 workload cgroup → wait/reap → 关闭 PTY master 与 pipe → 停 gRPC listener。超过 Node 给定 shutdown grace 后由 VM 停止处理收敛。

## 4. PTY 实现要求

1. 校验 PTY 尺寸范围，调用 Linux `openpty` 建 master/slave，初始 winsize 为请求的 cols/rows；master 设非阻塞，所有 fd 使用明确所有权，避免复制 slave fd 导致 master 永远读不到 EOF。
2. Shell 必须为直接可执行路径，默认 `/bin/sh -i`（模板实际 Shell 可由固定镜像配置覆盖），不通过命令字符串执行。子进程启动阶段 `setsid()`，再以 fd 0 调用 `TIOCSCTTY`；slave 接 stdin/stdout/stderr。工作负载用户是 UID/GID 1000。
3. Agent 自始至终持有 master。PTY reader 将 master 字节作为单一 `pty` channel 进入 OutputHub；不伪造独立 stdout/stderr。Linux master 读到 `EIO` 可表示 slave 全部关闭，应按 PTY 结束条件处理，不能忙循环。
4. ResizeTerminal 校验当前 execution 为 PTY、identity 正确、writer_token 有效且尺寸合法；对 master 做 `TIOCSWINSZ`。底层 Linux 向前台进程组投递 `SIGWINCH`；调用成功只表示窗口尺寸已提交。
5. 终端输入流断开后保留 PTY，订阅流断开后不关闭 master。重新连接先按 after_seq 回放 ring，再跟随新输出；遗漏的数据必须带 gap。多个 observer 可订阅，但同一时间只能有一个 input writer。
6. Shell 退出时保存真实 wait 结果，清理同 cgroup 的后代，最多 drain 1 秒 master 剩余字节，再发送唯一 exit 事件、关闭 master/slave、释放 cgroup 和 PTY 并发名额。RPC 错误或慢客户端不能跳过 drain/reap。

可重连 PTY 的 master 是 Agent 所有的长期资源，不归某个 gRPC handler 所有。任何 handler 的 defer/取消回调都不得直接 kill shell、关闭 master 或删除 Process 项。只有显式 Signal、命令期限、Guest shutdown、execution 完成策略允许释放这些资源。

## 5. 错误与资源回收矩阵

| 事件 | Agent 行为 | 对调用方可见结果 |
| --- | --- | --- |
| argv/cwd/env/PTY 参数非法 | 不 fork；释放 reservation；不建 execution | 稳定的 `INVALID_ARGUMENT` 与具体 reason |
| fork 前创建 cgroup/PTY 失败 | 关闭已打开 fd，删空 cgroup，保留可重试失败墓碑 | 对应内部错误；不可返回 running |
| fork 后登记或返回启动结果失败 | kill execution cgroup、wait/reap、结束 output pump、保存启动失败结果 | 同 request_id 重试返回同一失败 |
| Start/Subscribe 客户端断开 | 保留 execution；reader/reaper/timer 继续工作 | 客户端用同 identity 和 execution_id 重连 |
| PTY 输入断开 | 释放 writer token，保留 master、shell、输出 pump | 可重新建立 WriteInput |
| 一个 observer 变慢 | 补发 ring 可用事件；超过 10 秒关闭该 observer | `SLOW_CONSUMER`；执行不受影响 |
| Guest Agent 收到关闭信号 | 停止新 admission，终止并回收所有 workload，最后退出 | Node 观测 Guest 不可用，执行句柄不跨 guest_epoch |
| VM 强制关闭/Guest 崩溃 | 不能构造虚假 exit_code；新 Agent 启动清理旧 cgroup | Node 标 `interrupted`/旧 epoch stale |

## 6. 实施验收

这些是实现后必须满足的验收，不代表目前已通过。mock 进程表或本机 Unix socket 单测不能替代 Guest VM/Vsock 验收。

| 场景 | 验收结果 |
| --- | --- |
| argv `printf` 带空格、shell 特殊字符 | 参数按 argv 原样到达；默认不发生 Shell 展开 |
| StartProcess 回复丢失后同 request_id 重试 | 一个 execution、一个子进程、同一 execution_id |
| 同 request_id 参数不同 | 返回幂等冲突，不启动第二进程 |
| 长 Exec 的 Start/Subscribe 流断开 | 子进程仍存活；新订阅恢复 ring 尾部和实时输出 |
| stdout/stderr 同时大量输出 | 两路都被持续读取；内存有界；淘汰时可见 gap |
| 一个慢 observer 与另一个正常 observer 并发 | 慢者超时断开，正常者和子进程继续 |
| PTY Shell 执行交互命令并 resize | 有 controlling terminal；`stty size` 与最新尺寸一致；可观察 SIGWINCH |
| PTY 输入和订阅先后断开、再重连 | Shell 与 master 存活；输出按 seq 回放；输入只能有一个写者 |
| shell leader 退出但后台后代留在 cgroup | 后代被清理，输出有限 drain 后才发 exit；cgroup 最终为空 |
| 执行超时与自然退出竞争 | 只记录一次终止原因和一次 exit，保留真实 wait 状态 |
| 旧 execution_id 在 PID 复用后 Signal | 不影响新进程；返回 stale/not found |
| Guest Agent 重启 | 旧 workload 清理；guest_epoch 变化；旧 handle 拒绝 |
| Guest 无 cgroup v2 或 workload 用户配置不符 | Ready 失败，不接受工作负载 |

## 7. 当前实现顺序

第一阶段只实现进程管理与 PTY：Ready、StartProcess、GetProcess、ListProcesses、SubscribeProcess、WriteInput、ResizeTerminal、SignalProcess。按 [Guest proto](../api/proto/isodo/guest/v1/guest.proto) 逐 RPC 实现，不改 wire 字段。第二阶段再做 Stat/List/Read/Write/Mkdir/Remove/Move 文件 RPC，并执行独立文件安全验收。

开始编码前按项目入口核对 [参赛要求](../docs/competition-requirements.md) 和 Git/v0 基线。协议 protobuf 已编译；正式 Guest Agent 的 h2/gRPC over Vsock、PTY master 重连、cgroup 与 Node 生命周期都尚未验证。
