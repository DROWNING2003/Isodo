# E2B 网络实现补充核对

核对日期：2026-10-24。本地 `reference.e2b-infra` HEAD 为 `6b82caf690b06fb983254714187dc496ac4d0cec`。仅核对以下源码，未运行网络测试，也未完成全链路安全审计；这些事实不表示 Isodo 自动具备相同能力。

## Allow / deny 优先级

- TCP 的 `isEgressAllowed` 实际实现依次检查 allowed domains、allowed CIDRs、denied CIDRs，均未匹配则允许。显式 allow 可以覆盖用户 deny；只填写 allow 列表不会自动形成默认拒绝。函数注释提到 deny domain，但该函数实际没有对应循环，不应据注释承诺该能力。[源码](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/orchestrator/pkg/tcpfirewall/handlers.go#L177)
- namespace 防火墙中，代理自带 head rules 优先，其后为可选非 DNS UDP 规则、ESTABLISHED/RELATED accept、平台 allow、平台 deny、非 TCP 用户 allow、非 TCP 用户 deny，默认 accept。TCP 的用户策略交给代理；BYOP 模式对平台 deny 的 TCP 路径还有例外，不能把一条链当成完整边界。[规则安装](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/orchestrator/pkg/sandbox/network/firewall.go#L715)

## 平台保护与域名解析

平台 deny 清单包括 IPv4 `10/8`、`100.64/10`、`127/8`、`169.254/16`、`172.16/12`、`192.168/16`，以及 IPv6 `::1/128`、`fc00::/7`、`fe80::/10`。其中 IPv4 link-local 包含常见云元数据地址；这份清单不是所有特殊地址的完整集合。[清单](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/shared/pkg/sandbox-network/firewall.go#L22)

namespace 平台 allow 包含 orchestrator internal IP `/32` 和额外配置 CIDR，并排在平台 deny 前。因此应区分必要控制通道例外与用户可授权出口。[初始化](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/orchestrator/pkg/sandbox/network/firewall.go#L155)

域名匹配使用 HTTP Host / TLS SNI。命中域名 allow 时，宿主代理重新解析域名，`net.Dialer.ControlContext` 对实际解析出的地址检查平台 deny，避免信任 Guest 的 DNS / hosts 结果。源码确实包含 IPv6 本地地址检查，但不能据此断言整个 Guest 网络的 IPv6 转发都已安全覆盖。[域名处理](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/orchestrator/pkg/tcpfirewall/handlers.go#L53)、[拨号检查](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/orchestrator/pkg/tcpfirewall/handlers.go#L129)

## 更新与已有连接

`Slot.UpdateInternet` 串行取得锁后调用 `Firewall.ApplyRules`；后者通过替换链更新规则和集合。常规规则保留前置 ESTABLISHED/RELATED accept。专门的 `DenyEgress` 同样保留它以允许宿主主动连接 Guest 后的回复，再 drop 其余 tap 流量。因此不能仅以规则更新成功，宣称所有旧连接已撤销；已有 TCP 代理流的关闭与 conntrack 清理本轮未证明。[更新入口](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/orchestrator/pkg/sandbox/network/slot.go)、[ApplyRules / DenyEgress](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/orchestrator/pkg/sandbox/network/firewall.go#L801)

非 DNS UDP 的可选规则特意放在 established accept 前；源码注释说明 drop 模式逐包作用。它显式限定 IPv4，仓库测试 `TestFirewall_NonDNSUDP_IPv6LeftAlone` 的断言为 IPv6 datagram 不受该规则影响。这是该单项规则的范围，不代表整个平台是否提供 IPv6 出口。[UDP 规则](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/orchestrator/pkg/sandbox/network/firewall.go#L655)、[测试](https://github.com/e2b-dev/infra/blob/6b82caf690b06fb983254714187dc496ac4d0cec/packages/orchestrator/pkg/sandbox/network/firewall_udp_packet_test.go#L565)

## 对 Isodo 规格的约束建议

1. 明确用户 allow 与用户 deny 的优先级，并将平台保护置于用户规则之外；必要平台通道逐项列明。
2. IPv6 首版若不支持，在 Guest 和宿主边界明确禁用并测试；不能只配置 IPv4 nftables 集合。
3. 将“新建连接策略更新”与“强制撤销已有连接”分开承诺；后者需要代理流关闭和已有 UDP / conntrack 的专门验收。
4. 域名策略必须写明可解析的协议与端口；解析后仍要检查目的 IP，不能只检查 Host / SNI 字符串。

未核对：BYOP 全链路、v2 host firewall 所有保护规则、域名控制端口注册、Guest IPv6 路由、运行时策略版本一致性、现存 TCP 流撤销。上述项目不能写成已验证能力。
