# E2B 服务行为调研

核对日期：2026-10-24。范围：网络默认值、Exec / PTY 断连、暂停与数据生命周期。仅做源码和官方文档核对，未部署运行 E2B，也未验证 Isodo 对应实现。

## 版本与证据边界

- 本地代码：`/Users/drowning/Desktop/qiniu/reference.e2b-infra`，origin `https://github.com/e2b-dev/infra.git`，HEAD `6b82caf690b06fb983254714187dc496ac4d0cec`，核对时工作区干净。
- Graph 原有 `reference.e2b-infra` 索引位置与当前文件不一致，已重新索引为 `Users-drowning-Desktop-qiniu-reference.e2b-infra`；下述结论以当前文件为准。
- 官方 SDK / 文档是当日线上版本，不假定与本地 runtime commit 严格配套；SDK 网页 `main` 链接不是固定版本证据。

## 已确认行为

| 问题 | E2B 行为 | 对 Isodo 的意义 |
| --- | --- | --- |
| 默认联网 | 默认允许公网出口；可关闭，也可配置 allow / deny。`allowInternetAccess=false` 转成 deny-all CIDR，并保留显式 allowed addresses。 | 默认禁网不是 E2B 惯例；公开联网能力与凭证代理应分开建模。Isodo 是否采用公网默认值是自身设计，不能称为直接继承安全边界。 |
| Exec 断连 | 进程使用独立于请求的 context，客户端取消不会直接终止进程；执行超时仍有效。 | 区分执行期限与连接期限；断连清理订阅，不等于杀进程。 |
| PTY 断连 | PTY 也通过同一个进程服务创建，`pty.StartWithSize` 使用进程 context。 | PTY 会话可以与连接分离；不必另造“断线即销毁”默认语义。 |
| 重新连接 | 按 PID / tag 查找现存进程，重新订阅事件。 | 需要明确进程身份与 attach，不必将所有执行绑定一个 WebSocket。 |
| 输出与退出码恢复 | 源码只保留退出事件 30 秒，明确未实现丢失输出的完整 replay。 | “可重连”不等于“完整历史可重放”。Isodo 若提供持久日志，需要额外实现与限额。 |
| Pause / Resume | Pause 默认保存内存和文件系统；显式请求可只保存文件系统，恢复时冷启动。 | 内存恢复与磁盘冷启动是两个能力，不应混成一个 Stop / Start 承诺。 |
| Kill | 官方文档把 kill 定义为终态，资源释放，不可 resume。 | Kill / Delete 不能同时隐含保留同一沙箱供重新启动。独立导出的快照与卷应另订生命周期。 |

### 网络源码与官方说明

[`buildNetworkConfig`，create_instance.go](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/api/internal/orchestrator/create_instance.go#L181) 初始化空 egress，只有明确传 false 才追加 deny-all；注释明确保留用户的 allowed addresses。[官方网络说明](https://docs.e2b.dev/network/internet-access)确认默认公网出口开启。

域名控制有协议边界：官方说明使用 HTTP Host / TLS SNI，覆盖 80 / 443，其他端口按 CIDR 判断，不能泛化为所有协议的域名隔离。本文未完成内网、宿主和元数据目的地址的全链路防火墙审计，不能以“默认允许公网”推导“所有私网均已验证阻断”。

### 执行、PTY 与重连源码

[`handleStart`](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/envd/internal/services/process/start.go#L23) 创建 `context.Background()`，有执行超时才包 `WithTimeout`，并把该 context 交给 handler；流取消释放订阅。[`handler.New`](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/envd/internal/services/process/handler/handler.go#L213) 用它构造 `exec.CommandContext`，PTY 分支使用 [`pty.StartWithSize`](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/envd/internal/services/process/handler/handler.go#L305)。

[`handleConnect`](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/envd/internal/services/process/connect.go#L19) 重新寻找进程并订阅事件；[`terminatedRetentionTTL`](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/envd/internal/services/process/service.go#L21) 明确只缓存退出事件 30 秒，未实现完整丢失输出回放。

[官方后台命令文档](https://docs.e2b.dev/commands/background)说明 background 命令可在 SDK 断连后继续，默认命令超时为 60 秒，设置 0 可关闭；重新 connect 的 timeout 控制客户端等待。长期任务建议输出到文件；异常断网且大量输出可能造成阻塞，不能仅靠进程独立 context 宣称无限输出永不阻塞。[SDK CommandHandle](https://github.com/e2b-dev/E2B/blob/main/packages/js-sdk/src/sandbox/commands/commandHandle.ts)也将 disconnect 与 kill 分开，前者停止事件订阅，后者发终止请求。

### 暂停与保留源码

[`PostSandboxesSandboxIDPause`](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/api/internal/handlers/sandbox_pause.go#L67) 明确请求体缺省为完整内存快照，`memory:false` 为 filesystem-only。不能把旧文档中的“Pause 保存全部内存”当成唯一模式。[官方持久化说明](https://docs.e2b.dev/sandbox/persistence)确认默认 full、可选 filesystem，并区分 running / paused / snapshotting / killed；暂停保留与删除是不同操作。

## 建议落到 Isodo 规格的常规契约

1. Exec 和 PTY 的生命周期独立于订阅连接；分别定义创建、attach、输入、resize、signal、退出和结果查询。
2. 明确 command timeout、API timeout、流连接 timeout、sandbox TTL 四者的独立作用。
3. 明确输出保存上限、重连起点、截断标志与退出结果保留期限；不要默认承诺 E2B 本身也没有的无限回放。
4. 普通出口策略与密钥使用授权分离；默认值可由 Isodo 安全定位选择，但写明是本项目取舍。
5. 分别描述停止后磁盘保留、完整内存暂停、不可恢复删除；首版未实现的模式明确拒绝，不能静默降级。

尚未验证：Isodo PTY 真机行为、网络隔离与绕过、背压、Node 重启后的进程接管、CHV 内存快照兼容性。这里的 E2B 源码证据不是这些能力的验收结果。
