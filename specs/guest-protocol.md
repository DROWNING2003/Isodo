# Guest Exec / PTY / Files 协议 v1

状态：2026-10-24，实现契约草案。行为基准为 E2B；本文的 protobuf 已做编译校验，服务端、SDK 和 CHV 上的 gRPC 尚未实现或验收。桌面端集成延后。

协议源文件：[guest.proto](../api/proto/isodo/guest/v1/guest.proto)。服务通用行为见 [service-behavior.md](service-behavior.md)，已完成的真实 VM/Vsock 探针见 [validation.md](validation.md)。本文件固定 Guest 接口；REST/Node API 负责映射这些能力，不再另外定义另一套执行生命周期。

## 1. 范围与依据

首版提供 Ready、进程创建/查询/列表/输出订阅、输入、信号、PTY resize，以及工作区文件的 Stat/List/Read/Write/Mkdir/Remove/Move。持久 Shell Session、文件 Watch、Guest 热升级和 Agent 对话协议留给后续版本。

主要参考本地 E2B commit `6b82caf690b06fb983254714187dc496ac4d0cec`：

- [process.proto](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/envd/spec/process/process.proto)：进程、PTY、输入、信号、输出事件的分工。
- [handleStart](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/envd/internal/services/process/start.go#L23)：执行 context 独立于订阅请求。
- [handleConnect](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/envd/internal/services/process/connect.go#L19)：重新订阅现存进程，以及退出事件保留。
- [filesystem.proto](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/envd/spec/filesystem/filesystem.proto)：文件元数据和目录操作；本规格的 Read/Write 流是 Isodo 自有接口。

继承的行为是进程与连接分离、PTY 使用进程服务、显式输入/信号、超时分层。Isodo 自行规定执行 ID、启动幂等、有界输出回放、单写者和文件路径范围；不声称与 E2B wire/SDK 兼容，也不将这些补充归为 E2B 已有实现。

## 2. 分层、传输与身份

```text
API / SDK -> Node 可信 RPC -> CHV per-VM Unix socket
                           CONNECT 1024 + OK handshake
                           -> HTTP/2 gRPC -> GuestService
```

- Guest 监听 Vsock 1024，Node 先执行 CHV CONNECT 握手，确认 `OK <port>` 后才交给 HTTP/2。握手超时 5 秒，单行最多 128 bytes；错误/EOF 时关闭连接。
- Guest 接口不监听公网/TAP；Node 限制 Unix socket 所有权和可达性。控制面到 Node 的 mTLS 与 Guest 内部传输是两条不同边界。
- Node 依据自己的实例 journal 选择 socket，不接受客户端提供宿主路径、CID 或 Guest 端口；Guest 自报身份不能授权宿主操作。
- Guest 传输只授予该 VM 内操作能力，不下发 Nomad/数据库/对象存储或第三方永久凭证。Guest 与同一 VM 内负载不被当成两个租户边界。
- gRPC 在完成握手的 Vsock 字节流上运行，首版不额外套 Guest TLS。该选型须用真实 CHV 验证 HTTP/2、双向流和取消；现有 C 探针没有完成此验证。

### 2.1 RuntimeIdentity

所有有副作用的请求和读取现有资源的请求都带完整 identity：

| 字段 | 所有者与语义 |
| --- | --- |
| `sandbox_id` | 控制面生成的沙箱标识 |
| `ownership_generation` | 控制面/Node 的所有权版本；不能单独替代宿主 fencing |
| `boot_id` | Node 每次冷启动 VM 生成；Node 进程重启不更换 |
| `guest_epoch` | Guest Agent 每次启动生成；Guest Agent 重启即更换 |

前三项通过模板支持的启动配置注入，禁止放入长期密钥；`guest_epoch` 经 Ready 获取。Node 将响应与宿主启动记录交叉核对，随后在转发时填入 identity。外部用户不能通过提交更高 generation 获得控制权。

不匹配返回 `FAILED_PRECONDITION / STALE_RUNTIME`，不按新实例 PID 自动寻找替代进程。Guest 的 `execution_id` 只在完整 identity 下唯一。PID 仅用于诊断，不提供任意 PID 的操作入口。

### 2.2 Ready 与兼容

`Ready` 在 Vsock listener、进程管理、Guest workload cgroup、工作区目录及文件服务初始化成功后返回；未就绪返回 `UNAVAILABLE / GUEST_NOT_READY`。

响应必须含 protocol `1.0`、identity、agent_version、capabilities 和实际 limits。首版要求 `exec.v1`、`pty.v1`、`files.v1`、`output_replay.v1`。Node 仅在 major 相同且所需能力齐全时将实例判为 Guest Ready；不能只凭 TCP/Vsock 连通判断 running。

新增 protobuf 字段必须兼容；删除字段保留 field number/name。新增可选能力通过 capability 声明；客户端不得仅凭 minor 版本推测能力。安全字段、启动参数和枚举未知值要明确拒绝，不能静默按默认权限执行。

## 3. 进程 RPC 与数据模型

| RPC | 输入 | 输出/语义 |
| --- | --- | --- |
| `StartProcess` | identity、request_id、command、cwd、env、timeout、stdin_open、可选 pty | unary ProcessInfo；实际启动并登记成功后返回 |
| `GetProcess` | ProcessRef | 当前状态、执行身份、保留中的退出结果和输出游标 |
| `ListProcesses` | identity、page_size、page_token | 当前运行与结果保留期内的已退出记录；稳定分页 |
| `SubscribeProcess` | ProcessRef、可选 after_seq | attached、gap/output、exit、heartbeat 的服务端流 |
| `WriteInput` | open 后连续 data/close_stdin | 双向流；顺序输入、写者 token 与逐帧 ack |
| `ResizeTerminal` | ProcessRef、writer_token、size | 对现有 PTY 调整窗口 |
| `SignalProcess` | ProcessRef、signal | 发信号，不等待进程退出 |

`ProcessInfo.state` 只有 running/exited；尚未启动的请求由 Node operation 记录，启动失败返回 RPC 错误，不建立一个伪 running 进程。结果使用 `ProcessExit`，不是把传输错误编码为退出码。

### 3.1 创建参数

- `command.argv` 是完整 argv，至少一个元素，argv[0] 非空；单参数可包含空格但不发生 Shell 展开。需要 Shell 时由 SDK 显式发送 `["/bin/sh", "-lc", script]`。
- 可执行路径为绝对路径或按最终 PATH 查找；cwd 默认 `/workspace`，可指定默认 workload 用户可进入的 Guest 绝对目录。cwd 不存在返回 `INVALID_ARGUMENT / INVALID_CWD`。
- 以模板声明的非特权用户 `isodo` 运行（基线 UID/GID 1000）；请求没有 root/任意 UID 参数。该用户可修改自己的 Guest 环境，不能据此访问宿主资源。
- env 合并模板固定基础环境与请求覆盖项；基础环境提供 PATH、HOME、USER、LANG；PTY 默认 TERM=xterm-256color。键不得为空、含 `=`/NUL，值不得含 NUL。Agent 管理凭证和宿主环境不能作为基础 env 继承。
- argv 最多 256 项，argv+env UTF-8 编码总量最多 128 KiB，路径最多 4096 bytes；不满足返回错误而不是截断。
- 未传 command_timeout_seconds：普通 Exec 为 60 秒，PTY 为 0。显式 0 表示不设置命令时限，仍受沙箱停止/删除与资源限制；正值范围 1–86400 秒，超过拒绝，不静默截短。
- 非 PTY `stdin_open` 默认 false，子进程 stdin 立即为 EOF；需要后续输入时显式 true。PTY 必须 true，否则 INVALID_ARGUMENT。
- 传入 `pty` 就进入 PTY 模式；省略表示 stdout/stderr 分离。PTY rows/cols 必须各在 1–1000，SDK 的默认值为 24×80，不能发送 0 让服务端猜测。

### 3.2 启动确认、重试与幂等

Node 在调用前保存 API 幂等键到 `request_id` 的映射。Guest 以 identity + 方法 + request_id 去重，规范化后的相同参数返回同一 execution_id；不同参数返回 `ALREADY_EXISTS / IDEMPOTENCY_CONFLICT`。request_id 由 Node 生成，长度 1–128 的 ASCII 安全字符。

Guest 在 fork 前占用名额并登记 pending request，在进程配置和登记完成后答复。并发相同请求等待同一个结果，不能重复 fork。登记失败必须终止/回收已创建进程，再返回错误；fork 从开始就进入对应 workload cgroup，不能先运行用户代码后补移动。

启动是一次已接受的命令意图：请求 deadline/断连后仍可能完成。客户端没有收到答复时，Node 以相同 request_id 在有效窗口重试；不生成新 request_id。Guest 去重信息覆盖整个进程运行期及终止后 10 分钟；启动失败结果也保留 10 分钟，容量不足时拒绝新副作用请求。

该去重仅在相同 guest_epoch 内有效。Node 的自动启动重试窗口最多 30 秒，超过返回结果未知并保留 operation；不能在去重已过期或 Guest 重启后再次自动执行。Agent 崩溃导致“已 fork、未登记”的不确定窗口必须由 Guest 重启清理策略收敛，不承诺跨崩溃 exactly-once。

### 3.3 执行、断连与退出

Guest 为进程建立独立 process group 和 workload cgroup；普通 Exec 的进程组不包含 Agent。PTY 设置 controlling terminal/session；Shell 作业控制可切换前台进程组。

进程 context 从 Guest 生命周期与命令期限派生，不从 Start/Subscribe/WriteInput RPC context 派生。客户端取消等待、网络断开、Node 连接重建，只移除订阅/写者；不能触发 Kill、关闭 PTY master 或终止 Guest 进程。Agent 必须持续读取 stdout/stderr/PTY，不能因为没有订阅者停止读取。

退出处理：waitpid 获得实际状态 → 清理该执行的剩余子进程 → 排空输出（最多 1 秒）→ 保存结果 → 发布 exit → 回收 live entry。结果先保存再移除 live entry，避免重连落入两者都不存在的窗口。子进程独立长驻应使用另一次 Start；通过 shell `&` 留下的后代不独立于本次执行。

- 正常退出：exit_code 存在，signal 不存在，reason=completed。
- 信号退出：signal 存在，exit_code 不存在；SDK 可另外展示 shell 惯例的 128+signal，但不能将其写成 OS 真实退出码。
- command timeout：Guest 在到期且观察到进程仍运行时原子认领 timeout 原因，TERM → 2 秒宽限 → KILL；reason=command_timeout，并保留真实最终 wait 状态。已记录的自然退出不改写为超时。
- 内部传输/订阅错误：gRPC error，执行结果未知或仍运行；没有 exit 事件。不能返回 exit_code=0。
- 每个进程最多保存一个不可变退出结果；每次完整订阅最多收到一个 exit。重连可再次读取同一个结果。

`SignalProcess(TERMINATE)` 给该执行的所有 workload 进程发 TERM，2 秒后 KILL 剩余进程；KILL 立即清空 workload cgroup。INT 对普通 Exec 发到进程组，对 PTY 发到当前前台进程组。PID/进程组解析与身份检查必须原子保护；不得向复用 PID 发送旧请求的信号。

同一已退出执行的信号请求在结果仍保留时返回成功；未知/过期执行返回 NOT_FOUND。RPC 成功表示信号已提交，不表示进程必已退出，调用方应 Get 或 Subscribe 等待结果。

### 3.4 重启与故障边界

- Node 重启、Guest 和 VM 保持存活：恢复同一 identity 后可以 Get/Subscribe；不会自动重发旧 stdin。
- VM Stop/Start：新的 boot_id，旧 handle 失效，Node 将未获 Guest 退出结果的执行标为 interrupted，不能伪造 exit code。
- Guest Agent 崩溃重启：新的 guest_epoch，首版启动前清空旧 workload cgroup并回收旧 PTY/临时上传；不接管旧进程，不提供热升级。旧 handle 返回 STALE_RUNTIME。
- Guest 被恶意负载破坏：Node 只能报告不可用，不基于 Guest 自报扩大宿主权限。

## 4. 输出事件、回放与流控

### 4.1 事件顺序

每次 Subscribe 的首帧必须是 attached（seq=0），携当前 ProcessInfo。它表示订阅成功，不能被解释为重新启动命令。

output/exit 共享从 1 递增的进程事件 seq。每条 output 最多 64 KiB，channel 为 stdout、stderr 或 pty；非 PTY 永不发送 pty channel，PTY 永不伪造独立 stderr。stdout 内部和 stderr 内部各保持读取顺序，不承诺跨两个 OS pipe 的写入时间总序。

gap、attached、heartbeat 的 seq=0，不占用进程事件序号。exit 是最后一条有序事件，之后不再产生 output。输出读错误或排空超时置 result.output_incomplete=true，结果不因缺少输出而被替换成成功的完整流。

### 4.2 游标与有界回放（Isodo 自有补充）

每个执行的 ring 总预算为 1 MiB（含 payload 和事件元数据），最多 256 条 output，另有独立退出结果槽。每次从源读取先形成有序事件进入 ring，再通知订阅者；无订阅者时仍消费输出，旧数据按需丢弃。

- 未传 after_seq：从当前 ring 最早事件开始回放，再跟随实时输出。Start unary 与 Subscribe 之间的短命令输出在容量范围内可取回。
- after_seq=0：从事件 1 开始；如果已淘汰则先发 gap。
- after_seq=N：仅发送 seq>N 的事件；参数超过服务端已产生的最大 seq 返回 OUT_OF_RANGE，不等待不存在的未来游标。
- 丢失区间：发送 gap(first_missing_seq,last_missing_seq)，再发仍在 ring 内的 output/exit。客户端必须展示丢失提示，不能拼接为完整日志。
- 当前 ring 完全为空时 first_available_seq=next_seq；存在退出记录时可保留 exit，即使此前输出已丢失。
- 进程退出后 ring 与完整退出结果保留 30 秒；去重墓碑保留 10 分钟。30 秒后 Get/Subscribe 返回 NOT_FOUND / RESULT_EXPIRED，而 Start 相同 request_id 在墓碑期返回 FAILED_PRECONDITION / EXECUTION_EXPIRED，不重复执行。

订阅方根据游标从共享 ring 获取事件，通知只作唤醒提示，不能为慢客户端无限复制数据。ring 追不上时发送 gap；网络发送 10 秒没有进展时关闭该订阅，返回 RESOURCE_EXHAUSTED / SLOW_CONSUMER，进程继续。无输出时每 15 秒 heartbeat；RPC deadline 对订阅的结束只影响连接。

这与 E2B 当前仅保留退出事件、未提供完整丢失输出 replay 的实现不同。1 MiB 缓冲是 Isodo 的有限补充，不是持久日志；超过容量的长期日志应由工作负载写入受配额管理的文件。

## 5. 输入与 PTY

WriteInput 首帧为 open(ref)，seq=0；校验后返回 writer_token。每个进程同一时间最多一个输入写者；第二个返回 ALREADY_EXISTS / WRITER_BUSY，不抢占。只读 Subscribe 不占写者名额。

后续 data/close_stdin 的 seq 从 1 连续递增。每帧最多 64 KiB；Agent 按顺序写入 pipe/PTY，写入内核成功后 ack 对应 accepted_seq。ack 表示字节被接收，不保证应用已消费；不回显输入内容。写入 10 秒无进展则关闭输入流并返回 DEADLINE_EXCEEDED / INPUT_TIMEOUT，进程继续。

输入流断开释放 writer_token，保留非 PTY stdin 与 PTY master，由 Agent 持有；之后可用新输入流接续。未收到 ack 的输入可能已经部分或全部写入，SDK 不能自动重发。新流重新从 seq=1 开始，token 不跨连接生效。

- 非 PTY close_stdin：关闭 stdin write end，重复关闭幂等；之后 data 返回 FAILED_PRECONDITION / STDIN_CLOSED。
- PTY 不支持 close_stdin，返回 INVALID_ARGUMENT。发送 Ctrl+D（0x04）由终端 line discipline 决定行为，不能把关闭网络流当 EOF。
- Ctrl+C（0x03）经 PTY 写入，以 Guest terminal 当前 termios/前台作业控制解释；Signal(INT) 是显式进程控制，不与字节写入混用。
- ResizeTerminal 要求运行中的 PTY、当前 writer_token 与合法 size；调用 ioctl 更新 winsize，终端按 Linux 语义触发 SIGWINCH。非 PTY 返回 FAILED_PRECONDITION / NOT_A_TERMINAL。
- 进程退出时 WriteInput/Resize 不再成功，返回 FAILED_PRECONDITION / PROCESS_EXITED；不能对新进程复用旧 token。

PTY 和 Exec 共用 Start/ProcessInfo/Signal；外部 terminal_id 是 PTY execution_id 的展示别名，不创建第二份生命周期状态。首版不提供终端屏幕快照和自动重发键盘输入。

## 6. 文件 RPC

### 6.1 路径与访问范围

文件 RPC 使用 Guest 逻辑绝对路径，根为 `/workspace`；拒绝相对路径、NUL、任何 `..` 分段、超出根的路径和 `/workspace` 根目录删除/移动。不暴露宿主路径。Exec cwd 可在 Guest 内更广范围活动，Files 路径限制是文件接口的访问面限制，不是阻止同一用户 Exec 访问其 Guest 文件的隔离边界。

正式模板必须把 `/workspace` 挂载为独立文件系统，不向 workload 暴露同一文件系统的根外别名挂载；workload 不具有 mount 权限。这样目录不能通过同文件系统 rename 移出工作区，跨文件系统移动会被内核拒绝。Node/模板未满足此条件时 Files 不就绪，当前 initramfs 探针不满足本规格的正式 Files 条件。

以 workload 用户权限访问文件。目录描述符从受保护的工作区根获取，解析使用 openat2 的 BENEATH/NO_MAGICLINKS/NO_SYMLINKS 或等价严格实现；不允许存在可竞争的“先校验字符串再按绝对路径打开”。内核不支持时首版 Files 不就绪，不静默降级为不安全路径解析。

Stat/List 可用不跟随目标的元数据操作展示符号链接；Read/Write 不跟随最终或中间 symlink。Remove/Move 可以操作最终 symlink 本身，中间路径仍禁止 symlink；不提供创建 symlink 的 RPC。禁止读写设备、FIFO、socket、proc/sys/dev 和 Agent 可执行路径。

### 6.2 元数据与目录

FileInfo 含 path/name/kind/size_bytes/mode/modified_at；symlink_target 仅是原始目标字符串，不代表已授权访问。mode 以权限 bits 表示，不依赖展示用字符串。

Stat 不计算文件 sha256；ListDirectory 只列一层，不递归、不隐式统计目录大小。page_size 默认 100，最大 500；文件名按 UTF-8 bytes 排序，非法 UTF-8 名称返回 DATA_LOSS / INVALID_FILENAME（v1 不进行有损替换）。路径必须是 UTF-8。

首次 List 为该次分页建立最多 4096 entries 的短期列表快照，page_token 绑定 identity、path、snapshot 和过期时间；有效期 30 秒，每 Guest 最多 8 个目录列表快照。token 过期返回 FAILED_PRECONDITION / PAGE_EXPIRED，从首页重试；目录后续变化不会插入已有快照，但返回的文件内容/元数据不承诺持续不变。超过目录容量返回 RESOURCE_EXHAUSTED / DIRECTORY_TOO_LARGE。

ListProcesses 使用同类不透明分页 token，绑定 identity，有效期 30 秒、最多 8 个快照；只列运行与结果保留期内的记录，不列墓碑。列表条目是采样状态，之后应以 Get/实际操作结果为准。

### 6.3 ReadFile

ReadFile 仅接受普通文件。响应顺序为一个 header、连续 chunk、一个 end，再正常结束 RPC：

- chunk.offset 从 0 连续递增；data 最多 64 KiB，不发送空 chunk。
- header.file.size_bytes 为开始时大小；end 含实际传输字节数和传输内容的 32-byte SHA-256。
- 空文件只发送 header/end；读取取消关闭 fd，不影响其他操作。
- 打开 fd 保持同一 inode；读取前后检查 size/mtime/ctime/inode，发现变化或字节数不符返回 ABORTED / FILE_CHANGED，不发 end。客户端仅在 end 校验和 gRPC OK 均完成后接受完整结果。
- 该检查不等于对抗恶意并发写入的文件快照；需要一致版本时先停写或使用快照制品。并发 rename 不会把已打开 fd 切换到新文件。

v1 暂不提供 Range/断点续传；失败后重新读取整个文件。

### 6.4 WriteFile 与提交点

WriteFile 是 client-streaming。首帧必须 header，随后仅 chunk，发送方 half-close 表示传输结束；第二个 header、乱序 offset、超长/不足 size、hash 不匹配都拒绝。

Header 必填 identity/request_id/path/size_bytes/sha256；sha256 为 32 raw bytes，空文件同样校验。mode 未传默认 0600，只接受 0600/0644/0700/0755；不保留旧文件 mode。overwrite 默认 false；目标为目录、symlink 或其他特殊文件时一律拒绝。

1. 安全解析 parent dir（必须已存在），在同目录创建 agent 专用临时文件，使用 workload 用户权限与 0600 初始 mode。
2. 边写边校验 offset、大小、hash、空间配额；取消/失败删除临时文件，不改目标。
3. 校验通过后设置最终 mode、fsync 临时文件；overwrite=false 使用不覆盖 rename，overwrite=true 原子替换普通文件。
4. rename 是可见性提交点；之后 fsync parent directory，保存去重结果，返回 FileInfo+sha256。

请求取消在 rename 前可放弃；rename 后必须完成有限收尾，不因连接断开删除新目标。rename 后 fsync/结果保存失败返回 COMMIT_UNKNOWN，调用方先同键重试/Stat+Read 校验，不能默认目标没改变。宿主崩溃的数据持久性仍取决于 Guest 文件系统、虚拟磁盘和宿主存储链路，不因一次 Guest fsync 宣称完整灾备。

同一 identity+方法+request_id 已提交成功且 header 哈希一致时，服务端返回原始结果并标 replayed=true；重试流可仅发送相同 header 后 half-close。未提交的新 header 即使看见目标 hash 相同也不能跳过上传；同键不同 header 返回 IDEMPOTENCY_CONFLICT。成功响应丢失后的重试无需重写目标。

上传元数据（path/size/hash/mode/overwrite）和结果参与 10 分钟去重缓存；只提供同 guest_epoch 内重试，不承诺 Guest 崩溃后的 exactly-once。首版写同一路径采用每路径互斥，冲突返回 ABORTED / PATH_BUSY；外部 Exec 修改目录不受此锁约束，安全路径解析仍必须处理竞争。

临时文件名由 Agent 随机生成，不使用用户路径拼接规则；启动清理只处理 Agent 登记并验证归属的残留，不按宽泛通配符删除用户文件。

### 6.5 Mkdir / Remove / Move

这些修改 RPC 同样带 request_id，成功结果保留 10 分钟，同键不同参数冲突。只在相同 epoch/有效窗口内重试；不能盲目重放取消或传输错误后的文件修改。

- MakeDirectory：parents 默认 false；已有同名目录幂等成功，已有文件冲突。默认 mode=0700，可选 0755。parents=true 的新目录逐层创建，部分失败可能保留已建空目录，不能宣称全事务。
- Remove：不存在返回成功；recursive 默认 false，非空目录返回 FAILED_PRECONDITION / DIRECTORY_NOT_EMPTY。recursive=true 仅删除根内、不跟随 symlink；超过 4096 个条目拒绝继续并返回 RESOURCE_EXHAUSTED / REMOVE_LIMIT，可能已部分删除，需检查后重试。
- Move：同一工作区文件系统内 rename，overwrite 默认 false；overwrite=true 仅替换普通文件，不能替换目录或 symlink。跨文件系统返回 FAILED_PRECONDITION / CROSS_DEVICE_MOVE，不隐式 copy+delete。源与目标相同则成功。
- Remove/Move 的目录遍历按 dirfd 进行；独立工作区文件系统和无别名挂载是防止目录并发移出授权根的前置条件。openat2 处理 symlink/向上解析，独立文件系统阻止 rename 越界；不能仅在修改前检查一次路径字符串。

修改 RPC 在实际副作用后断连可能已成功，传输失败表示结果不确定；Node 按同键查询/重试，超出缓存或 epoch 改变后停止自动重放。

## 7. 限额与默认值

以下是 Isodo v1 工程默认值，通过 Ready 返回实际值。配置只能在服务启动时固定；Node 按实际限额做准入，不能等分配了进程后才发现超限。

| 项目 | 默认/上限 |
| --- | --- |
| gRPC 解压后单条消息 | 1 MiB；首版不启用消息压缩 |
| output/input/file chunk | 64 KiB |
| 并发运行进程 | 32（包含 PTY） |
| 运行 PTY | 8 |
| 每进程输出订阅 | 4；写者独立限制为 1 |
| 输出 ring | 每进程 1 MiB，active+retained 共用 64 MiB 全局预算 |
| 退出结果和输出保留 | 30 秒 |
| 副作用去重/墓碑 | 终止/完成后 10 分钟，最多 4096 个请求记录；活跃请求不淘汰 |
| 单文件读写 | 256 MiB；还须满足磁盘配额 |
| 并发文件传输 | 4（读写合计） |
| 普通 RPC deadline 建议 | 10 秒；启动自动重试总窗口 30 秒 |
| 文件 RPC deadline 默认 | 300 秒；取消不保证回滚已提交修改 |
| 输出订阅 deadline | 默认无固定期限；每 15 秒 heartbeat |
| List page_size | 默认 100，最大 500 |
| 单次目录快照/递归删除 | 4096 entries |

ring 元数据与 payload 一起计入预算。64 MiB ring 全局预算不足时先淘汰已退出输出，再缩减最旧输出，始终保留仍有效的退出结果；gap 明示丢失。请求墓碑预算不足不能淘汰去重窗口内的记录，应拒绝新副作用操作。容量上限与内存占用均纳入压力测试，不能将它们当无限资源。

## 8. 错误和超时

gRPC 使用标准状态码，附自有 ErrorDetail（放在 google.rpc.Status.details 中）；HTTP facade 将 ErrorDetail.code 原样保留在统一 error body，不以错误文本驱动 SDK 分支。消息不含命令 env、文件内容或 secret。

| gRPC | ErrorDetail.code 示例 | SDK / HTTP 映射 |
| --- | --- | --- |
| INVALID_ARGUMENT | INVALID_COMMAND / INVALID_CWD / INVALID_PATH / INVALID_FRAME | 400，不重试 |
| UNAUTHENTICATED / PERMISSION_DENIED | UNAUTHENTICATED / ACCESS_DENIED | 401/403；跨租户存在性由 API 隐藏为 404 |
| NOT_FOUND | PROCESS_NOT_FOUND / FILE_NOT_FOUND / RESULT_EXPIRED | 404；不能当 exit 0 |
| ALREADY_EXISTS | IDEMPOTENCY_CONFLICT / WRITER_BUSY / FILE_EXISTS | 409；WRITER_BUSY 可在旧连接确认释放后重试 |
| FAILED_PRECONDITION | STALE_RUNTIME / PROCESS_EXITED / STDIN_CLOSED / NOT_A_TERMINAL / PAGE_EXPIRED / EXECUTION_EXPIRED | 409；先刷新状态 |
| RESOURCE_EXHAUSTED | PROCESS_LIMIT / TRANSFER_LIMIT / SLOW_CONSUMER / STORAGE_QUOTA | 429；调整资源或订阅 |
| OUT_OF_RANGE | INVALID_CURSOR | 400，重新获取 ProcessInfo |
| ABORTED | FILE_CHANGED / PATH_BUSY | 409，读取可重试；写操作必须同键 |
| DEADLINE_EXCEEDED / CANCELLED | RPC_TIMEOUT / INPUT_TIMEOUT / RPC_CANCELLED | 504/请求取消；不代表命令终止 |
| UNAVAILABLE | GUEST_NOT_READY / GUEST_UNAVAILABLE | 503；先确认 epoch，副作用按同键规则 |
| DATA_LOSS | HASH_MISMATCH / INVALID_FILENAME | 422；文件结果不能接受 |
| INTERNAL | INTERNAL_ERROR / COMMIT_UNKNOWN | 500；副作用可能已发生，不能生成新键重试 |
| UNIMPLEMENTED | CAPABILITY_UNSUPPORTED | 501；不降级到假成功 |

retryable 只表示恢复连接/同键重试可能有用，不表示任何请求均可重放。传输断开导致的 status 无 ErrorDetail 时，SDK 仍保留标准状态并报告结果未知。

命令 timeout 导致真实进程退出时，用 ProcessExit.reason=command_timeout 表达，gRPC 订阅正常送完 exit 并以 OK 结束。只有等待/网络本身超时才是 DEADLINE_EXCEEDED；二者不能混淆。

## 9. SDK 语义示例

以下为接口伪代码，不代表已有 Go SDK：

```text
handle = processes.start(argv, command_timeout_seconds=0)
subscription = handle.subscribe()           // 可随时 disconnect，进程继续
writer = handle.open_input()                // 仅 stdin_open=true / PTY
writer.send(bytes)                          // 未收到 ack 不自动重发
writer.disconnect()                        // 释放输入写者，保留进程
subscription.disconnect()
handle = processes.get(execution_id)        // 验证原 runtime identity
subscription = handle.subscribe(after_seq=last_received_seq)
handle.signal(terminate)                    // 显式终止；等待 exit 判断完成
```

`commands.run` 在 start 后 Subscribe 并聚合有限输出；`background=true` 仅使 SDK 立即返回 handle，Guest 不需要第二个“后台进程”状态。聚合上限默认 stdout+stderr 8 MiB，超限截断并标 truncated，不因此 Kill。若收到 gap、stream error 或等待超时，必须返回 partial/result_unknown 状态，不能返回看似完整的成功结果。一个已知非零 exit 与传输失败是两类 SDK 错误。

读文件客户端先落本地临时文件，收到 end 与 gRPC OK 后验证总长度/hash再提交；写文件先计算 size/hash再流式上传。需要 stdin EOF 的非 PTY命令使用 close_stdin；PTY 的输入连接关闭不发送 Ctrl+D。

## 10. 验收矩阵

实现时按真实 Guest/CHV 和可重复故障注入验证，不能用 protobuf 编译通过替代：

| 场景 | 必须观察到的结果 |
| --- | --- |
| argv 含空格/引号；显式 shell | argv 不被二次展开；shell 仅在明确指定时生效 |
| stdout/stderr 与非零退出 | 两流分别可识别，exit_code 为真实值；PTY 为合并流 |
| 极短命令先退出再 Subscribe | 30 秒内从 ring+exit 获取结果，不落入空窗 |
| Start 响应丢失后同键重试 | 同 epoch 内只有一个实际进程；不同参数返回冲突 |
| 断开 Start/输出/输入连接 | 进程继续，写者释放；重新 attach 后身份一致 |
| command timeout 对比 wait timeout | 前者杀 workload，后者只断开等待 |
| 高输出、无人订阅、慢订阅 | Guest 持续排空输出，内存有界；gap/慢客户端错误可见 |
| after_seq 游标、过期、并发退出 | 顺序可解释，最多一个 exit/订阅，不重放新 PID 的旧结果 |
| PTY Ctrl+C / Ctrl+D / resize | 前台 job 中断、EOF 按 termios 工作、stty size 正确 |
| 双写者与未确认输入重连 | 第二写者拒绝，SDK 不重发可能已提交的字节 |
| 进程 setsid/fork、TERM 被忽略 | timeout/terminate 后 cgroup 无残留；Agent 仍存活 |
| Node 重启与 Guest 重启 | 前者同 epoch 可重连；后者清理旧 workload 并拒绝旧 handle |
| 二进制与空文件读写 | 不经 UTF-8 替换，长度/hash 正确 |
| 上传断流/坏 hash/空间不足 | rename 前目标不变，临时文件回收；commit 后不假称回滚 |
| 覆盖、移动与重复修改 | 默认不覆盖；同键不产生第二次修改；跨设备移动明确拒绝 |
| traversal/symlink/并发目录 rename | 无越界读写/删除，无 TOCTOU 绕过；根不可删除 |
| File Read 并发修改 | 可检测变化返回 FILE_CHANGED，不能接受不完整成功响应 |
| 资源/记录缓存达到上限 | 正确拒绝/截断，不 OOM、不提前丢失幂等保护 |

## 11. 本次验证与后续落地

本次仅校验协议编译、文档引用与接口一致性；服务尚未实现。protoc 校验命令：

```sh
protoc -I api/proto --include_imports \
  --descriptor_set_out=/tmp/isodo-guest-v1.pb \
  api/proto/isodo/guest/v1/guest.proto
```

实现顺序：Guest Ready/identity → Exec create/Get/Subscribe → stdin/signal/timeout → PTY → Files → 真机断连、重启、限额与路径竞争测试。Node/HTTP facade 必须沿用同一 execution_id 和错误模型，不能把“已收到请求”当“已执行完成”。
