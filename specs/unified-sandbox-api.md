# Sandbox 管理与 Files API 统一设计（修订待确认）

更新：2026-10-25。设计原则总体认可；当前仅授权最终计划/文档修订，实施仍待确认。执行顺序以[黑客松关键路径](../docs/plans/hackathon-unified-api.md)为准。

本版依据用户对架构 Review 的反馈收敛。历史发现见 [Review](../docs/reviews/sandbox-api-architecture-2026-10-25.md)，目录依赖证据见[目录职责](directory-responsibilities.md)，最小文件变更清单见[设计修订清单](../docs/reviews/sandbox-api-design-revision-2026-10-25.md)。历史报告中的 desired=deleted、公开 Environment API、默认不覆盖只是早期建议，均以本版重新裁定。

## 1. 本轮范围

Sandbox管理契约统一与远端关键链路预检并行：立即分别验证OAuth入口与本机Preview Host路由、CHV创建/出网策略、模型出站及凭证注入，不等App迁移完成；随后集成真实远端App/T3/Agent，再完成通用Files和配置迁移。基本fencing和必要身份校验随相关路径补齐；Delete最低真实清理、UID实测、Preview直接路径修补不能省略，但完整reconciliation等不作为统一API的全面前置工程。

保留 Control/Node/Guest、Guest gRPC、journal、PostgreSQL outbox、现有 T3/Preview 配对流程；CHV 的服务访问继续走 Vsock，不因本轮接口统一启用网卡或修改已有可选网络路径。

Commands、PTY、Sessions、通用 Ports API、公开 SDK 发布分别立项。仅消除当前风险，不为它们预建路由、服务或目录。App 所需统一 client 是内部调用边界，不宣称已提供 SDK。

本地旧接口直接替换；相关 App/IPC/退出暂停等调用方同批迁移，无双版本兼容 parser。Agent 配置使用通用写文件，撤除专用 agent-config 入口。

## 2. 分层和生成

```text
App/internal client
  ├─ cloud HTTP → Control：鉴权、归属、desired、outbox → Node mTLS RPC
  └─ Electron IPC → local Node HTTP：本地归属、journal、同构 DTO
Node runtime：VM 管理、revision/generation、实例准入、流转发
  ├─ Lima transport → Guest gRPC
  └─ CHV Vsock transport → Guest gRPC
Guest：Files/Process，独立于 T3 协议
```

已确认演示拓扑：API/Control、数据库、认证服务与现有Preview Gateway运行开发者本机；ngrok主要用于GitHub OAuth公网回调。App优先本地连接Control，Control→jfcs1024 Node通过SSH本地转发并验证mTLS。Preview优先验证本机Host路由，不要求公网通配子域名。Guest模型出站独立验收；4040管理接口不可达不构成Sandbox阻塞。

业务契约一致不等于内部实现相同。`api/openapi` 和 `api/proto` 是协议源；Go/TS 生成代码保持现有位置，生成脚本不在 Review 阶段运行。Files 无需 T3 pairing、Preview Cookie 或新 Environment API。

## 3. 最小 Sandbox DTO

保留 id/name/metadata/template references/resources/created_at/updated_at，补充：

```ts
type VMTarget = "running" | "stopped";
type VMObservation =
  | "requested" | "preparing" | "starting" | "running"
  | "stopping" | "stopped" | "error" | "unknown";
type ActionStatus = "accepted" | "running" | "succeeded" | "failed" | "superseded";
interface LastAction {
  revision: string;
  type: "create" | "start" | "stop" | "delete";
  status: ActionStatus;
  stage?: string;
  error?: { code: string; message: string; retryable: boolean; request_id?: string };
  updated_at: string;
}
```

PATCH name/metadata不改变生命周期revision，也不改变last_action；本轮不新增公开resource version，普通属性更新只更新updated_at。

Sandbox 的 revision 是递增控制意图编号，JSON 用字符串避免 JS 精度问题；last_action.revision 必须与它一致。status 描述动作，不允许使用 running/stopped VM 状态来代替整个动作枚举；其中动作 running 的语义是执行中，不是 VM 已运行。

desired_state 是用户目标，不在动作失败时回滚。observed_state 是最近确认的 VM 事实；error 只用于不能给出已确认事实的错误，不把“stop 失败但 VM 仍 running”强制写成 stopped。unknown 必须伴随 observed_at/安全错误信息，不推测运行状态。

运行实例可有 runtime_id（不透明），内部映射 ownership_generation、boot_id、guest_epoch；不向调用者开放内部 identity 自选权限。

## 4. 删除是资源清理，不是第三种 VM 目标

为简化契约，desired 仍只有 running/stopped；不将 deleted 放进 VM 目标，也不将 deleting/deleted 放进 VM observed 枚举。Delete 将 VM 目标设为 stopped，同时设置：

```ts
interface Deletion {
  requested_at: string;
  completed_at?: string;
}
// Sandbox.deletion?: Deletion
```

有 requested_at 表示删除意图已持久化，禁止新 start/写入/配对准入；completed_at 仅在资源清理确认后填写。未完成的删除失败原因由 last_action.error 提供，不额外维护同义 deletion.status/cleanup_pending 布尔组合。

现有 DB deleted_at 是逻辑隐藏标记，不作为公开 completed_at；实现需补清理完成事实，并避免直接重解释已有数据。默认列表隐藏已请求删除资源，同账号 GET 可查询 tombstone 至少 24 小时；未清理记录不因 TTL 淘汰。其他账号访问仍返回 404。

Delete 事务锁定资源、设置删除意图、递增 revision、写 outbox；worker 发 Node delete，关闭准入/流、停止 VM、清理实例盘、撤销 routing/注入，再提交 completed_at。删除 supersede 在途动作必须由 journal/fencing 保证资源不会被迟到动作重建。

未证明清理时保留容量占用/归属和失败原因，不能因列表不显示就宣称完成。实例 UUID 不重用。

## 5. HTTP、并发和 revision 等待

| 请求 | 成功响应 |
| --- | --- |
| GET /v1/sandboxes?limit&offset | 200 `{items,total,limit,offset}` |
| POST /v1/sandboxes | 202 Sandbox；Location 指向该 Sandbox |
| GET/PATCH /v1/sandboxes/{id} | 200 Sandbox |
| POST /v1/sandboxes/{id}/start 或 /stop | 接受 202；已完成同目标 no-op 200 |
| DELETE /v1/sandboxes/{id} | 新接受/清理中 202 Sandbox；已完成重复请求 204 |
| GET /v1/templates?scope=available&limit&offset | 200 TemplateList，只列可见且 default build Ready 的项 |
| POST /v1/sandboxes/{id}/app-link | 200 T3 SandboxAppLink，协议行为保持 |

创建 name 可选，缺省由服务端生成展示名，template 默认 base。分页 limit 默认100、最大100、offset非负；两端确定排序。Template count 与 items 使用相同可创建条件，创建时仍重新校验。

生命周期 Idempotency-Key 绑定账号+方法+路径+规范输入；同键不同输入409，同键重试查询/返回同一 revision 的动作，不产生新 job。幂等结果不只返回当前最新 revision：如果动作已被后续操作替代，返回明确 ACTION_SUPERSEDED 及原 revision/当前快照。保留24小时，进行中动作不淘汰。

同目标动作已执行中返回当前快照；冲突 start/stop 返回409 ACTION_IN_PROGRESS，避免隐式覆盖。Delete 可以升级目标，但只有在实现安全取消/串行化/fencing 后执行，不跳过在途资源清理。本地 Next/Apply/Commit 需要完整逻辑串行化，不依赖几个独立小锁。

每次等待都使用写响应的 target_revision：

1. GET revision 相同且 last_action.status 进行中：继续等待。
2. 相同 revision，动作 succeeded 且目标事实满足：成功；Delete 还要求 deletion.completed_at。
3. 相同 revision，failed：返回结构化动作错误；superseded：返回 ACTION_SUPERSEDED。
4. GET revision 更高：目标被替代，返回 ACTION_SUPERSEDED；不能因碰巧 observed=running 就认定旧 start 成功。无法比较的异常 revision 是契约错误。
5. 客户端超时/取消只结束等待，不取消已接受后台动作；错误含 target_revision 和最近快照。再次等待仍使用原 revision，不重新发创建。

waitUntilRunning 可默认等待5分钟，参数可调；服务端动作预算独立。服务端 timeout 是 failed，网络不可核实是 unknown，不混为一谈。SDK 未来隐藏 target_revision 的保存，但现阶段 App adapter 必须实现该规则。

worker 完成写入要同时 CAS sandbox_id、ownership_generation、revision、删除意图；旧动作只更新自己的 outbox/journal结果，不能写新目标。Node重启先核实原实例，Guest重启使旧runtime_id失效；无法恢复时 fail-closed/unknown，不重建第二台 VM。

## 6. Process 身份前置校验

实际启动证据：CHV `images/linux-base/isodo-agent` 使用 setpriv 1000:1000/clear-groups/no-new-privs；Lima `internal/node/lima/base.go` unit 使用 nobody/nogroup/NoNewPrivileges。Process 子进程继承 Guest Agent OS 身份，`USER=nobody` 环境字符串不是降权。

当前不能仅凭未设 SysProcAttr.Credential 推断 Guest 子进程为 root。优先补实际 uid/gid/groups/no-new-privs 验证、工作区所有权和读写权限验收；非标准 privileged 启动应拒绝对租户开放 Process，或明确隔离为受信任初始化路径，不增加公开 user/root 参数。

不为统一外部 API 强行统一两种镜像的数字 UID。若权限一致性测试证明必须迁移，再单列持久盘 ownership 迁移和 T3 回归，不在本轮直接全盘 chown。

## 7. Files：复用、覆盖和流

路由使用 `/v1/sandboxes/{id}/files` 的 POST multipart 写入、GET 二进制读取；stat/list/mkdir/move/remove 映射现有 Guest RPC，具体最小路由在 Files 契约冻结时一并确认。不要为基础写入附带 Shell 命令执行 API。

### 路径与配置

仅接受 `/workspace` 内规范绝对路径，保留 os.Root、symlink逃逸和目录竞态校验。配置 helper 沿用实际布局：HOME=/workspace/.home，CODEX_HOME=/workspace/.codex；`~/.codex/config.toml` 作为 Agent 表单路径由 helper 映射，不在 Files server 里做 Agent 特判。

不新增公开 Environment API。首版使用已验证 base 模板的布局常量与现有启动环境测试；只有将来确有可变布局再扩充已有 Sandbox capabilities/模板 metadata，届时单独评审。

保留 `.isodo`/`.t3` 等运行时内部目录的用户写入边界，配置目录允许显式写入。真实凭证只由 injection 管理；Files 不读取/解析/替换真实材料，不声称普通文件 API 可阻止用户上传任意秘密。

### 默认覆盖：overwrite=true（用户已确认）

事实：E2B envd POST /files 默认覆盖；Isodo 现有配置 helper 也发送 Overwrite=true。前份报告建议通用 SDK 默认 false，属于未经确认的行为改变，本版撤回该建议。

用户已确认通用Files默认overwrite=true，与现有配置/E2B常用行为一致，允许显式overwrite=false。新建、配置覆盖、overwrite=false冲突均需测试。本轮仅更新规格，没有修改实现。

### 长度、摘要、背压与完成

Guest WriteFile 首帧要求长度及 SHA256，chunk 校验offset/大小，末尾验证并原子提交。可重读文件先计算摘要，再用有界流上传；不可预计算流宿主有界磁盘暂存并计算，清理临时文件。HTTP multipart Content-Length 不是文件长度。

保留256MiB限制和既有Guest gRPC；不引入新上传任务资源、分片对象存储或envd。IPC使用chunk+ACK/credit或main进程句柄，单条消息大小有界；云端HTTP/gRPC逐段转发，取消/空闲超时/背压传递到底层。

完成只认 Guest 确认原子提交。COMMIT_UNKNOWN/丢回复时先查目标摘要，不自动重复覆盖；提交前取消保留旧文件，提交后取消不代表回滚。下载结束摘要来自现有ReadFile终帧，需保证适配层能提供完成校验：首版允许宿主有界暂存以兼容普通浏览器下载，不强制依赖HTTP trailer或重写Guest协议。

Files 不依赖T3配对或Preview Cookie，停止/删除要撤销准入和在途流，旧实例请求不得在重启后重新解析为新实例。

## 8. Preview/Relay 安全修订与后续阶段

现在只修现有入口：preview claim绑定具体运行实例、端口、期限；Node转发核实identity，Stop/Delete撤销路由与旧token。现有 generation=0 不继续作为公开受控入口的绕过方式。新boot/epoch不复用旧路由，权限拒绝不以获取新链接绕过。T3 cookie/bootstrap/descriptor/token/WebSocket协议保持，增加全新客户端和重启回归。

通用ports.getUrl API、Commands、PTY、Sessions及公开SDK是独立阶段，不在本轮增加占位路由、公开能力承诺或新服务目录。现有非交互Guest Process可复用；PTY/stdin/持久shell确实还缺实现，未来不能只用adapter冒充完成。

## 9. 执行门槛

先确认本版设计，再按最小变更清单实施。安全回归先于API批量迁移；管理与Files各自完成契约、handler和真实Lima/CHV验收。工作区先前实现草稿仍未通过完整迁移，本版不把它视作完成。

旧本地接口直接替换；T3和真实运行实例保持不受Review影响；无提交、推送、部署授权。
