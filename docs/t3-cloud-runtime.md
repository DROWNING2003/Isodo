# T3 云端运行时：模块、边界与验证

状态：2026-10-25。本文描述当前代码实际具备的本地/云端 T3 链路、已验证证据和明确未实现的能力。运行步骤见 [cloud-t3 runbook](runbooks/cloud-t3.md)，协议细节见 [sandbox-endpoints](../specs/sandbox-endpoints.md)。

## 目标与范围

用户可以在创建沙箱时选择本地或云端运行位置；T3 作为沙箱提供的服务，沙箱内的终端、文件浏览/上传和 Agent 会话都由 T3 工作台承载。Isodo 控制面不把 Task 当成 Sandbox，也不把 T3 当成独立服务实例。

当前实现覆盖：

- 本地 Lima 沙箱：Guest Ready 后启动 T3，沙箱卡片进入配对登录。
- 云端 CHV 沙箱：不可变 `t3-base` 模板、独立 rootfs/workspace、Guest Agent、T3 Server + Web client、Codex/Claude CLI。
- 云端应用链接：`POST /v1/sandboxes/{sandboxID}/app-link` 返回带 pairing fragment 的 URL；Preview Gateway 用 host-scoped signed cookie 把浏览器反代到 Node relay，再到 Guest `127.0.0.1:8080`。
- 生命周期：Create/Stop/Start/Delete 的 Node 后端语义；Stop 保盘，Start 冷启动并产生新 boot ID/guest epoch。
- T3 capability：app-link 返回 `["terminal","files"]`；pairing credential 的 scopes 含 `terminal:operate`。

明确不在本文范围：真实 Casdoor 登录、真实 API/Node 独立进程的 mTLS E2E、Connection Gateway 的凭证注入、warm snapshot 与产品快照。

## 模块与职责

| 模块 | 代码位置 | 职责 | 边界 |
| --- | --- | --- | --- |
| Control API | `cmd/isodo-api`, `internal/control/sandbox`, `internal/httpapi` | 鉴权、沙箱 CRUD、app-link、Preview Gateway 入口 | 不直接访问 CHV socket 或节点磁盘 |
| Control Worker | `internal/control/sandboxworker`, `internal/control/operation` | 领取 create operation，通过 mTLS gRPC 调 Node，回写 observed state | 不把运行态 VM 当成 Nomad Job |
| PostgreSQL | `internal/control/postgres`, `migrations` | 租户、沙箱期望/观测状态、operation、模板 catalog | 不是 VM 真实状态 |
| Node Orchestrator | `internal/node/orchestrator` | CHV 生命周期、journal、容量、app relay、Guest 连接 | 只信任本机 CHV API socket |
| Template Runtime | `internal/node/templatebuilder` | 校验模板、复制实例盘、启动 CHV、Guest Ready、BindRuntime、Restart | 实例盘不可复用给其他 Sandbox |
| Guest Agent | `internal/guest/agent`, `internal/api/guest/v1` | Exec · PTY · Files · PortForward | 不持有 CHV 控制权或平台长期凭证 |
| T3 | `images/t3-base` | Node 24 + T3 Server/Web + CLI，监听 Guest `127.0.0.1:8080` | 以 uid 1000 运行，不读取宿主秘密 |
| Preview Gateway | `internal/control/preview` | signed token、cookie、host 路由、反代 | 不把 token 发到上游；上游 cookie 保留 |
| Registry | `internal/templateartifact`, `scripts/publish-t3-template.sh` | 不可变模板 artifact 的 S3/DB 发布 | 发布失败不能伪装成 Ready |

## 请求与信任流

```text
Browser
  │  POST /v1/sandboxes/{id}/app-link (Casdoor bearer)
  ▼
Control API ── sandbox.Handler ── appLinkIssuer (Node client)
  │                                      │ mTLS gRPC CreateSandboxAppLink
  │                                      ▼
  │                              Node CHVServer
  │                                ├─ loopback appRelay (127.0.0.1)
  │                                ├─ Vsock PortForward → Guest 127.0.0.1:8080
  │                                └─ pairing CLI → fragment token
  ▼
preview.Publisher
  ├─ public URL  http://8080-<id>.localhost:<api-port>/
  └─ pairing URL .../_isodo/auth/<signed>?next=/pair#token=...
  ▼
Browser ── /_isodo/auth/... ── Set-Cookie isodo_preview (host-scoped)
  └─ GET / with cookie ── Preview Gateway ── relay ── T3
```

安全边界：

1. `isodo_preview` 与 `isodo_session` 不转发给上游；T3 自己的 cookie 保留。
2. pairing token 只在 fragment，不出现在 API 日志或上游请求。
3. Node relay 只监听 `127.0.0.1`；公网入口只能是 API/Preview Gateway。
4. Guest 内 T3 以非 root 运行；CLI 凭证由 T3 pairing 产生，不来自 Control DB。
5. Stop/Start 的 boot ID/guest epoch 由 Node 生成；旧 app link 在 Stop 时关闭。

## 已验证证据（真实 CHV）

`jfcs1024`（Linux 5.15 + KVM + CHV v54），`t3-base-003`（digest `78d5b97aee4fa553c9de3cf1c05a7595d4547ac7aee6debf961318c8158e9366`）与 `t3-base-005`（4 GiB + virtio-net，digest `774db6cc221b24207f4e77be752d8e837bb34240dfcc08aa6f3304cb76261dbd`）：

| 探针 | 结果 |
| --- | --- |
| `probe` | Guest Ready 1518ms |
| `probe-t3` | T3 HTTP 200，`boot_to_t3_ms=18140`，pairing，`terminal:operate` |
| `probe-node-t3` | create→stop→start→T3 200；`create_to_app_link_ms=39580`，`restart_to_app_link_ms=11882`，新 boot ID/epoch |
| `probe-api-t3` | `POST /app-link` 200，signed cookie → 多宿主 relay → T3 200，capabilities `["terminal","files"]`，stop/delete |
| `probe-t3`（`t3-base-005`） | CHV egress：`1.1.1.1` 301、`www.cloudflare.com` 200；T3 200 |
| `publish-system` + `probe-node` | 本地 SeaweedFS/PostgreSQL 发布 `t3-base`，registry 拉取、克隆、cache reuse |

完整 JSON 与限制见 [验证记录](validation/t3-base-chv-20261025.md)。

## 未实现与风险

- **Registry 发布**：已在 jfcs1024 的临时 SeaweedFS/PostgreSQL 上发布 `t3-base-005` 并用 `probe-node` 从 S3 拉取验证；isodo-test 的 8G 发布尝试导致主机 I/O 失联，生产 registry 仍需在健康主机重发 4G 工件。
- **公开 start/stop**：`POST /start`、`/stop`、Store/worker/migration 和 UI 按钮已实现，HTTP 单测覆盖 202/404/409；尚未在真实 Casdoor/PostgreSQL/API/Node 进程中做端到端。
- **多宿主**：Node `RelayPort` bidi gRPC、Control `relayConn` 和 Preview relay dialer 已实现；`probe-api-t3` 使用真实 Guest relay，gRPC 帧有单测，但尚未启动两个独立 API/Node 进程做 mTLS E2E。
- **CHV 出网**：`isodo0` bridge/NAT + Guest virtio-net 已实现并验证；私有地址/metadata 被拒绝。jfcs1024 宿主上游可访问 Anthropic/Google，但 OpenAI 被网络侧阻断，Codex 的 OpenAI 调用仍需宿主代理或 Connection Gateway；凭证注入 Gateway 尚未实现。
- **warm snapshot**：jfcs1024 的 5.15 内核没有 VMGENID，4G 候选是冷启动 initramfs；需要带 VMGENID 的 guest kernel 才能做 warm snapshot。
- **真实浏览器**：`probe-api-t3` 使用 fake auth/store，不依赖 Casdoor/PostgreSQL；尚未在真实登录 + 真实 API/Node 进程中跑浏览器闭环。

## 分工

个人作品。产品决策、代码实现和真实验证由用户与 AI 编码助手协作完成；提交记录以仓库 Git/PR 为准。本文只陈述可核对的事实，不把 mock 或探针结果说成完整产品能力。
