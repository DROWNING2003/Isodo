# 沙箱生命周期与恢复规格 v0.1

状态：2026-10-24，实现契约草案；尚未实现。主要参考 E2B 的 API/节点编排分层，VMM 操作以 CHV 官方接口为准。首版支持 Create/Get/List/Stop/Start/Delete；Pause、Snapshot、Fork 不在本规格的首版实现范围。

相关契约：[服务行为](service-behavior.md)、[Guest 协议](guest-protocol.md)、[模板与存储](template-storage.md)、[网络与安全](network-security.md)。E2B 与 Cocoon 的证据边界见 [E2B 调研](research-e2b-behavior.md)、[Cocoon 对照](research-cocoon-behavior.md)。这里描述的是 Isodo 设计，不能作为上游实现事实引用。

## 1. 所有权与状态

- 控制面拥有用户鉴权、配额、desired state、节点归属和 operation；Node 拥有该节点真实 VM、进程托管、磁盘、网络与 journal。
- 首版一个沙箱固定一个节点，不自动跨节点恢复。节点不可达时保留原归属与磁盘占用，不以心跳超时为由创建第二个 VM。
- 每个 Node 用宿主独占锁保证一个写入实例；每个 sandbox 使用单独的操作锁。进程锁不是跨节点 fencing。
- `ownership_generation` 控制所有权；`boot_id` 区分冷启动；`guest_epoch` 区分 Guest Agent 生命周期。Node 重启接管不改变这三个值；Guest Agent 实际重启只改变 epoch。
- `revision` 是沙箱控制命令的递增序号；与 ownership generation 分开。Node 保存最后接受 revision，拒绝旧控制命令，并在每个不可逆阶段前检查是否已被 Delete supersede。

### 1.1 状态模型

`desired_state` 只取 running/stopped/deleted；瞬态进度属于 observed_state：

| observed_state | 事实 |
| --- | --- |
| requested | 记录/配额已提交，尚未确认 Node 接收 |
| preparing | 镜像、磁盘与网络准备中 |
| starting | VMM 启动，尚未通过 Ready |
| running | 同一实例 VMM 存活、Guest Ready、必需网络策略生效 |
| stopping | 停止准入、VM 与网络回收中；实例盘保留 |
| stopped | VMM unit 已退出，磁盘完整保留，运行态网络已回收 |
| deleting | 持久删除意图已接受；实例资源尚未全部证实清理 |
| deleted | Node 确认实例资源全部清理，或有明确节点销毁/介质回收证据 |
| error | 已确认的操作失败；含资源是否残留和恢复路径 |
| unknown | Node 失联或无法核实实例事实；last_confirmed_state 单独保留 |

每次观测记录 `observed_at`、`node_id`、`ownership_generation`、`revision`、`boot_id`、`last_error`。失去 Guest Ready 时不能继续无条件返回 running：立即标 health=degraded，连续 15 秒不可用转 error；Node 失联转 unknown。短暂健康故障不能触发第二次创建。

### 1.2 转移与并发

| 当前状态 | Start | Stop | Delete |
| --- | --- | --- | --- |
| requested/preparing/starting | 同一目标返回已有 operation | 409 OPERATION_IN_PROGRESS | 接受删除，取消未完成创建并收敛清理 |
| running | 返回已完成的 no-op operation | 接受停止 | 接受删除 |
| stopped | 接受冷启动 | 返回已完成的 no-op operation | 接受删除 |
| stopping | 409 OPERATION_IN_PROGRESS | 返回已有停止 operation | 升级为删除目标，先完成停止再删盘 |
| deleting | 409 SANDBOX_DELETING | 409 SANDBOX_DELETING | 返回已有删除 operation |
| deleted | 410 SANDBOX_DELETED | 410 SANDBOX_DELETED | 同租户 tombstone 内幂等成功 |
| unknown | 503 NODE_UNAVAILABLE | 可记录停止意图，待原 Node 核实执行 | 可记录删除意图，保持 deleting/cleanup_pending |
| error | 仅在已确认无 VMM、盘可恢复且失败原因已消除时允许 | 尝试停止残余运行态 | 总是允许记录删除并重试清理 |

Delete 是唯一能 supersede 在途生命周期操作的用户动作；不能先删盘再等在途 Create 结束。原 operation 标 cancelled 仅表示被新意图替代，不表示它从未产生资源；后续清理由 Delete operation 接管。

所有 Exec/PTY/Files admission 都检查最新 revision 的本地目标和 observed state。Stop/Delete 先关闭新请求准入、释放写入通道，再终止 VM；已提交文件修改按 Guest 提交语义处理。数据平面错误不得撤销已接受的删除意图。

## 2. API 与 operation

所有路径位于 `/v1`，snake_case 字段。生命周期修改返回 `202 {sandbox_id, operation_id}` 和操作查询 Location；no-op 返回 `200` 及 succeeded operation。请求取消不取消后台 operation。

| API | 入参 | 结果 |
| --- | --- | --- |
| POST /sandboxes | template_digest、resources、network、可选 metadata | 新沙箱与 Create operation |
| GET /sandboxes/{id} | 身份上下文 | desired/observed、health、时间戳、资源、错误、operation_id |
| GET /sandboxes | page_size/page_token、state | tenant 内分页，默认 50、最大 200，token 5 分钟 |
| POST /sandboxes/{id}:stop | 可选 grace_period_seconds（默认 20，0–60） | Stop operation |
| POST /sandboxes/{id}:start | 无可变模板/资源参数 | 对原盘冷启动的 operation |
| DELETE /sandboxes/{id} | 无保留磁盘参数 | Delete operation |
| GET /operations/{id} | 身份上下文 | 状态、阶段、结果或错误 |

首版资源在创建时固定；Start 不兼作 resize、换模板或迁移节点。未实现的 Pause/Resume/Snapshot 接口返回 501 CAPABILITY_UNSUPPORTED，不能静默执行 Stop。

operation 至少保存：id、tenant_id、sandbox_id、action、request_hash、idempotency_key、revision、status、stage、attempt、created_at、updated_at、result、error、cleanup_pending、needs_attention。

`status=queued/running/succeeded/failed/cancelled`；stage 表示实际执行阶段。succeeded 只在目标真实状态已核实后设置。failed 必须描述已知失败和残留资源；网络未知不能伪造为已清理失败。取消/failed 结果保持不可变，reconcile 后续恢复记事件或新的内部 repair operation。

### 2.1 幂等与任务领取

- 所有写操作必填 Idempotency-Key，长度 1–128。作用域为 tenant+method+规范化 path+key，绑定规范化请求哈希；同键不同参数返回 409。
- 去重记录最少保留 24 小时；operation 未完成时不得淘汰。SDK 自动重试不超过 24 小时，超过窗口需显式新操作与状态核查。
- API 在一个 PostgreSQL 事务中提交 desired state、revision、配额预留和 operation；operation 表充当 outbox，不另加消息系统。
- worker 以事务条件更新领取 operation，租约 30 秒、每 5 秒续租；worker 租约只协调控制面消费者，不能证明 Node 或 VM 已死亡。
- Node 持久化 operation_id/revision/阶段，重复 RPC 查询/继续相同 operation，不重复创建 VMM。Guest Start 的 request_id 与节点生命周期 operation_id 分属不同操作类型，不能混用。
- HTTP 超时后客户端先查询 operation；worker RPC 丢回复后先 Inspect 同 operation，而不是分配新 sandbox/node。

## 3. Create 工作流与补偿

首版启动 Ready 总预算默认 120 秒（镜像下载/准备另设 300 秒）；Node 的网络 RPC 单次默认 10 秒。预算到期触发停止与清理，只有确认结果后才终结 operation。

| 阶段 | 持久记录先于副作用 | 完成条件 | 失败补偿 |
| --- | --- | --- | --- |
| reserve | operation、tenant/node 容量 reservation | 控制面与 Node 原子准入成功 | 仅在确认无残留后释放对应占用 |
| materialize | 目标模板 digest、实例目录、两个盘路径 | 校验并原子完成模板和独立实例盘 | 删除登记的 staging/实例盘；保留共享模板 |
| network_prepare | netns/TAP/IP/策略 revision | 隔离规则已生效，Guest 尚未启动 | 默认拒绝后删除实例网络对象 |
| launch | unit 名、boot_id、socket 路径、CHV 版本 | 独立 unit 存活，CHV create/boot 成功 | 停止同身份 unit，不按宽泛进程名 kill |
| guest_ready | 目标 Guest major/能力与 identity | Guest Ready、workspace/cgroup 前置条件满足 | 停止 VM，清理运行态和新实例盘 |
| publish | 完整资源 inventory 与 observed revision | Node 落盘后，控制面 CAS 发布 running | 回复丢失由 Inspect 恢复，不销毁成功实例 |

实例盘未写入用户数据的 Create 失败可在停止 VMM 后回收。已经发布 running 的沙箱故障进入 error，不沿用“创建失败删除磁盘”的补偿；用户数据只能经 Delete 明确回收。

如果旧 Create 已被 Delete supersede，publish 阶段不得重新宣告 running；Node 转入删除收敛。所有清理步骤可重复且带 sandbox/generation 路径归属检查，已不存在即成功。

## 4. Stop、Start、Delete

### 4.1 Stop

1. 落盘 stopped 目标与 revision，拒绝新执行/文件修改。
2. 调用 CHV `PUT /api/v1/vm.power-button`，由模板的 ACPI/init 关机能力执行优雅停止；现有 GuestService 没有 Shutdown RPC，不编造该方法。宽限期内等待 Guest shutdown。CHV 路径以 [官方 OpenAPI](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/7913750ed89f25192d920d2a1cef8a8c80f638f0/vmm/src/api/openapi/cloud-hypervisor.yaml) 为依据，实际发行版需一并验证。
3. 到期后调用 CHV vm.shutdown；确认 VMM unit 退出，必要时 systemd TERM/KILL。CHV vm.shutdown 后可能回到 Created，不能只据此报告 stopped。
4. 停 VMM 后 fsync journal，清理 TAP/netns/路由、socket、运行 cgroup，保留 rootfs/workspace 和 template 引用。
5. 确认 unit inactive 后释放 CPU/RAM reservation，保留磁盘占用与 template pin；发布 stopped、记录 clean_shutdown 或 forced_stop。强杀后的文件系统仅承诺 crash recovery，不承诺应用一致性。

### 4.2 Start

仅在原 Node 上校验原盘、模板 pin、空间和容量，创建新的 boot_id，按“网络先隔离→VMM→Guest Ready”顺序冷启动；不重新复制模板覆盖原盘。启动失败保留原盘，运行资源回收后回到 error；可由新的 Start 重试。

已有 Guest 内安装和文件保留；进程、PTY、Guest epoch 和执行句柄不能恢复。磁盘损坏或模板工件缺失要明确报错，不用空盘替代。

### 4.3 Delete

落盘 deleted 目标→关闭准入/受控出口连接→停止 VM→确认 unit inactive→清理网络与 socket→删除登记实例盘与目录→释放模板 pin/磁盘配额→发布 deleted。

有任何清理结果未知时保持 deleting+cleanup_pending，不释放对应磁盘/资源占用；可释放已明确证实不存在的资源分项。节点不可达时不伪造 deleted。模板、他人的实例与独立快照不在清理集合。

删除是逻辑/文件系统回收，不承诺 SSD 物理安全擦除；日志按脱敏保留策略另行清理。deleted tombstone 最少保留 24 小时，之后 GET 可返回 404；租户鉴权优先于 tombstone 查询。

## 5. Journal、重启接管与 GC

Node SQLite 使用 WAL+synchronous=FULL，宿主本地持久目录；资源创建前写 intent，完成后写 observed inventory。记录至少含 unit、PID+启动时间、boot_id、两个 socket、磁盘 inode/路径、网络对象、CHV 版本、模板 digest、operation/revision、Guest identity。

启动接管流程：

1. 获取 Node 独占锁，迁移兼容 journal；不兼容版本拒绝服务并保留现存 unit。
2. 枚举 journal 关联的独立 systemd units，以 unit 元数据、PID 启动时间、CHV API 和磁盘配置共同匹配。只查 PID 或单个文件名不够。
3. running VM：重连 Ready，同 epoch 则恢复路由和订阅能力；Guest epoch 改变则旧执行失效，不自行重启 VM。
4. intent 有而资源不完整：按对应阶段继续/补偿。unit 存活但 Guest 不可达：保留盘，标 degraded/error，不默默杀掉重建。
5. journal 外的疑似 Isodo 资源进入 quarantine，记录操作员可审查清单；身份不明不自动删除。

reconcile 每 5 秒运行；Node 心跳每 5 秒上报，20 秒未收到时控制面标 unknown，恢复后用 generation/revision CAS 更新。GC 只回收 journal 明确登记且无 live unit/operation/pin 的临时资源；首次发现 orphan 至少隔离 10 分钟，不能通过“目录年龄大”判用户盘可删。

宿主重启后 unit 可能不再存在；首版不自动启动用户负载。原盘仍在时报告 stopped 或 error，并说明非正常停机，由用户 Start 恢复；不声称内存或进程恢复。

## 6. Nomad 与宿主生命周期

API/Gateway 是 service job；Node 是专用 KVM 节点上的 system job。VMM 由宿主独立 systemd unit 托管，不能留在 Nomad task cgroup；Node 任务的资源声明只覆盖自身开销。

Node 升级：关闭新准入、标 draining→flush journal→退出 Node→新版本获取同一独占锁→接管 existing units→健康后恢复调度。不能在退出 hook 对全部 VM 执行 kill；不能用 setsid/nohup 代替 cgroup 生命周期边界。

专用 VMM 非特权 UID、宿主 root-owned 工作目录、仅授予必要 KVM/磁盘/socket 权限。Node 所需的 netns/systemd 操作由受信任宿主权限执行，租户不能提交 unit 名、命令、宿主路径或任意 Nomad Job。

永久节点下线与 Node 二进制升级不同：先阻止新分配，等待用户 Stop/Delete 和存储回收；首版无自动迁移。回滚必须兼容 journal schema、Guest major、镜像 pin 和 VMM 版本，不兼容时保留 VM 并停止新准入。

## 7. 验收与完成标准

| 注入点/场景 | 验收 |
| --- | --- |
| Create 同键并发/回复丢失 | 一个 sandbox、一个 unit、一次容量预留 |
| 创建每阶段进程崩溃 | 重启后继续/补偿，无未登记磁盘、VMM、网络残留 |
| Create 中 Delete | 最终 deleted，不再发布 running、不重建被删实例 |
| Stop/Start | 两盘内容保留、进程消失、boot_id 更新 |
| shutdown API 成功但 unit 仍存活 | 不报告 stopped |
| Node SIGKILL/升级 | VM 与磁盘不受杀，重启接管同一身份 |
| journal 旧 PID 被复用 | 不接管/kill 其他进程 |
| Node 失联 | unknown，无第二 owner，删除不假成功 |
| 磁盘清理失败/满盘 | deleting+cleanup_pending，配额不提前释放 |
| 宿主重启 | 报告真实停机/错误，保留盘，不承诺进程恢复 |
| 未授权/跨租户 operation | 不泄露对象存在性，不可操作别人的实例 |

需要在独立测试 Nomad job/systemd unit 上执行，不能以已运行的 C 探针代替 Node 重启与滚动更新验收。
