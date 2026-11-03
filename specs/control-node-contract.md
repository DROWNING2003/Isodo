# Control 与 Node 运行时契约

Control 负责用户鉴权、沙箱所有权、期望状态、数据库事务和任务登记；Node 负责自己管理的 VM 与实际运行状态。当前 Control 使用 PostgreSQL 持久化与 Outbox 协调，不能把早期 Redis 方案当作运行必需链路。

Control → Node 使用 mTLS Runtime RPC。Node 在向 Guest 发起文件、命令或端口操作前，核对完整 RuntimeIdentity、generation、revision、boot_id、guest_epoch 与运行状态。旧身份和生命周期不匹配的请求必须拒绝，不能因 Control 已鉴权而跳过。

创建及启停是异步动作，调用方根据 revision、last_action 与实际状态等待。删除完成以 deletion.completed_at 为准，清理失败不能直接丢弃资源记录。Node 重启及 Guest 重启的恢复应保持身份隔离。

普通 HTTP 预览复用 Gateway → Node RelayPort → Guest PortForward；T3 工作台的授权独立。停止时不可转发，启动后重新校验身份，旧短期授权失效。实际字段与错误映射以 `api/proto/`、OpenAPI 和实现为准。

验收范围见[当前状态](../docs/current-status.md)；单元测试和服务健康不能替代真实 Guest 验证。
