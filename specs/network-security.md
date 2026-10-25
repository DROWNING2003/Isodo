# 网络、身份与凭证边界规格 v0.1

状态：2026-10-24，实现契约草案，尚未实现/安全验收。主要行为参考 E2B；Isodo 的具体规则与限制以下文为准。[E2B 网络补充核对](research-network-details.md) 记录固定版本源码和未验证边界，不能把上游代码审阅当成本项目的隔离证明。

相关：[服务行为](service-behavior.md)、[生命周期](sandbox-lifecycle.md)、[Guest](guest-protocol.md)、[模板/存储](template-storage.md)。首版 Linux 单节点功能闭环，默认公网出口，IPv4；通用域名过滤、任意 MCP 协议和公开端口预览延后。

## 1. 身份与信任边界

- 用户 API 认证使用 Casdoor OAuth 2.0/OIDC access token（Bearer），Casdoor Application 仅允许 GitHub OAuth 登录；本地密码/验证码等登录方式和其他 OAuth Provider 关闭。Isodo 后端经固定 issuer 的 discovery/UserInfo 验证 token，并以 `(issuer, sub)` 映射本地 tenant；email、用户名、GitHub username、客户端传入的 tenant ID 均不构成授权凭据。GitHub-only 限制依赖 Casdoor Application 配置，Isodo 当前不根据 UserInfo 推断上游登录 Provider。完整 R-01 契约见 [Sandbox CRUD 与 Casdoor 鉴权](sandbox-crud-auth.md)。桌面登录和 CLI/device-code 流程延后。
- API 每次读取/操作先核实对象 tenant。跨租户对象返回 404；GET /nodes 和节点配置仅管理员可见。metadata、资源 ID、IP 不构成授权凭据。
- Control Plane→Node、Node→Gateway 使用各自服务身份的 mTLS；证书绑定 node/service ID，不能只验证“由同一个 CA 签发”。Node 核实该 sandbox 的 owner generation/revision。
- Node→Guest 是该实例 Vsock 私有通道，规则见 Guest 协议；Guest 自报 tenant/hostname 不可信，不能据此选择宿主路径或第三方 credential。
- VMM 是专属非特权 UID 的宿主进程；Node 执行必需的 netns/systemd 操作。Guest workload 无宿主 KVM/socket/目录权限，无 Guest CAP_SYS_ADMIN，无隐式 host 文件共享。

首版配置需要说明 API TLS 终止、CA/证书来源、Casdoor 应用配置和新用户开通策略。Casdoor 仅启用 GitHub OAuth Provider，允许通过 GitHub 首次登录/注册以触发 Isodo identity 与个人 tenant 的自动创建；该 Application 上的其他 Provider 和 Casdoor 本地登录方法必须关闭。Casdoor client secret 只能通过部署密钥管理注入，不能写进 Git、镜像、示例配置或日志。Casdoor 用户认证不替代 Control Plane→Node、Node→Gateway 的服务身份认证。

## 2. 创建时网络参数

```json
{
  "network": {
    "allow_internet_access": true,
    "allow": ["203.0.113.10/32"],
    "deny": ["0.0.0.0/0"]
  }
}
```

示例 IP 为文档地址，不是可用服务。allow/deny 首版只接受规范化 IPv4 或 CIDR，最多各 128 项；重复项合并。拒绝域名、IPv6、无效掩码和隐式端口语法，不静默忽略。

allow_internet_access 缺省 true。关闭公网等价于在用户 deny 中加入 `0.0.0.0/0`，仍允许用户显式 allow 的公网例外；只填 allow 列表不会自动关闭其余公网。若要白名单模式，须同时 false 或 deny-all。

### 2.1 规则优先级

从高到低：

1. 平台固定基础设施例外：仅必要 DHCP/DNS；按虚拟设备、源身份、目标 IP、端口/协议精确匹配，不放开整个宿主网段。
2. 平台不可覆盖保护：宿主所有地址、管理网、所有沙箱网段、私有/loopback/link-local/元数据/其他非公网范围；任何用户 allow 不能覆盖。
3. 用户 allow。
4. 用户 deny（含关闭公网产生的 deny-all）。
5. 缺省允许其余公网。

用户 allow 优先于用户 deny 参考 E2B；平台保护不可被用户覆盖是 Isodo 的明确边界，不从 E2B 常规规则链推导绝对保证。必要 DNS/DHCP 不得被任意端口复用为 Node 控制接口。

### 2.2 地址与协议范围

平台保护配置至少包含 0/8、10/8、100.64/10、127/8、169.254/16、172.16/12、192.168/16、198.18/15、224/4、240/4，及运营环境实际的节点/控制面/沙箱/宿主路由地址。云元数据和 IPv4-mapped 地址必须规范化后识别；HTTP 代理另处理整数、非标准文本和解析歧义。

首版允许按上述 IPv4 目的地策略访问 TCP 和 UDP，不按 HTTP 包装全部普通公网通信；ICMP 仅提供必要错误响应，Guest 发起 ICMP echo 不作为首版能力。Guest 不能控制上游 resolver：只允许虚拟 DNS 端点，阻断直连外部 UDP/TCP 53。DNS 查询本身可能携带用户数据；此策略不宣称防止所有 DNS/HTTP 数据外传。

IPv6 首版禁用：Guest NIC 不配置 IPv6，宿主 TAP/转发规则明确 drop IPv6；对伪造包同样拦截。关闭 Guest sysctl 本身不是安全边界。尚未验证 IPv6 前不提供 IPv6 allow 参数或公网声明。

普通联网可以访问公开网站；受控凭证调用的 host/path/tool 另受第 6 节限制。公开联网权限不自动赋予 credential 使用权。

## 3. 单实例网络与先后顺序

每个 VM 独立 netns/TAP、IP/MAC、路由和 NAT/防火墙标识；地址由宿主分配，Guest 不能指定宿主接口名。使用路由/点对点隔离，不能把所有 Guest 放到没有二层隔离的共享 bridge。

顺序：记录网络 intent→预装 default-drop/anti-spoof/内部保护→分配接口和受控 DNS→原子安装目标 egress revision→启动 VMM→Ready。启动前策略失败则不启动 VM，恢复/Start 同样先安装规则；不能让 VM 先联网再补防火墙。

- 检查 Guest 源 IP/MAC 与 TAP 归属；禁止伪造其他实例地址、DHCP server 或 router advertisement。
- 宿主 INPUT 拒绝来自 Guest 的新连接，只有精确基础设施端点例外。FORWARD 根据平台保护和用户策略处理，不能用前置 ESTABLISHED accept 绕过平台保护。
- 默认没有公网入站；已有 Guest 发起连接的返回包按 conntrack 与实例归属接收。Node/Guest 管理走私有 RPC/Vsock，不开 Guest SSH 作为控制面。
- NAT 只改变出口源地址，不承担隔离；每个实例的 conntrack/策略归属可独立清理，不误删其他 VM 流量。
- 管理口、DNS adapter 和 Gateway relay 应具有不同 listener/权限，不能将一个宿主 HTTP 管理端口顺便作为通用代理。

## 4. 策略更新与撤销

`PUT /v1/sandboxes/{id}/network` 接受完整 network 目标及 expected_policy_revision，返回 202+operation_id；revision 不匹配 409。首版只在 running/stopped 接受，其他生命周期操作冲突时 409；与 Delete 串行化。GET 返回 desired/applied revision 与 apply_status，不能把已写数据库当作已生效。

stopped 更新只记录目标，下次 Start 安装；running 更新使用 **短暂关闭出口→原子装规则→清理该实例旧 conntrack 和受控代理流→重新开放** 的保守方案。所有新策略生效前不报告 operation 成功；可能中断当前公网连接，客户端需重连。此方案是 Isodo 取舍，比 E2B 本轮已核实的常规规则更新承诺更严格。

如果删除旧连接或新规则安装失败，保持 fail-closed、报告 error/needs_attention，不回退到无保护公网。该平台固定保护规则始终存在，不能在切链窗口失效。对所有已有 TCP/UDP 流立即撤销的承诺，必须经过 per-VM conntrack 与代理流关闭真机测试后才能标完成。

网络更新成功只表示限制后续网络可达性；已经发送到对端的数据、对端已开始的任务不能撤回。Node 失联时记录待执行策略，但 API 返回 pending，不称为撤销已经生效。

## 5. 普通域名与公网入站的边界

首版通用出口规则是 IPv4/CIDR。将域名解析一次转成 CIDR并非稳定域名策略，CDN IP 变化、共享 IP、HTTP Host/SNI、其他端口和 DNS 重绑定必须单独设计；未实现时域名参数返回 422 CAPABILITY_UNSUPPORTED。

模型/MCP 的特定服务适配器可以按固定 host 做 HTTPS 访问，但不因此宣称已有通用域名防火墙。公网 preview、动态端口 URL 和自动唤醒后续另立入站/鉴权契约，不能默认暴露 Guest 所有监听端口。

## 6. 最小 Secretless Gateway

目标是一个真实模型 HTTP 服务和一种 MCP remote HTTP 工具链路，credential 保存在沙箱外。用户在 Guest 中运行的 Agent 通过配置好的本地 adapter 使用它；具体兼容的模型/工具在首次实现 PR 记录并真机验收，不凭配置项存在就宣称全部 CLI/MCP 支持。

### 6.1 资源与 API

| 资源 | 最少字段 | 接口 |
| --- | --- | --- |
| Credential | id、tenant_id、service_type、secret_version、status、created_at | POST /credentials；GET /credentials；DELETE /credentials/{id} |
| Connection | id、tenant_id、sandbox_id、credential_id、allowed_operations、policy_revision、status | POST /sandboxes/{id}/connections；GET 同路径；DELETE /sandboxes/{id}/connections/{id} |

Credential 创建接收 secret，响应仅 metadata；GET 永不返回 secret。删除 credential 先禁用新使用并撤销关联连接，原密文按 key/存储保留策略清理；不在数据库事务中等待所有上游网络调用结束。

Connection status=active/revoked；撤销不可通过旧 token 恢复。重复 DELETE 幂等，新授权创建新 connection ID。credential 与 sandbox 必须属于同 tenant，scope 只能收窄，不能按 Guest 传入 scope 扩大。

首版 POST 写操作使用 Idempotency-Key，规则沿用生命周期。密钥值不进入通用请求哈希日志；去重只存摘要和资源 ID。Secret 创建日志与审计中对 body、query、headers 全面脱敏。

### 6.2 调用链与上游选择

```text
Guest Agent workload / local HTTP adapter
  -> per-VM Vsock credential relay
  -> Node（从宿主映射绑定 sandbox/generation/boot_id）
  -> mTLS Gateway
  -> 服务端固定 operation 对应的 HTTPS 上游
```

relay 是仅承载已注册 Gateway operation 的通道，不接受任意 host:port/CONNECT。Guest 侧 adapter 不含第三方密钥；Node 不能直接把 Guest 自报的 tenant/sandbox/connection 作为授权事实。

Gateway 每次调用核实 Node mTLS 身份、tenant、sandbox owner/generation、boot_id、sandbox 当前允许运行且未进入停止/删除准入关闭状态、connection active、credential active、允许 operation 与配额。以数据库当前状态为准；校验失败或权限存储不可用时拒绝。首次调用和长流开始均须检查，不用缓存“授权曾成功”代替现在的权限。

同一 sandbox 内的 workload 共享其连接能力，首版不声称不同进程/Task 之间有独立租户隔离。权限随 sandbox 授予；Stop 后不允许调用，Start 保留 active 连接记录但必须为新 boot_id 重新绑定；从快照新建的沙箱必须重新授权。

### 6.3 凭证与 HTTP 安全

- Gateway 在专门的受限入口读取 secret，使用独立 SecretStore 接口；首版可用信封加密存储，主密钥只通过 Gateway 的受保护配置/文件挂载提供。不得把主密钥与密文一起写数据库或镜像。
- 平台 API 只处理 metadata；如承担创建转发，须使用专用无 body 日志路径，不把 secret 复制到 operation/audit/trace。Node 无第三方 secret 解密权限。
- 固定 scheme=https、host、port、method、路径/工具白名单，服务端生成目标 URL；拒绝 Guest 任意 URL、用户信息段、混淆 Host、未批准请求头和任意重定向。
- 上游请求在 Gateway 重新解析域名、校验所有候选 IP，并把连接固定到实际已验证 IP；TLS 仍校验原域名。拒绝宿主/私网/元数据等保护地址，不按 Guest DNS 结果拨号。
- 注入前移除 Guest 提供的鉴权头/受保护 query，再按 adapter 替换相应字段；不把上游 Authorization、set-cookie、调试回显或 secret store 错误原样返回。
- Request/response body 默认不记录；MCP 工具响应仍是不可信内容，不能授予更高平台权限。

长期订阅/OAuth 登录态和任意客户端订阅的兼容性取决于对应上游协议，不由“支持 HTTP 注入”自动推出。临时凭证若确需下发必须单独规格，不能悄悄改变“第三方 token 不进 Guest”的承诺。

### 6.4 撤销语义

Connection DELETE 提交 revoked 后，所有后续调用按权威检查拒绝；返回 revoked 不等于远端已中止。Gateway 记录 active connection→request 映射，撤销通知触发关闭本地流和取消上游请求（best effort）；进程中已传给远端的工作不可保证撤回。

Gateway 缓存/多副本通知不能作为新调用授权的唯一依据。首版每次调用做权威检查，避免撤销传播窗口；Node 失联不影响 Gateway 对新调用的拒绝。已有流关闭进度可审计，未完成不能宣称“所有调用瞬间撤回”。

## 7. 发布与验收

首版默认限制单租户测试调用率，单沙箱 Gateway 并发 8、单请求 body 8 MiB，普通调用 60 秒、模型流 10 分钟；流式输出有界并在撤销/超时后关闭。实际适配器可更小，不能超过平台上限而未做验收。

| 场景 | 必须验证 |
| --- | --- |
| 默认公网 / 显式关闭 / allow 例外 | 真实访问结果与规则优先级一致 |
| allow 与 deny 同时命中 | 用户 allow 生效，平台保护仍不可覆盖 |
| Guest 伪造 IP/MAC、扫描宿主和其他 VM | 宿主/跨 VM 不可达，无二层旁路 |
| IPv6 / UDP / 外部 DNS / 元数据 | 不出现只保护 TCP/IPv4 的绕过 |
| Start / 网络更新失败 | 在 Guest 可发包前 fail-closed |
| 规则更新时已建立 TCP/UDP | 成功返回前受影响旧流已清理，其他沙箱不受影响 |
| 凭证授权→真实调用→撤销→新调用 | Guest 无第三方 token，撤销后新调用拒绝 |
| 旧 boot_id/别人的 connection/伪造 tenant | 拒绝，不查询/返回他人 secret |
| 重定向、DNS 重绑定、IP 文本混淆 | 固定上游边界有效，无 SSRF |
| Gateway/权限数据库不可用 | 拒绝敏感调用，不凭旧缓存开放 |
| 日志、镜像、Guest env/文件 | 无第三方 token 或主密钥 |

范围含 Linux 网络、API 身份和最小密钥使用链路。未完成测试前只报告设计，不称为生产级多租户安全认证。

## 8. 当前本地实现：账号规则与沙箱注入代理

2026-10-25 的本地 Lima 路径先落了一条可演示的真实注入链路，作为未来 Connection Gateway 的本地对照实现，不改变上文云端 Gateway 的授权边界：

- 账号级持久规则保存在控制面 `injection_rules`，密钥 AES-256-GCM 加密；`GET /v1/injection-rules` 只返回脱敏提示，material 接口仅接受交互式会话。
- 本地 Node 为每个沙箱维护独立规则集：可引用账号规则，也可拥有只属于该沙箱的单次规则。单次规则整体加密保存在 Node 状态目录。
- Node 在宿主启动 per-sandbox HTTP CONNECT 代理，Guest 通过 `HTTPS_PROXY` 使用；只有命中 `base_url` host 的请求终止 TLS，其余请求原样隧道转发。
- CA 私钥只保存在宿主，Guest 只拿到 CA 证书和代理地址；真实第三方密钥不写入沙箱文件。T3、终端和 Agent 子进程继承代理与 CA 环境变量。
- 已用真实 Lima Guest 的 curl 验证 Header 注入；云端 CHV 的注入代理、WebSocket/HTTP2、非 HTTP 协议和所有 Agent CLI 的兼容性仍未验证。
