# 模板服务与构建系统规格 v0.1

状态：模板 Registry、个人 public/private 权限、Build 队列和 tag/fork API 已开始实现；Lima 本机 Base 已可内置 Isodo Guest Agent，Sandbox 可从 Base 克隆并完成 Guest Ready、Exec（非交互）与 Files 基础往返。模板 Build Worker/派生模板选择、独立工作区盘、严格路径竞态防护、完整 Files 幂等与 PTY/stdin 尚未完成，因此当前 Agent 不报告 `exec.v1`、`files.v1` 或 `pty.v1` 能力。实现基线优先参考本地 E2B `reference.e2b-infra` commit `6b82caf690b06fb983254714187dc496ac4d0cec`，但产物格式与运行时采用 Isodo 的 Linux、Cloud Hypervisor 和 Guest Agent。Isodo v1 不做团队/组织模板。

本文定义“模板是什么、归谁、如何构建、如何被 Sandbox 使用”。预热 CHV 内存快照是模板产物的一种启动加速工件，详见[预热模板与快照启动](warm-template-startup.md)；不能把快照文件直接等同于模板资源。

## 1. 目标

- 提供平台默认模板和个人模板；个人模板由创建者拥有，可选择公开或私有。
- 将 Isodo Guest Agent 随系统基础模板交付，让新模板和 Sandbox 启动后都能通过 Node ↔ Guest 协议工作。
- 支持私有模板和公开模板；模板的所有权、可见性、构建版本和 Sandbox 使用版本分开建模。
- 支持从系统模板、公开模板或已有模板构建新模板；每次 Sandbox 创建固定到一个确定的 Ready build。
- 先支持单 Node、Linux amd64、CHV 和节点本地构建/缓存；SeaweedFS 通过 S3 保存已验证的模板工件，Node 按 digest 下载并校验本地缓存。

非目标：公开的产品内存快照 API、跨架构镜像转换、任意 Docker daemon 访问、用户自定义内核、Firecracker 后端、桌面模板市场。

## 2. E2B 源码调研结论

以本文件状态行中的本地 E2B commit 为准：

1. E2B 的模板记录以 `TeamID` 为 owner，并用 `public` 布尔值描述是否能跨 Team 使用；个人账号也通过自己的默认 Team 持有资源。Isodo 借鉴其模板/Build 分离、访问控制和构建生命周期，但 v1 直接以个人账户为 owner，不引入 Team、组织成员或角色。
2. 模板身份和构建是不同实体。模板具有稳定 Template ID；每次构建有自己的 Build ID/status；tag（包含 `default`）指向可运行的 build。Sandbox 创建解析模板名/alias 和 tag，检查 owner 或 public 权限，再用选定 build 创建实例。
3. “系统默认模板”和 build tag `default` 是两件事。E2B 用单独的 `env_defaults` 关系标记平台默认模板；模板本身仍有归属和 public 策略。Isodo 采用独立的系统模板目录，不复用 build tag 表达系统身份。
4. 构建是异步工作流。API 先登记 Build 并返回 build ID，Template Manager 执行源镜像/源模板准备、配置和构建，之后同步完成状态并使模板/构建缓存失效。
5. E2B 中 private 模板不能跨 Team 访问；Isodo 对应为仅模板 owner 可读写/使用私有模板，其他用户通过列表、ID、alias 或 build ID 访问时统一隐藏资源是否存在。公开模板允许其他已认证用户发现和使用，但不能修改源模板。

关键实现位置：

- `packages/api/internal/template-manager/create_template.go`：为 Team、Template、Build 创建构建配置，调用 Template Manager 并异步同步状态。
- `packages/api/internal/cache/templates/cache.go`：按模板/Build tag 读取，并在 TeamID 不同且 `Public=false` 时拒绝访问。
- `packages/api/internal/handlers/sandbox_create.go`：Sandbox 创建时解析 alias/tag、校验模板访问并选定 build。
- `packages/db/pkg/dashboard/sql_queries/templates/list_team_templates.sql`：Team 模板与系统默认模板列表合并；默认模板关联与 `public` 策略独立。
- `packages/db/pkg/dashboard/sql_queries/templates/get_default_templates.sql`：通过单独的 `env_defaults` 关系读取默认目录。
- `packages/api/internal/handlers/auth.go`：模板别名/Team 归属检查。

## 3. 资源模型

### 3.1 Principal 与个人归属

Isodo 将 OAuth subject 映射到稳定 `user_id`，并通过现有身份上下文解析其账户 `tenant_id`。v1 不提供组织、Team/Tenant membership、成员角色或切换工作空间。个人模板以账户为 owner；API Key 仍固定属于认证时解析出的 tenant，不允许请求体覆盖 tenant。

当前身份存储把 OAuth identity 映射到一个 `tenant_id`，适合作为 v1 账户边界。模板表保存 `owner_tenant_id`，同时记录 `created_by` 作为审计字段；服务端从认证上下文取得 owner，不能信任请求体里的 owner。系统模板由平台 service principal 管理，不能通过普通用户 API 改写。

### 3.2 Template

| 字段 | 语义 |
| --- | --- |
| `template_id` | 平台生成、全局唯一、稳定标识；外部可用 ID 或有 namespace 的 alias 引用 |
| `owner_tenant_id` | 个人账户的 tenant；系统模板使用受保护的 system owner/scope |
| `scope` | `system` 或 `personal`；不从名称、alias 或 public 推断 |
| `visibility` | 个人模板为 `private` 或 `public`；决定其他用户能否发现/使用，不改变 owner；system 模板固定公开只读 |
| `name`, `description` | 个人模板默认只对 owner 展示；public 时可供其他用户发现；名称可修改，ID 不变 |
| `source_template_id`, `source_build_id` | 可选来源；派生模板固定引用来源的不可变 build |
| `created_by`, timestamps` | 审计创建者与生命周期 |
| `state` | `active`、`deleting`、`deleted`；删除前处理 tag 和构建引用 |

系统模板是只读目录资源，至少包含一个由平台构建的 Linux base template；manifest 必须标明内核/rootfs 版本、架构、guest-agent 版本、协议 major/capabilities、CHV 兼容范围、disk layout 和 digest。系统 default 由专门的 `system_template_catalog` 映射到模板及 Ready build，不允许个人 Tenant 抢占相同 alias。

个人模板属于创建者的账户 tenant，可设为 private/public。私有模板只有 owner 可查看、构建、派生和删除；公开模板可由其他已认证用户发现、查看公开 metadata 并用于创建 Sandbox，但只有 owner 可编辑、更新 visibility/tag 或删除。v1 不支持团队、组织或共享私有模板。

### 3.3 Template Build、tag 与 Artifact

Build 是不可变的一次构建尝试，至少包括 `build_id`、`template_id`、`source_build_id`、recipe digest、资源参数、执行状态、错误摘要、创建者与时间。状态：`queued → building → validating → ready`，失败为 `failed`；删除为单独的资源清理流程，不覆写历史结果。

模板 build 成功后生成 manifest，并引用只读 OS/rootfs/模板盘工件；用户定制层按 digest 固定。CHV VM memory/device snapshot 作为可选加速工件引用同一 build，不是唯一可启动数据源。manifest 至少校验完整文件集合、SHA-256、架构、CHV 兼容范围、启动配置和 Guest Agent 能力。构建秘密在 build VM 销毁前清理，禁止进入 rootfs、Guest Agent memory snapshot、manifest、构建日志或缓存 key。

Tag 是 `(template_id, name) → build_id` 的可变指针；`default` 是约定的默认 build tag，不代表平台系统模板。只允许将 Ready build 绑定到用户 tag；指针更新需原子完成。Sandbox 创建时先解析一次 alias/tag，并持久化最终 `build_id`，此 Sandbox 后续恢复不得跟随 tag 漂移。删除 build/tag 不能破坏已创建 Sandbox 对固定 artifact 的引用。

### 3.4 System Default Template

平台安装/升级流程管理系统目录；普通 API 不创建、不更新、不删除 system scope 资源。至少提供 `linux-base`，其 guest image 已包含 Isodo Guest Agent，并在无 Sandbox RuntimeIdentity、无用户进程/会话、无租户凭证时通过 template-ready 检查。

系统模板升级创建新的不可变 Build，再由平台在验证通过后更新 catalog 指针；已有 Sandbox 和用户派生模板固定在原 build。用户创建模板时可以指定 system template/build 作为 source，构建输出归用户 Tenant 所有。

## 4. 权限规则

1. 用户 Web/API Key 请求都从服务端认证上下文解析唯一个人账户 tenant，不支持客户端选择 owner/active Tenant。
2. API Key 身份固定 Tenant。`tenant_id`、owner、scope 等字段不接受客户端覆盖。
3. private 模板只能由 owner 读取、构建 Sandbox 和派生；其他用户对 private 模板 ID、alias、build ID 的读取统一表现为 `404 TEMPLATE_NOT_FOUND`，避免资源探测。
4. public 模板可被其他已认证用户列出和用于 Create Sandbox；源模板的构建日志、环境变量值、registry secret、用户 metadata 不随公开性暴露。
5. system 模板对所有 Tenant 可用、只读；构建历史、平台内网路径和节点地址不对用户暴露。
6. 将 private 转 public 前，服务端必须验证模板清单不含秘密引用值、租户数据和构建日志；v1 若无法做该验证，拒绝公开，而不是仅翻转 visibility。
7. 创建 Sandbox 前重新鉴权并固定 build；不能依赖缓存中的过期 public/owner 授权。缓存失效只影响性能，不改变授权源。

## 5. 构建与发布生命周期

```text
Template API -> DB: Template + queued Build + durable operation
             -> Build Worker / Node
                -> select immutable source build
                -> boot isolated builder VM with Isodo Guest Agent
                -> apply recipe / user steps
                -> scrub secrets and transient processes
                -> validate guest-ready template state
                -> capture disk/rootfs artifact and optional CHV snapshot
                -> hash manifest, persist build artifacts
             -> DB: Build ready, update selected tag atomically
```

- `CreateTemplate` 先创建个人模板元数据；`CreateTemplateBuild` 原子登记 Build/operation ID 和 recipe，再由 Worker 异步调度。HTTP 请求断开不取消已接收构建。
- 同一 Template 同时允许多个 build attempt；每个 attempt 独立 Build ID，失败不覆盖前一个 Ready build。并发默认 tag 发布用 compare-and-swap，显式处理冲突。
- Builder 使用一次性 Sandbox/节点临时目录；不挂载宿主用户目录。registry 凭证通过短期 secret channel 注入，生命周期结束立即销毁。
- recipe 的初版只允许 `from_template`、经 allowlist 的 OCI 镜像来源、明确 argv 命令和资源参数；模板命令运行在隔离 VM 内的 workload 用户下。任意 host path、宿主 socket、设备映射、宿主网络代理凭证均拒绝。
- Build ready 必须晚于 artifact fsync、manifest digest 校验、Guest template-ready 检查和注册表提交。任何验证失败保留失败状态，不发布 tag。
- 启动 template build 的 Node / Build Worker 不等同 Sandbox runtime Node；首版可以同一进程部署，但接口、资源配额、网络策略和清理责任分开。

### 5.1 Worker 租约、发布与恢复

- PostgreSQL Build 是持久任务。Worker 使用 `FOR UPDATE ... SKIP LOCKED` 领取，默认租约 30 秒、每 10 秒续租；同一模板的 `building/validating` 由部分唯一索引限制为一个活跃构建。
- Builder、校验和 S3 上传都受独立构建 deadline 约束。续租失败立即取消构建上下文；`validating/ready/failed` 更新还必须匹配 lease token 和未过期租约，旧 Worker 不能发布。
- 失联超过租约的 Build 进入 `failed`，保留原 Build ID 与失败原因；重新请求会创建新的 Build ID，不在未知 VM 状态下重跑同一个 attempt。
- 接收 Build 时固定 source Build 与当时的目标 tag 指针。工件完整上传后，在同一事务中更新 Ready 与 tag；若 tag 已被用户/另一构建改动，保留新的 Ready Build，保留当前 tag，并向 owner 返回 `publication_error`。公开使用者不读取发布诊断。
- 校验/上传失败不写 Ready，不移动 tag。公开的失败摘要只报告阶段，Worker 内部错误、宿主路径和存储凭证不直接进入 API 响应。

### 5.2 平台基础模板候选工件

`images/linux-base/` 在受控构建上下文中内置 Isodo Guest Agent；上下文仅包含 Dockerfile、启动脚本与待打包的 Agent binary。Ubuntu 镜像以实际 OCI digest 固定，包清单保存在 guest image。`scripts/build-base-template.sh` 在 Linux 上生成 8 GiB raw ext4 rootfs、对应 kernel、最小 initramfs 与 manifest；kernel 模块与 kernel 来自同一版本，不复制宿主 initramfs 中的机器配置。

Guest Agent 使用 UID/GID 1000、独立工作区盘和受控 PID 1 启动。候选模板不包含 Sandbox identity。当前 Guest 只声明已经确认的 capability；完整进程资源边界、文件路径竞态和 PTY 尚须按 Guest 规格完成后才能声明相应能力。

CHV v54 的磁盘 API 显式指定 `image_type: Raw`。Guest init 在降权前将 `/dev/vsock` 设为 `root:1000 / 0660`，Agent 保持普通用户运行。宿主 Vsock relay 握手单独限制为 1 秒，避免 Guest 尚未监听时占满 gRPC 的默认连接期限；握手成功后清除临时 socket deadline，后续 RPC 使用各自的调用上下文。

`cmd/isodo-template` 的 `verify` 检查工件完整性；`probe` 从写盘副本启动短期 CHV VM、检查无绑定身份的 Guest Ready 与版本/capability，并回收 VM。这两个命令不将候选工件登记为 Registry Ready；`publish-system` 强制执行探针、S3 发布与系统目录登记事务；运维替换已有目录项必须传入当前 build ID，避免并发覆盖。完整个人模板 Builder 与 Sandbox 创建接入仍需独立验证。

Guest Agent 的构建约束：Base 镜像内存在 Agent binary 与 systemd/init 配置；模板 Builder 使用 `ISODO_TEMPLATE_MODE=true`；构建完成快照前保证 Agent 未绑定运行时身份。Agent 的启动探针必须返回协议版本和真实能力集。当前 Lima Base/Guest Ready、非交互 Exec 和基础 Files 调用已有源码及真实 VM 证据，但 Ready 只声明 `ready.v1`；直到工作区隔离、路径安全、Files 幂等、PTY/stdin 和能力协商完成前，不得将其标为可供用户模板使用的完整 Guest 协议实现。

## 6. API 草案

HTTP 路由统一采用 Isodo `/v1` 命名；接口的模板/Build 分离、异步构建、tag 指向 Ready build、私有访问隐藏和公开使用授权，以 E2B 源码行为为依据。字段/能力差异在对应契约中明确描述。

所有用户接口纳入根目录 `api/openapi/openapi-public.yaml`，通过 `make generate` 生成 Go handler types、SDK types 和 docs。API Key 使用 `templates:read`、`templates:write`，并继续检查模板 owner；管理 API Key 的控制面接口放在 `openapi.yaml`。sqlc 查询也统一由 `make generate` 生成。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/v1/system-templates` | 查看平台只读默认目录及 Ready build |
| `GET` | `/v1/templates` | 默认列出当前用户拥有的模板；`scope=owned/public/system/available` 切换列表，使用 `limit`、`offset` 分页 |
| `POST` | `/v1/templates` | 创建个人模板元数据，visibility 默认 private |
| `GET` | `/v1/templates/{template_id}` | owner 或 public 模板使用者读取允许公开的元数据 |
| `PATCH` | `/v1/templates/{template_id}` | owner 更新 name、description、visibility |
| `DELETE` | `/v1/templates/{template_id}` | owner 发起软删除；被活跃 Sandbox/build 引用时进入 deleting |
| `POST` | `/v1/templates/{template_id}/builds` | 从固定 source/build 启动新的异步构建 |
| `GET` | `/v1/templates/{template_id}/builds/{build_id}` | 查询单次构建状态和公开错误摘要 |
| `GET/PUT/DELETE` | `/v1/templates/{template_id}/tags/{tag}` | 查询/原子更新/删除 build 指针 |
| `POST` | `/v1/templates/{template_id}/fork` | 从 system/public/owner 可访问模板的固定 build 派生当前用户自己的模板 |

Template 创建/构建请求只接受逻辑源 ID、资源参数、recipe 和 tag 发布意图，不接收 CHV snapshot host path、node socket、文件 URL 或对象存储内部 key。Build 响应返回 `build_id` 与 `operation_id`，不是假报 build ready。

Sandbox Create 使用可选 `template` 引用：系统目录名称或模板 UUID，可追加 `:tag`；省略时选 `linux-base:default`。服务端在一个授权流程中解析 template、owner/visibility 和 build，保存解析后的 `template_id + build_id + artifact_digest`，Node 只接收固定后的内部运行引用。

## 7. Isodo 实施顺序

1. **个人归属与授权**：复用 OAuth user→tenant 映射，不引入 Team membership/role；模板 owner 来自认证上下文，API Key tenant binding 保持不变。
2. **Template Registry**：DB migrations/sqlc、system catalog、Template CRUD、public/private 授权、private not-found masking、alias uniqueness 和 OpenAPI。
3. **Guest-enabled base build**：构建可复现 Linux image，内置 Isodo Guest Agent 与 manifest；template-ready 不带 Sandbox identity。
4. **Build lifecycle**：持久 Build/Operation、单 Node Worker、status/log、可重试失败、原子 tag promotion、artifact manifest 与清理。
5. **Sandbox from template**：Sandbox Create 授权并固定 Ready build，独立工作区写层/网络，完成 Guest identity bind 后再公布 Ready。
6. **Warm snapshot optimization**：从验证合格的 base/custom build 捕获 CHV memory snapshot，按需恢复和实测，不作为 Registry/模板权限的前置替代。
7. **扩展**：多 Node artifact cache、公开模板发现/搜索、审计导出和模板配额。团队/组织模板不在当前规划内；未来需要时另行决策和立项。

## 8. 验收标准

- [ ] 系统模板与个人模板 owner/scope、visibility 可区分；个人模板同时支持 private/public。
- [ ] 系统模板由平台管理，用户不能编辑或删除；个人 owner 权限正确。
- [ ] 用户 A 不能列出、读取、构建或从用户 B 的 private 模板派生；错误不泄漏名称和 Build ID。
- [ ] public 模板可被其他已认证用户使用，但不能读到 build secret/log；public 查询只返回 Ready build 及公开 metadata。
- [ ] Template build 是异步且可观察；失败保留 Build ID/阶段，不能错误移动 default tag；重试创建新的 Build ID。
- [x] Sandbox Create 固定 build ID 和 artifact digest；tag 更新不改变已创建 Sandbox 的来源。真实 PostgreSQL 与 API Key HTTP 测试通过；VM dispatch/Ready 发布仍单独验收。
- [ ] 系统 base manifest 包含 Isodo Guest Agent 版本、protocol/capabilities、架构与 digest；agent 无租户 identity 时 Ready。
- [ ] 使用现有 identity→tenant 映射确定模板 owner；现有登录、API Key、Sandbox CRUD 的 owner isolation 不回退。
- [ ] 单 Node 真实 CHV 构建、启动、Agent ready、清理完成；性能数据按模板 build 与 restore 阶段分开统计。

## 9. 参考位置

- E2B 模板构建：[create_template.go](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/api/internal/template-manager/create_template.go)
- E2B Template ownership/Public 权限：[cache.go](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/api/internal/cache/templates/cache.go)
- E2B Sandbox 使用模板时的解析与权限：[sandbox_create.go](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/api/internal/handlers/sandbox_create.go)
- E2B Template/Team auth：[auth.go](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/api/internal/handlers/auth.go)
- E2B Team 与系统默认模板合并列表：[list_team_templates.sql](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/db/pkg/dashboard/sql_queries/templates/list_team_templates.sql)
- E2B 系统默认模板关联：[get_default_templates.sql](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/db/pkg/dashboard/sql_queries/templates/get_default_templates.sql)
