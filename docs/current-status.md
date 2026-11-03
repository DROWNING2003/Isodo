# 当前功能、验收与版本状态

更新时间：2026-11-03（Asia/Shanghai）。本页用于说明当前版本，不将早期草案中的“计划实现”或“当时未验证”当成现状，也不把源码存在等同于所有环境已经部署。

## 能力矩阵

| 能力 | 当前实现 | 验证与范围 |
| --- | --- | --- |
| 沙箱生命周期 | 创建、查询、重命名、停止、启动、删除；异步 revision、清理确认 | 远程 CHV 与本地 Lima 均有真实运行记录；停止保盘，启动为新运行身份 |
| Agent 工作台 | T3 任务、模型选择、文件、终端、配对连接 | Codex/gpt-6.1-sol 真实生成文件并运行测试；不据此声明所有模型与 Agent 组合兼容 |
| 文件 | 上传、下载、目录创建、Stat、ListDirectory、Remove、Move | 远程公开 API 已完成真实 CHV 验收；Lima 共用 Guest Files，桌面文件能力有既有实机验证 |
| 命令 | 公开 `commands/run`，stdout/stderr、退出码、超时、取消、幂等 | 远程 CHV 真实命令与失败语义已验收；不提供同等 Lima 公开 Commands 承诺 |
| HTTP 预览 | 端口默认 public、可切 private、持久化策略、HTTP/WS/SSE、限流与连接上限 | 专用 CHV 和公开 HTTPS 链路已验证；T3 仍走独立鉴权 |
| 凭证库 | 账号加密存储、逐沙箱授权、模型配置与占位符、撤销 | 真实模型调用与宿主注入已验证；第三方协议、模型名及 service_tier 必须与服务方兼容 |
| GitHub MCP | 市场、收藏、OAuth、仓库配置入口、绑定、Broker、只读工具 | Web + CHV + Codex 真实工具调用已验证；长期凭证留在 Control |
| Notion MCP | OAuth 元数据发现、DCR、PKCE、刷新、只读工具白名单 | 真人授权与工具发现已验证；指定测试页面的 Agent 端到端读取未独立验收 |
| 自定义 MCP | 账号私有 HTTPS Streamable HTTP，无认证或 Bearer Token | DeepWiki 公共 MCP 经专用 CHV/Codex 真实调用已验证；拒绝内网、重定向与 DNS 重绑定 |
| Skill 市场 | 制作个人 SKILL.md、收藏、远程沙箱选择与同步 | 专用 CHV/Codex 实际加载技能并返回测试标记通过；首版为指令正文，不做仓库导入或附件执行 |
| Guest root | 新 Lima/CHV 标准模板使用 Guest UID/GID 0，完整系统 PATH | 两端真实系统写入、测试 deb 安装/卸载、T3 root 验证通过；不提权宿主 |
| 开发者文档 | OpenAPI/Proto、生成 Go/TypeScript 请求绑定、中文参考与在线调试 | 生成一致性、文档构建和受限调试代理分别校验；独立外部 SDK 产品尚未发布 |

本地 Electron 的模型配置和工作台已接线；市场 MCP/Skill 的完整绑定同步当前优先远程 CHV，不将远程验收自动推广到本地。工作台终端已可用，但不等同于已经公开独立 Processes/PTY/Sessions API。

## 最新真实验收摘要

| 项目 | 证据 | 结果 |
| --- | --- | --- |
| 本地 Guest root | `local-f16ddefa-a48a-4db5-8544-42677ac7a968` | UID/GID=0、工作台连接、T3 root、系统写入、deb 安装/卸载，测试 VM 已清理 |
| 云端默认 root 模板 | `4886709f-0c0b-4083-8b2d-a33482054f6b` | 公开 API 创建 → running → Commands → 包安装/卸载，exit_code=0，清理完成 |
| 云端工作台恢复 | `8c5b7837-adec-4192-a641-173e89d5ead5` | T3 优雅退出卡住导致代理重载失败；仅重启 T3 后真实 Desktop 重新进入工作台，未重建 VM 或工作盘 |
| 本轮代码检查 | PR #39 / #40 | 全量 Go 测试、相关 race、vet、生成一致性通过；pidfd 重启有 4 项 Linux 真实子进程回归 |

上述证据是对应版本、环境和流程的真实结果；不表示每次调用都无故障，也不表示完成了生产规模或全链路安全认证。详细历史输出可在相应 Git 提交和 PR 中追溯。

## 部署与代码版本

- 当前代码基线：`main` 已合并 [PR #39](https://github.com/DROWNING2003/Isodo/pull/39)（Guest root）与 [PR #40](https://github.com/DROWNING2003/Isodo/pull/40)（T3 有界退出兜底）。
- root 默认模板和 Desktop 已部署。远程默认 Build 为 `1988be31-9f56-493e-81ee-9f065c4050c7`，Node 发布目录为 `/opt/isodo/releases/guest-root-20261103-v2`。
- PR #40 已合并但尚未部署到当前 Node；当前工作台通过 T3 进程恢复后已可用。部署补丁需要单独授权维护窗口。
- 当前 Web 为 `mcp-preview-fixed-20261103`，文档站为 `docs-api-url-20261103`，API 镜像为 `isodo-api:mcp-notion-fix-20261103`。各服务不随合并自动更新；本轮文档整理也不代表线上文档已发布。
- GitHub 的 macOS 打包工作流在合并时仍排队，不能记作 CI 通过；本地检查和真实运行结果单独列示。

## 完成范围之外

用户可见的快照上传/迁移、完整跨节点接管、商业套餐与计费、所有 Agent/模型组合兼容、独立外部 SDK，以及完整生产级安全和容量验收，不属于本次路演的完成声明。模板内部的预热快照机制不等于用户产品快照功能。

停止/启动保留工作盘，不保留正在执行的进程内存。Guest root 可以修改本 VM 的系统及 Agent；公开 Files API 仍限定 `/workspace`，宿主隔离、归属、mTLS 和运行身份检查保持原有边界。
