# 目录与模块职责

状态：现状与目标布局，2026-10-24。`api/` 存放机器可读接口契约；`specs/` 存放人读的架构与实施规格；原型和 T3 派生应用工作区仍按仓库约定保留在本地并由根 `.gitignore` 排除。

## 当前目录

```text
Isodo/
├── api/
│   ├── openapi/                 # 控制面与公开 HTTP 契约
│   └── proto/isodo/guest/v1/    # Guest RPC 契约
├── app/                         # 本地 Isodo/T3 派生多应用工作区；当前被忽略
├── artifacts/validation/        # CHV/Vsock 探针的可核对证据
├── assets/brand/                # 项目共用品牌素材
├── cmd/isodo-api/               # 控制面进程入口
├── deploy/                      # Casdoor、PostgreSQL、Redis、SeaweedFS 等本地/部署配置
├── docs/                        # 产品上下文、产品决策、参赛交付要求
├── docs-site/                   # Fumadocs 产品文档站；公开 API Reference 从 OpenAPI 生成
├── internal/
│   ├── api/{admin,public}/      # OpenAPI 生成的 Go 路由与模型
│   ├── control/{auth,postgres,sandbox}/ # 当前控制面实现
│   └── httpapi/                 # API 组合与 HTTP 中间件
├── migrations/                  # PostgreSQL schema 迁移
├── prototype/                  # 原型、mock、交互稿和设计历史；当前被忽略
├── scripts/                     # 生成、环境配置和集成验证脚本
├── specs/                       # 人读的产品、架构、模块和研究规格
├── tasks/                       # 实施计划与 TODO
├── tests/validation/            # 跨模块/环境验证工具
└── tools/openapi/               # Go 与前端 API 生成器配置
```

`app/` 是根仓库中的本地目录，不是嵌套 Git 仓库；它包含 web、desktop、mobile、server 和共享 packages。当前根 `.gitignore` 排除了整个目录，因此这些本地工作区内容不会随根仓库提交。`prototype/` 同样是本地原型目录。修改或迁移这两个目录前应先明确是否要纳入正式源码。

## 目标布局

随着 Runtime 实现逐步进入仓库，后端按可执行入口和业务模块扩展。目录只在对应实现开始时创建，不预建空模块：

```text
Isodo/
├── api/
│   ├── openapi/{openapi.yaml,openapi-public.yaml}
│   └── proto/isodo/{node,guest}/v1/
├── cmd/{isodo-api,isodo-orchestrator,isodo-guest,isodo-gateway}/
├── internal/
│   ├── control/                 # 租户、鉴权、配额、placement、operation/outbox
│   │   ├── orchestrator/        # Control -> Node 的 gRPC client/连接池
│   │   └── redis/               # active Sandbox Store、索引、routing catalog adapter
│   ├── node/                    # gRPC server、宿主资源、journal、reconcile 与 Guest relay
│   ├── vmm/cloudhypervisor/     # CHV 适配，不承载租户业务
│   ├── guest/                  # Guest 内 exec、PTY、files
│   ├── gateway/                # 连接授权、上游适配与秘密读取
│   └── template/               # manifest、校验与缓存
├── migrations/
├── deploy/{nomad,systemd}/
├── images/                     # Guest 镜像配方、版本和 manifest
├── docs/                       # 产品上下文、决策和交付说明
├── docs-site/                  # 面向用户的 Fumadocs 文档站与公开 API Reference
├── specs/                      # 可实施规格、架构决策和参考研究
├── tests/                      # 跨模块 contract/integration/e2e/security
└── artifacts/                  # 可复核的验证证据
```

正式 Web 目前位于本地工作区 `app/apps/web/`，由 `make web` 启动；其代码尚未纳入根仓库提交。原型统一留在 `prototype/`，正式网页逐项绑定真实能力后再调整源码归属。

## 依赖方向

- `cmd/` 只装配配置、依赖和信号处理；业务放在对应 `internal/` 模块。
- `app/apps/web/` 消费公开 HTTP 契约；不得引用 Node/Guest 内部协议，也不持有宿主地址和权限。
- `internal/control/` 存储租户期望状态并调用 Node 接口，不直接操作 CHV、systemd 或节点文件。
- `internal/control/orchestrator/` 负责 Node registry/placement 结果到 gRPC target 的映射、mTLS 连接池、RPC deadline 和错误翻译；HTTP handler 不直接拨号 Node。
- `internal/control/redis/` 维护 active Sandbox record、tenant index 和过期扫描；Redis 记录仅用于在线状态与路由发现，不作为租户授权依据。
- `internal/node/` 管理本机资源、VMM、Guest RPC 和宿主网络；不实现全局用户和计费。
- Node Orchestrator 实现内部 gRPC service，并在真实 Guest Ready/MarkRunning、Stop/Delete/Crash 时发布/撤销 Redis routing record；用 execution_id 做 fencing。
- `internal/vmm/cloudhypervisor/` 只适配 VMM。进程托管、磁盘和网络事务由 Node 编排。
- `internal/guest/` 只管理 VM 内进程和文件，不操作宿主资源。
- `internal/gateway/` 是独立信任边界；平台 API 只处理 credential metadata/connection ID，凭证值走受限入口和 secret store。
- 模块单元验证与代码同目录；`tests/` 只存跨模块验证，不重复搬运单元验证。

R-01 的 Control API 入口位于 `cmd/isodo-api`；Casdoor 身份验证和本地 tenant 映射位于 `internal/control/auth`，Sandbox HTTP 处理位于 `internal/control/sandbox`，PostgreSQL adapter 位于 `internal/control/postgres`。机器可读契约位于 `api/openapi/`，生成流程见其中的 README。

## 网页职责

正式网页沿用原型的沙箱主体与页面布局，但逐项绑定真实能力：

| 页面 | v0.1 接口职责 | 未实现时的呈现 |
| --- | --- | --- |
| 沙箱总览/创建 | templates、sandboxes、operations | 显示真实准备/失败状态，不用定时器模拟完成 |
| 沙箱详情 | observed state、资源、启动/停止/删除 | unknown 与 stopped 分开 |
| 终端 | terminal 创建、WS 字节流、resize | 未实现的重连明确不可用 |
| 文件 | 受限路径上传/下载/目录 | 展示后端错误，不假成功 |
| 安全连接 | 授权、撤销、受控调用审计 | 仅展示已验证的服务适配 |
| 快照/套餐/对话任务 | 后续契约确定后接入 | 保留在原型演示，不显示为已可用能力 |

网页不是新的 Node 控制层；它不自行认定操作成功，operation 完成后重新读取 Sandbox 观测状态。

## 按模块参考上游

| 本项目模块 | 第一参考 | 补充参考 |
| --- | --- | --- |
| control / API / 调度 | E2B Runtime API 与架构 | sandboxd 对照单节点边界 |
| node / CHV adapter | Cocoon Engine | CHV 官方 API 为行为依据 |
| guest exec / 协议 | Cocoon Agent、silkd 进程表 | E2B envd process proto；Isodo 落地见 [Guest 运行时规格](guest-agent-runtime.md) |
| guest PTY / files | silkd PTY 实现 | E2B envd；Isodo PTY 实现见 [Guest 运行时规格](guest-agent-runtime.md) |
| checkpoint / snapshot | CHV 官方文档 + Cocoon | E2B 工件管理思想，非 Firecracker 格式移植 |
| gateway | 自身威胁模型与真实上游协议 | 不因某仓库有代理就认定其满足全部要求 |

具体许可证、固定版本和源码入口见 [references.md](references.md)。这是代码职责分解，实际成员分工尚未提供。

Guest 接口已定义于 [协议规格](guest-protocol.md) 与 [guest.proto](../api/proto/isodo/guest/v1/guest.proto)；当前 `api/proto/` 仅有 Guest 契约，Node gRPC 契约与 Runtime 实现仍待完成。Control/Node gRPC 与 Redis record 的规范见 [Control API 与 Node RPC 接入契约](control-node-contract.md)。

模板 Registry HTTP 位于 `internal/control/template/`，查询与事务仍由 `internal/control/postgres/` 承担。`internal/templateartifact/` 校验不可变 manifest，并通过 S3 传输模板文件；不处理用户授权或 VM 调度。SeaweedFS 的 Compose 配置与本地凭证初始化见 `deploy/seaweedfs/`。
