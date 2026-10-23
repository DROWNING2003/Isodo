# 目录与模块职责

状态：建议布局，2026-10-24。当前已有 prototype、specs、验证探针 tests/validation、验证证据 artifacts/validation 和 api/proto 下的 Guest 契约；正式 Runtime 实现目录按相关 PR 创建。

```text
Isodo/
├── prototype/                 # 原有交互原型、mock 和设计历史
├── specs/                     # 可实施规格、契约说明、上游依据
├── web/                       # 正式网页：沙箱列表、详情、终端、文件、连接
├── cmd/
│   ├── isodo-api/             # 控制面进程入口
│   ├── isodo-node/            # Linux 宿主进程入口
│   ├── isodo-guest/           # Guest 进程入口
│   └── isodo-gateway/         # 第三方凭证与受控调用进程入口
├── api/
│   ├── openapi.yaml           # 外部 HTTP 契约
│   └── proto/                # Node、Guest 内部协议与版本
├── internal/
│   ├── control/              # 鉴权、配额、调度、operations、数据库
│   ├── node/                 # 本机资源、journal、reconcile、Guest relay
│   ├── vmm/cloudhypervisor/  # CHV API 与配置映射，不含租户和业务鉴权
│   ├── guest/                # exec、PTY、files；不操作宿主资源
│   ├── gateway/              # 连接授权、工具/路径策略、上游适配、秘密读取
│   ├── template/             # 不可变 manifest、校验、缓存契约
│   └── protocol/             # 少量跨组件共享类型；避免 common 杂物目录
├── migrations/               # PostgreSQL schema 与升级
├── images/                   # 内核/Guest 镜像配方、版本锁定、清单
├── deploy/
│   ├── nomad/                # API/Gateway service job、Node system job
│   └── systemd/              # 脱离 Nomad allocation 的 VMM 生命周期单元
├── config/                   # 无密钥的示例配置
├── tests/                    # 跨组件 contract/integration/e2e/security
└── docs/                     # 运行说明、ADR、演示、验收证据与实际分工
```

## 依赖方向

- `cmd/` 仅装配配置、依赖和信号处理；业务放对应 internal 模块。初期使用一个 Go module，尚不需要 go.work 多模块。
- `web/` → 外部 OpenAPI；不得引用 Node/Guest 内部协议，也不持有宿主地址和权限。
- `control/` → Node RPC；控制面存期望状态，不直接操作 CHV、systemd 或节点文件。
- `node/` → VMM adapter、宿主资源管理、Guest RPC；只管理本机，不实现全局用户和计费。
- `vmm/cloudhypervisor/` 仅适配 VMM。systemd 进程托管、磁盘和网络事务由 node 编排，避免 adapter 变成第二个 Node。
- `guest/` 只管理 VM 内进程和文件；上层 Agent 是用户负载，不属于基础设施 Guest Agent。
- `gateway/` 是独立信任边界，公开平台 API 只处理 credential metadata/connection ID；凭证值走专门受限入口和 secret store。
- 模块单元测试与代码同目录；`tests/` 只存跨组件验证，不重复搬运所有单元测试。

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

Guest 接口已定义于 [协议规格](guest-protocol.md) 与 [api/proto/isodo/guest/v1/guest.proto](../api/proto/isodo/guest/v1/guest.proto)；api/ 当前仅有契约，尚无 Runtime 实现。
