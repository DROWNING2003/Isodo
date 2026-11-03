# 上游参考与许可证

以下为开发时核对的固定上游版本与许可证记录，不代表全部代码均被引入，也不构成 Isodo 的能力承诺。实际引用与修改应继续保留对应版权和许可证。

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

## 可复核版本

以下为本次读取的 HEAD，不代表推荐发布版本：

| 仓库 | Commit |
| --- | --- |
| cocoonstack/cocoon | [`4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5`](https://github.com/cocoonstack/cocoon/commit/4de05ae7c3cea3cda68e51ceca1dc4bf2644a1b5) |
| cocoonstack/cocoon-agent | [`624c167778f3ab0db24884715b62c33fedb872e9`](https://github.com/cocoonstack/cocoon-agent/commit/624c167778f3ab0db24884715b62c33fedb872e9) |
| cocoonstack/sandbox | [`2654727931532a37f952bf8b7c44bcb8ce8b9cff`](https://github.com/cocoonstack/sandbox/commit/2654727931532a37f952bf8b7c44bcb8ce8b9cff) |
| e2b-dev/runtime | [`ba31e2f42acd550a126e35ea2d1d7f89dce4800a`](https://github.com/e2b-dev/runtime/commit/ba31e2f42acd550a126e35ea2d1d7f89dce4800a) |
| cloud-hypervisor/cloud-hypervisor | [`7913750ed89f25192d920d2a1cef8a8c80f638f0`](https://github.com/cloud-hypervisor/cloud-hypervisor/commit/7913750ed89f25192d920d2a1cef8a8c80f638f0) |
