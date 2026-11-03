# 目录与模块职责

| 目录 | 职责 |
| --- | --- |
| cmd/ | Control、Node、Guest 及管理员工具入口 |
| internal/control/ | 账号与所有权、HTTP Handler、生命周期编排和持久化 |
| internal/node/ | 本机 VM、运行时身份、Guest 通道、转发及资源回收 |
| internal/guest/ | VM 内文件、进程、端口转发与工作台运行 |
| internal/sandboxfiles/ | Control 与运行时适配器共用的工作区文件契约 |
| api/openapi/ | 公开 API 与账号控制 API 的机器契约 |
| api/proto/ | Node 与 Guest 的 RPC 契约 |
| app/ | Electron/Web 客户端、沙箱管理及工作台接线 |
| docs-site/ | 中文开发者站与生成的交互式 API 参考 |
| images/、deploy/、scripts/ | 镜像、部署配置、构建生成及验收工具 |

模块分工按职责表达，具体作者和实际贡献以 Git 提交及 PR 为准，不虚构团队成员职责。协议和生成代码应随源契约一并更新；部署环境配置不得混入长期凭证。
