# CHV、Lima 与 Sandbox 端口 endpoint 能力核对

核对日期：2026-10-24。范围：当前 Isodo 源码/specs、Cloud Hypervisor（CHV）官方文档及网络配置源码、Lima 官方端口文档、E2B 官方文档及 infra 源码。本文只记录调研与建议，不修改实现，未运行端口/DNS/TLS 真机测试。CHV `main` 和在线文档为查询时版本；E2B infra 固定参考 HEAD 为 `6b82caf690b06fb983254714187dc496ac4d0cec`。

## 1. CHV 是否原生映射 Guest TCP 端口到 Host

**结论：本次核对的 CHV 原生网络配置没有 Docker `-p` / QEMU `hostfwd` 式 Guest TCP → Host TCP 映射。** CHV 提供虚拟网络设备及其后端，端口发布需要宿主规则或额外代理。依据是官方 `NetConfig::SYNTAX` / `NetConfig::parse` 的网络选项：TAP、IP/mask、MAC、fd、MTU、队列、vhost-user socket/mode、限速、PCI 和 offload，没有 host/guest TCP port mapping 项。[CHV 配置源码](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/vmm/src/config.rs)；官方启动示例使用 `--net "tap=,mac=,ip=,mask="`。[CHV README](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/README.md#booting-the-guest-vm)

可行工程方案是：配置 Guest NIC/TAP 和宿主到 Guest 的路由后，由宿主 DNAT 或 TCP/HTTP proxy 转到 Guest IP:port；或者不用 Guest IP，由 Guest relay 在 TCP 与 Vsock 间桥接。**这是部署层方案，不是 CHV 已有的端口发布 API。** `--net socket=` 指向 vhost-user 网络后端，不是应用 TCP 端口。

CHV 原生提供 **stream Vsock**：Host 连 CHV Unix socket，先写 `CONNECT <vsock-port>\n`，Guest 必须监听 AF_VSOCK；Guest 主动连 Host 则使用 CID 2，Host 监听 `<socket>_<port>` Unix socket。[CHV Vsock 官方文档](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/vsock.md)

Vsock port 不是 Guest TCP port。Guest 在 `127.0.0.1:8080` 监听 TCP，不会自动变成 Vsock:8080，仍需 relay 或应用直接支持 AF_VSOCK。

| 表述 | 真实方向 / 所需组件 |
| --- | --- |
| “把 Guest 服务转发到 Host” | Client/Host 发起连接到 Host listener，再由转发器连 Guest；属于入站服务发布 |
| “Guest 到 Host 通信” | Guest 发起连接到宿主服务；Vsock 支持，但宿主需对应 Unix listener |
| “Guest 访问公网” | NIC、路由、出口策略/NAT；与服务发布和 Vsock 控制通道分别设计 |

## 2. 当前 Isodo 的真实实现

### CHV：无 NIC，无应用端口转发

- 创建请求只接受 egress deny，非 deny 或非空 allowed CIDR 返回 FailedPrecondition。[chv.go:140](../internal/node/orchestrator/chv.go#L140)
- `RuntimeConfig` 没有网络/端口发布输入；冷启动明确注释未挂 network device，隔离接线完成前 deny egress。[runtime.go:19](../internal/node/templatebuilder/runtime.go#L19)、[runtime.go:108](../internal/node/templatebuilder/runtime.go#L108)
- `prepareVM` 配置磁盘、CPU、内存、payload、Vsock（CID 3）、serial、console；当前 `vmm.VMConfig` 类型也没有 `net` 字段。[probe.go:98](../internal/node/templatebuilder/probe.go#L98)、[client.go:23](../internal/node/vmm/client.go#L23)
- Runtime 的 Ready/Bind 通过 Vsock 连接 Guest Agent；`VsockSocket()` 是 Node 内 Unix socket 路径，不是浏览器 URL。[runtime.go:117](../internal/node/templatebuilder/runtime.go#L117)

当前 CHV 不具备已证实的任意 Guest TCP → Host listener、Guest 普通 TCP → Host/公网、E2B 式 preview 域名。Guest Agent RPC 不能推出通用 TCP relay 已实现。

### Lima：显式转发两个 loopback 端口

克隆时写入 `.portForwards`，两条规则均为 `guestIP=127.0.0.1`、`hostIP=127.0.0.1`、`static=true`。[base.go:96](../internal/node/lima/base.go#L96)

| Guest 目标 | Host listener | 用途 |
| --- | --- | --- |
| TCP 1024 Guest Agent | `127.0.0.1:<40000..59999>`，sandbox ID 哈希决定 | Node→Guest gRPC |
| TCP 8080 T3 | `127.0.0.1:<20000..39999>`，另一哈希决定 | 浏览器打开 T3 |

端口常量见 [manager.go:20](../internal/node/lima/manager.go#L20)，Host 分配见 [base.go:141](../internal/node/lima/base.go#L141)。T3 URL 为 `http://sbx-<sandbox-hash>.localhost:<hostPort>`。[base.go:167](../internal/node/lima/base.go#L167) `CreateSandboxAppLink` 仅在 Lima backend 生成 5 分钟 T3 pairing credential，返回 `/pair#token=...`。[t3.go:28](../internal/node/orchestrator/t3.go#L28) 这不是任意端口 API，也不是 CHV 能力。有限区间哈希不保证唯一，本轮未证明冲突检测/重试。

Lima 本身支持 localhost 自动端口转发，官方提供 SSH 和 gRPC forwarder；gRPC 支持 TCP/UDP，SSH 不支持 UDP，默认选择随版本变化。[Lima 官方 Port Forwarding](https://lima-vm.io/docs/config/port/) 两条显式规则不证明其余端口被禁止自动转发；最终合并配置和自动匹配规则本轮未核对。

### specs 与实现不能混用

[network-security.md](network-security.md) 声明契约草案未实现/验收，第 3 节设计独立 netns/TAP/NAT/防火墙，第 5 节明确公网 preview、动态 URL 和自动唤醒延期。其默认公网设计目标与当前 CHV deny-only 运行能力不同。

## 3. E2B 域名模型及 DNS/TLS/代理要求

官方 `getHost(3000)` 返回如 `3000-i62mff4ahtrdfdkyn2esc.e2b.app`，调用方访问 `https://${host}`。这是外部 HTTPS 默认 443 上的 **HTTP 反向代理地址**；3000 编码在 hostname，不表示客户端连远端 TCP 3000，不能推出任意裸 TCP/UDP 发布能力。[E2B 官方 public URL](https://docs.e2b.dev/network/public-url.md)

固定 infra 源码证实以下链路：

```text
Browser HTTPS :443
  → TLS/HTTP ingress（生产部署拓扑本轮未核实）
  → client-proxy：Host → port+sandboxID，catalog → Node
  → Node orchestrator proxy
  → sandbox slot HostIP:guestPort
  → Guest 服务
```

- `GetTargetFromRequest/parseHost` 解析最左 label、校验 1..65535 端口和 sandbox ID；localhost/IP/shared-host 路径另支持 routing header。[host.go](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/shared/pkg/proxy/host.go#L17)
- client-proxy 查询 catalog，再转到 Node IP 的 orchestrator proxy port。[client proxy](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/client-proxy/internal/proxy/proxy.go#L184)
- Node proxy 找本机 sandbox，检查 ingress traffic token（若配置），再连 `Slot.HostIPString():port`；内部 envd 管理路径另行保护。[Node proxy](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/orchestrator/pkg/proxy/proxy.go#L65)
- 官方明确 public URL 总是 HTTPS，默认 Guest hop 是 HTTP；`httpsPorts` 可选下游 HTTPS，属于终止后重加密，不是 TLS passthrough。[E2B 文档](https://docs.e2b.dev/network/public-url.md#connecting-to-https-servers-inside-the-sandbox)

Isodo 若提供同类模型，至少需要以下能力（**架构必要条件/建议，不是 E2B 生产配置断言**）：

1. **DNS**：`*.<preview-domain>` 指向可达 ingress，或动态生成精确 DNS 记录。DNS 不解析 sandbox/port 语义，不需要每端口独占公网 IP。
2. **TLS**：入口证书覆盖这些 hostname，例如 `*.<preview-domain>` 或精确 SAN，并自动续期。`3000-id` 为一个 label；若改成 `3000.id.domain`，原单层 wildcard 不覆盖。若选 wildcard ACME，需 DNS-01 自动化。拼域名不能产生 HTTPS 能力。
3. **入口 HTTP/WS 代理与路由目录**：校验 Host、解析 sandbox→owner Node/current generation，代理到 Node 可达数据面；支持 upgrade/streaming/超时/背压。域名格式不是鉴权。Isodo UUID 含连字符，不能照搬 E2B `Split("-")[1]`，须定义无歧义编码。
4. **Node→Guest 数据面**：可达 Guest IP:port，或经授权 Vsock→Guest TCP relay。VMM API/Unix socket 不自动承载网页。
5. **暴露策略与生命周期**：端口允许名单、鉴权、管理口禁止发布、Stop/Delete/迁移撤销路由与连接、generation 防陈旧路由。E2B 默认知晓 URL 者可访问；Isodo 需自行决定权限语义。[E2B 默认访问行为](https://docs.e2b.dev/network/public-url.md)

**未知：** E2B 公有云 DNS provider、边缘负载均衡产品、证书签发/续期和内部 hop TLS 实际拓扑，本轮未检查部署清单，不猜测 Nginx/Envoy/Cloudflare 等具体产品。

## 4. 推荐统一 endpoint 抽象（建议，未实现）

统一“沙箱服务身份与访问结果”，由 backend 解析 `(sandbox_id, service/guest_port, protocol)` 返回 endpoint；前端不要自行拼域名或假设所有 endpoint 都是 localhost。

| 字段 | 语义 |
| --- | --- |
| `sandbox_id`, `service`, `guest_port` | 稳定服务身份；T3 可命名 `sandbox-app`；port 为 Guest 端口 |
| `protocol` | `http` / `https` / `tcp`；WS/WSS 为显式能力，裸 TCP 不伪装 URL |
| `scope` | `local` / `private` / `public` 可达范围 |
| `status` | `ready` / `pending` / `unsupported` / `stopped` / `failed`，未实现不返回伪地址 |
| `url` / `address` | ready 后的实际 URL 或 TCP host:port，调用方不解析重建 |
| `auth` | pairing/session/token 等已支持方式；敏感 token 不进普通列表/日志 |
| `expires_at`, `generation` | 有效期及 owner/run 身份，Stop/Delete/重建使旧 endpoint 失效 |
| `capabilities` | 协议、范围、端口支持，UI 能力与 backend 一致 |

内部另维护 Node ID、generation、Guest TCP/Vsock relay 目标、Lima host port 等 `EndpointTarget`，不泄露宿主私有路径。稳定身份可跨后端保留，可连接地址和凭证允许变化。

| 后端 | 当前诚实结果 | 推荐后续 |
| --- | --- | --- |
| 本地 Lima / T3 | local；现有 localhost 子域 URL 和 pairing link | 延续现有转发器，补端口租约/冲突处理；任意端口另做受控发布 |
| 远程 CHV / 应用端口 | unsupported | 先为明确服务实现独立 Guest TCP↔Vsock relay，再接 Node/入口代理；或完整实现隔离 NIC/TAP 后由 Node 直连 |
| 远程 CHV / HTTP preview 完成后 | 才返回 private/public HTTPS URL | DNS/TLS/proxy 为部署能力；返回实际配置 domain，前端不依赖 E2B 字符串形式 |

近期建议保留 CHV Vsock 控制面，为明确应用服务增加独立 relay 数据面，适配当前无 NIC 实现。若同时需要通用公网出口，再按网络规格实现隔离 NIC/TAP/NAT。relay 不自动继承 credential gateway 权限，两条路线均需真机验收。

## 5. 明确未验证项

- 未运行 CHV/Lima 端口、Vsock↔TCP、DNS/TLS 测试；源码审阅不是上线能力证明。
- 部署 CHV/Lima 版本、Lima forwarder 选择和最终自动转发规则未核对。
- 冷启动无 NIC 已证明；snapshot restore 对外部制品 NIC 的拒绝/净化保证未做完整审计，不能外推全部 snapshot。
- Lima 哈希端口冲突、停止/恢复清理、`.localhost` 在目标 OS/浏览器解析、Cookie Domain 跨 sandbox 行为未验收；不同 origin 不代表 Cookie 按 port 隔离。
- HTTP/WS streaming、Host 校验、鉴权、内部管理口保护、Stop/Delete 后活跃连接及旧链接撤销须在实现后验收。
- 本研究未提交、推送或创建 PR；未修改实现代码。
