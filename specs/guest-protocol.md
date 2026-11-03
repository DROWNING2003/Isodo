# Guest 协议边界

GuestService 在 VM 内提供文件、进程及端口通道。Node Runtime RPC 是宿主节点的受认证入口；Guest RPC 是 VM 内操作入口，二者不能替代 Control 的账号与所有权检查。

| 能力 | Guest 与公开接口关系 |
| --- | --- |
| 文件 | 上传、下载、建目录、Stat、ListDirectory、Remove、Move 已由 Files API 接通 |
| 命令 | 远程 CHV 公开有界非交互式 Commands；超时、取消与输出完整性由契约明确 |
| 端口 | Guest PortForward 支撑通用 HTTP、WebSocket、SSE 和工作台链路 |
| 交互终端 | 工作台具备终端；这不表示独立公开 PTY/Processes API 已交付 |

调用必须绑定当前运行时身份。文件操作复用 Guest Files 实现，不通过 Shell 模拟。公开文件路径只允许 /workspace，防止父目录、符号链接和路径竞态逃逸，保护运行时目录。Guest root 不放宽公开 Files 权限。

分页 page_token、overwrite 默认值、Remove 递归与不存在路径行为以[Files 契约](../docs/runtime-files.md)和机器协议为准。新增能力先修改 `api/proto/` 并生成绑定，再接通上层与测试。
