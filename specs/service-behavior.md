# 沙箱服务行为基准

状态：2026-10-24，依据用户“主要参考 E2B”的方向收敛的实现规格；不是已完成能力。桌面端延后。后续字段与协议由此展开，不再把常规 SDK 行为作为产品访谈题。

优先级：E2B 定义服务与 SDK 行为参考；Cloud Hypervisor 官方定义 VMM 契约；Cocoon 参考 CHV 资源生命周期及失败回滚。遇到差异时不混合两个 SDK 的隐式行为。

源码依据见 [E2B 调研](research-e2b-behavior.md) 与 [Cocoon 对照](research-cocoon-behavior.md)。E2B 本地固定版本为 `6b82caf690b06fb983254714187dc496ac4d0cec`；未经验证的上游当前版本能力不自动纳入首版。

## 1. 网络

采用 E2B 风格：**默认允许公网出口，可显式关闭或限制目的地**。这取代早期草稿“默认无网卡”。默认开放公网不等于开放宿主与内网，也不代表授权使用第三方凭证。

- 创建参数使用 `network.allow_internet_access`，默认 true；`allow` 与 `deny` 描述目的地策略。关闭公网时，仍可明确授权必需目的地；保留宿主安全拒绝规则。
- 不允许访问宿主控制端口、节点管理网、其他沙箱、私有及 link-local/元数据地址。平台明确提供的 DNS/代理通道作为受控基础设施单独配置，不能因此允许访问整个网段。
- 策略在宿主出口执行，Guest 修改 DNS/环境变量不得绕过。首版按实现能力公开 IP/CIDR 规则；域名和 HTTPS 注入必须经过真实适配验证才宣称支持。
- 凭证授权是单独的 connection：普通联网不获得任何 credential。敏感调用需由受控代理验证连接授权后注入，不能提供一个可任意使用密钥的开放转发入口。
- `allow/deny` 冲突优先级、IPv6、DNS 和重定向规则已在 [网络安全规格](network-security.md) 固定，须在实现后做负例测试；在此之前，不把 E2B 的安全覆盖范围当成 Isodo 已有能力。

验收：普通公网访问成功；关闭公网后未授权目的地失败；宿主/私网/元数据访问失败；授权目的地和凭证授权分别验证。

## 2. 进程与连接

采用 E2B 的**进程归 Guest 所有，客户端连接只是观察/输入通道**。

- Create process 返回稳定执行标识。SDK `run` 是创建后等待；后台模式是创建后立即返回 handle，两者使用同一进程服务。
- 客户端断开、取消订阅或等待请求超时，不自动结束 Guest 进程。SDK 应区分 request timeout 与 command timeout，禁止一个 timeout 参数同时表达两种含义。
- command timeout 是 Guest 执行时限，到期终止对应进程组；显式 Kill/Signal 同样作用于进程，而不是只关闭连接。
- 支持 List、Connect/Attach、Wait、Signal/Kill。终止后返回真实 exit code/signal；连接错误不能冒充进程退出。
- 运行中进程可重连；重连不承诺重放全部输出。首版使用有界输出缓冲，溢出必须显式报告截断；完整日志持久化不作为默认承诺。
- 执行身份绑定 sandbox + boot generation + execution ID；不能只信 PID，以免 VM 重启/PID 复用后误操作其他进程。
- 停止 VM 后旧执行结束且不能恢复 attach；启动 VM 后返回新 boot generation。Guest Agent 自身崩溃后的进程接管另测，不由“客户端断连独立”推导。

与早期草稿的差异：删除“非 detached exec 断连就清理进程组”的规则；显式取消进程应调用 Kill，取消等待仅断开观察。

## 3. PTY

PTY 仍由 Isodo Guest 自己实现，参考 E2B 的进程生命周期：

- Create → Attach → Input/Resize → Exit；PTY 输出是合并字节流，不分 stdout/stderr。
- 断开 Attach 不结束 PTY，显式 Kill 或 VM 停止结束。SDK 将 Disconnect 与 Kill 命名分开，不使用一个含糊的 Close 同时表示两者。
- 重连可继续接收后续输出；不承诺还原完整终端屏幕或无限历史。
- 权限检查、输入序列、并发 attach 和输出容量在 Guest contract 中固定；首版可限制单个写入连接，避免多个控制端互相覆盖输入。
- PTY、非 PTY Exec、持久 Shell Session 是不同概念。首版不额外实现 Cocoon 风格带 cwd/env 状态的 Shell Session RPC。

验收：断连期间进程继续；新连接恢复输入输出；Ctrl+C、resize 正常；Kill 后退出且可获取结果；旧 generation 的 attach 拒绝。

## 4. Stop / Delete / Pause

E2B 的 kill 是销毁，pause 是保留状态；不能把它的 kill 翻译成 Isodo 的保盘 Stop。

| Isodo 操作 | 进程 | 系统盘和工作区 | 后续动作 |
| --- | --- | --- | --- |
| Stop（P0） | 结束 | 保留原节点磁盘 | Start 冷启动，旧进程不恢复 |
| Start（P0） | 新启动 | 使用保留磁盘 | 新 boot generation |
| Delete（P0） | 结束 | 删除实例拥有的系统盘和工作区 | 不可 Start |
| Pause/Resume（后续） | 按显式 snapshot mode 决定 | 由一致工件恢复 | 验证后才开放 |

首版不暴露独立持久 Volume，不承诺“Delete 时保留工作区但没有再挂载 API”。后续 Volume 独立设计。已发布模板与独立产品 Snapshot 不属于实例临时资源，Delete 不应顺便删除它们。

Stop 是 Isodo 已有需求，不声称与 E2B 完全一致。E2B 的 filesystem-only pause 可以作为后续磁盘恢复模式参考，但首版 Stop 仅承诺原节点本地磁盘保留，不等于对象存储备份或跨节点迁移。

客户端连接断开不触发 Stop/Delete。运行时限与数据保留期限必须分别定义；尚未配置自动过期策略时，不隐式用 SDK request timeout 销毁沙箱。

## 5. 详细服务规格

1. [guest-protocol.md](guest-protocol.md)：已补齐 Exec/PTY/Files 字段、stream 帧、deadline、退出、背压、重连与能力版本，附可编译 protobuf；实现和真机验收待完成。
2. [sandbox-lifecycle.md](sandbox-lifecycle.md)：状态转移表、operation 去重、generation/revision、Node journal、逐阶段补偿和崩溃矩阵。
3. [network-security.md](network-security.md)：默认公网、allow/deny 优先级、IPv6 禁用、策略更新/存量连接、凭证代理和鉴权。
4. [template-storage.md](template-storage.md)：正式镜像清单、系统盘/独立工作区、空间配额、Stop 持久化和 Delete/GC 清理。

这四份是后续可实施契约；现有总体 spec 继续保留架构与范围，避免重复定义不同语义。开发与验收要同时覆盖 API/SDK 可见结果和真实 Guest/宿主状态。
