# 参考项目与源码阅读路线

核对日期：2026-10-24（Asia/Shanghai）。本文记录上游依据，不表示 Isodo 已实现或完成兼容测试。源码树、README、LICENSE 通过 GitHub 连接器读取；下文单独标注实际阅读的实现。未运行这些上游项目，性能数据不作为 Isodo 承诺。

## 分层参考

| 参考项目 | Isodo 应学习的职责 | 已核实协议 | 边界 |
| --- | --- | --- | --- |
| Cocoon Engine | 节点 VM 生命周期、CHV 适配、快照与 clone、网络资源 | MIT | 当前已是 CHV 默认 + Firecracker 双后端；Isodo v0.1 只选择 CHV 即可 |
| Cocoon Agent | guest 基础 exec、stdin/stdout/stderr、退出码、clone 后 reseed | MIT | README 明确 PTY 尚在规划，不应把持久 shell/PTY 归给它 |
| Cocoon silkd | guest 产品能力：PTY、持久 shell session、文件与进程 | AGPL-3.0 | Rust/tokio 实现；其职责值得参考，但不是 Go guest-agent 的现成同语言实现 |
| Cocoon sandboxd | 节点 warm pool、claim 生命周期、guest RPC relay、重启 reconcile | AGPL-3.0 | per-node 控制面；全局账号、鉴权与放置仍需 Isodo 明确定义 |
| E2B Runtime | API / orchestrator / guest / edge 分层；模板构建、暂停恢复、数据模型 | Apache-2.0 | VM 后端是 Firecracker；不要把其设备、快照格式和恢复实现直接当 CHV 契约 |
| Cloud Hypervisor 官方 | VM API、snapshot/restore、vsock 语义 | 本文未单独评估全仓许可证 | 应作为 CHV 行为依据；实现前锁定实际发布版本和镜像 |

协议来源：[Cocoon LICENSE](https://github.com/cocoonstack/cocoon/blob/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5/LICENSE)、[Agent LICENSE](https://github.com/cocoonstack/cocoon-agent/blob/624c167778f3ab0db24884715b62c33fedb872e9/LICENSE)、[Sandbox LICENSE](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/LICENSE)、[Runtime LICENSE](https://github.com/e2b-dev/runtime/blob/ba31e2f42acd550a126e35ea2d1d7f89dce4800a/LICENSE)。Sandbox [README](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/README.md) 明确服务端为 AGPL-3.0，但 SDK 和 protocol/wire 单独为 Apache-2.0；不能用根目录协议概括所有子模块。实际引入代码时仍应记录来源、文件许可证与修改，不能把“参考设计”与“复制/修改代码”混为一项。

## 应先读哪些路径

### 1. Engine：节点如何拥有 VM

先读 [README.md](https://github.com/cocoonstack/cocoon/blob/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5/README.md)，再读以下源码入口（路径已核实，除特别标记外未逐函数审查）：

- [hypervisor/cloudhypervisor/create.go](https://github.com/cocoonstack/cocoon/blob/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5/hypervisor/cloudhypervisor/create.go)、[hypervisor/cloudhypervisor/start.go](https://github.com/cocoonstack/cocoon/blob/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5/hypervisor/cloudhypervisor/start.go)、[hypervisor/cloudhypervisor/stop.go](https://github.com/cocoonstack/cocoon/blob/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5/hypervisor/cloudhypervisor/stop.go)：创建、进程启动和清理。
- [hypervisor/cloudhypervisor/snapshot.go](https://github.com/cocoonstack/cocoon/blob/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5/hypervisor/cloudhypervisor/snapshot.go)、[hypervisor/cloudhypervisor/restore.go](https://github.com/cocoonstack/cocoon/blob/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5/hypervisor/cloudhypervisor/restore.go)：暂停、快照与恢复。
- [hypervisor/cloudhypervisor/clone.go](https://github.com/cocoonstack/cocoon/blob/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5/hypervisor/cloudhypervisor/clone.go)：**已读实现**；clone 更新磁盘与 socket 路径，使用 clone 独立临时 TAP，restore 后更换 NIC、设置 CPU quota，再 resume；失败路径调用 AbortLaunch。Isodo 的 clone 不能只是复制 snapshot 目录。
- [network/cni/lifecycle_linux.go](https://github.com/cocoonstack/cocoon/blob/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5/network/cni/lifecycle_linux.go)：CNI 资源生命周期入口；需进一步审查实际隔离与失败回滚。
- [snapshot/localfile/gc.go](https://github.com/cocoonstack/cocoon/blob/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5/snapshot/localfile/gc.go)：快照回收入口；Isodo 必须定义恢复中/运行中引用如何阻止删除。

### 2. Guest：基础通道与产品协议分别设计

[Agent README](https://github.com/cocoonstack/cocoon-agent/blob/624c167778f3ab0db24884715b62c33fedb872e9/README.md) 说明 vsock exec 和 reseed 的角色。[agent/protocol.go](https://github.com/cocoonstack/cocoon-agent/blob/624c167778f3ab0db24884715b62c33fedb872e9/agent/protocol.go) **已读实现**：换行 JSON；区分 started/stdout/stderr/exit/error；exit 与 error 是终止帧；单帧上限 8 MiB；输出写失败触发取消。它可作为 exec 协议状态机参考，不等于 Isodo 必须采用相同编码和上限。

Silkd 的进程/PTY 实现审计与 Isodo 落地规格见 [Guest Agent 运行时规格](guest-agent-runtime.md)，依据 [`exec.rs`](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/exec.rs)、[`pty.rs`](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/pty.rs)、[`proc.rs`](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/proc.rs)、[`server.rs`](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/server.rs)。持久 Session 与文件实现入口：[session.rs](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/session.rs)、[fs.rs](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/silkd/src/fs.rs)。Isodo 应按自身已经固定的断连、取消、输出回放、Session 生命周期和路径权限契约实现，不把 Cocoon 的行为直接当成相同语义。

### 3. Sandboxd：节点池与转发

[Sandbox README](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/README.md) 描述节点 warm pool、golden clone、HTTP upgrade 到 vsock 的 relay 以及重启 reconcile。重点入口：

- [sandboxd/pool/claim.go](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/sandboxd/pool/claim.go)：领取 warm 实例。
- [sandboxd/engine/engine.go](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/sandboxd/engine/engine.go)：节点引擎边界。
- [sandboxd/engine/silkd.go](https://github.com/cocoonstack/sandbox/blob/2654727931532a37f952bf8b7c44bcb8ce8b9cff/sandboxd/engine/silkd.go)：guest 通道适配。

不要同时把 sandboxd 自有 memberlist 放置和 Nomad 调度全量引入 v0.1。Isodo 必须明确谁选择节点、谁创建 VM、谁确认实例死亡，并为这三件事设唯一负责人。这是架构建议，尚未验证 Isodo 的具体实现。

### 4. E2B：学习完整平台边界

先读 [README.md](https://github.com/e2b-dev/runtime/blob/ba31e2f42acd550a126e35ea2d1d7f89dce4800a/README.md) 与 [架构入口](https://github.com/e2b-dev/runtime/blob/ba31e2f42acd550a126e35ea2d1d7f89dce4800a/docs/ARCHITECTURE.md)。当前仓库包含 packages/api、packages/orchestrator、packages/client-proxy 和 packages/envd，guest 不必按旧拆仓信息另找。

重点协议入口：[packages/envd/spec/process/process.proto](https://github.com/e2b-dev/runtime/blob/ba31e2f42acd550a126e35ea2d1d7f89dce4800a/packages/envd/spec/process/process.proto)、[packages/envd/spec/filesystem/filesystem.proto](https://github.com/e2b-dev/runtime/blob/ba31e2f42acd550a126e35ea2d1d7f89dce4800a/packages/envd/spec/filesystem/filesystem.proto)。以这些入口对照进程/文件操作契约；以 API / orchestrator / edge 分层检验 Isodo 的目录职责。上游 README 的 lazy restore、pause、fork 和流量唤醒是参考能力，本文没有独立执行验证，也不应全部列为 v0.1 必交付功能。

### 5. Cloud Hypervisor：底层事实来源

- [官方 OpenAPI 定义](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/7913750ed89f25192d920d2a1cef8a8c80f638f0/vmm/src/api/openapi/cloud-hypervisor.yaml)：路径已确认；生成/实现客户端前按选定发布版本重新锁定。
- [Snapshot/Restore 文档](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/7913750ed89f25192d920d2a1cef8a8c80f638f0/docs/snapshot_restore.md)：**已读文档**；先 pause，再 snapshot；restore 默认回到 paused，需要 resume；内存和设备状态文件不等同完整磁盘备份。Isodo 需将磁盘一致性及外部资源写入自己的 manifest/工作流。
- [VSOCK 文档](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/7913750ed89f25192d920d2a1cef8a8c80f638f0/docs/vsock.md)：**已读文档**；CHV 支持 stream socket；宿主连接 guest 通过 Unix socket 与 CONNECT 端口握手。vsock 是传输通道，不承担租户鉴权。
- 当前主线文档包含 copy、ondemand、copyonwrite 恢复模式，且 copyonwrite 要求源内存文件在 VM 全生命周期保持不变。不要据此推断已选发行版具备相同能力；先锁版本，再做恢复与回收测试。

## 可复核版本

以下为本次读取的 HEAD，不代表推荐发布版本：

| 仓库 | Commit |
| --- | --- |
| cocoonstack/cocoon | [`4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5`](https://github.com/cocoonstack/cocoon/commit/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5) |
| cocoonstack/cocoon-agent | [`624c167778f3ab0db24884715b62c33fedb872e9`](https://github.com/cocoonstack/cocoon-agent/commit/624c167778f3ab0db24884715b62c33fedb872e9) |
| cocoonstack/sandbox | [`2654727931532a37f952bf8b7c44bcb8ce8b9cff`](https://github.com/cocoonstack/sandbox/commit/2654727931532a37f952bf8b7c44bcb8ce8b9cff) |
| e2b-dev/runtime | [`ba31e2f42acd550a126e35ea2d1d7f89dce4800a`](https://github.com/e2b-dev/runtime/commit/ba31e2f42acd550a126e35ea2d1d7f89dce4800a) |
| cloud-hypervisor/cloud-hypervisor | [`7913750ed89f25192d920d2a1cef8a8c80f638f0`](https://github.com/cloud-hypervisor/cloud-hypervisor/commit/7913750ed89f25192d920d2a1cef8a8c80f638f0) |

## 纳入 specs 的最小决策

1. 采用一个 guest 产品服务接口；基础 exec 与 PTY/session 可以分模块，但不默认运行两个职责重叠的 guest daemon。
2. engine 只拥有本机 VM 和资源；平台 API 拥有租户、授权、配额与期望状态；node 服务负责 reconcile。Nomad 的具体职责必须在部署规格中单独固定。
3. snapshot 是带版本、磁盘引用、内存/设备状态、校验值与引用关系的制品；clone 另分配身份、网络和凭证。
4. 上游毫秒级性能描述只作研究线索；Isodo 验收分别测冷启动、warm claim、clone、恢复及失败清理。


## 本地参考源码

2026-10-24 准备完成。参考仓库位于 Isodo 同级目录，不作为产品源码或依赖引入。

| 本地目录 | 当前 HEAD | origin |
| --- | --- | --- |
| `/Users/drowning/Desktop/qiniu/reference.e2b-infra` | `6b82caf690b06fb983254714187dc496ac4d0cec` | https://github.com/e2b-dev/infra.git |
| `/Users/drowning/Desktop/qiniu/reference.cocoon` | `4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5` | https://github.com/cocoonstack/cocoon.git |
| `/Users/drowning/Desktop/qiniu/reference.cocoon-agent` | `624c167778f3ab0db24884715b62c33fedb872e9` | https://github.com/cocoonstack/cocoon-agent.git |
| `/Users/drowning/Desktop/qiniu/reference.cocoon-sandbox` | `2654727931532a37f952bf8b7c44bcb8ce8b9cff` | https://github.com/cocoonstack/sandbox.git |
| `/Users/drowning/Desktop/qiniu/reference.cloud-hypervisor` | `7913750ed89f25192d920d2a1cef8a8c80f638f0` | https://github.com/cloud-hypervisor/cloud-hypervisor.git |

E2B 使用用户提供的现有 checkout，未 fetch、切分支或修改；其 origin 仍是 `e2b-dev/infra`，本地 HEAD 与本文此前线上 `e2b-dev/runtime` 核对版本不同。后续源码结论应标明使用哪一版本，不把线上新目录结构直接套到本地。其余四个仓库为 `--depth 1` 浅克隆；silkd 和 sandboxd 同在 `reference.cocoon-sandbox` 中。需要追溯历史时再补取。

本次仅准备和核验 Git 工作区，没有运行上游构建或测试。后续代码发现先通过 codebase-memory-mcp 索引对应目录，再用图工具检索。
