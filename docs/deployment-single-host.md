# 单机部署边界

当前演示主机为 VPN 内 `jfcs1024`（10.210.10.32），不是公网产品入口。HTTPS 域名与当前版本见[部署快照](current-status.md)，详细入口配置见[drowning.top 部署](deployment-drowning-top.md)。历史无证书、无 Node 的 API 探针不作为当前运行说明。

| 服务 | 管理方式与职责 |
| --- | --- |
| Control API | 独立 Nomad/Docker 作业 `isodo-api-pr18-test`，负责账号、持久状态及 Node RPC |
| PostgreSQL / Casdoor | Isodo 独立依赖，不能复用或覆盖其他项目的凭证及数据库 |
| Node | systemd `isodo-node-pr18-test.service`，管理 CHV 和工作盘 |
| Nginx | systemd 独立入口，VPN 地址 HTTPS 18443 |
| 文档 | systemd `isodo-docs.service`，loopback Next.js 经 Nginx 转发 |
| 工件 | SeaweedFS S3 接口，构建上传与 Node 读取权限分别管理 |

`deploy.sh` 提供构建、计划及更新入口，发布应先审查生成配置、Nomad plan、镜像版本与环境文件。迁移不由 API 启动自动执行，应按目标数据库和备份单独审核。不要修改宿主其他 E2B 服务。

Node 升级可能中断 VM，必须在明确授权的维护窗口执行。保留有效 systemd drop-in、PrivateMounts、BindReadOnlyPaths、hosts 映射和证书设置。Node 实际命名空间的 Broker DNS/HTTPS 与专用 Guest 的 MCP tools/list 是发布验收条件，见[发布检查](web-preview-release-checklist.md)。

Web 使用仓库固定构建脚本，上传和切换前都检查构建清单。API、Web、Node、Guest 模板与文档站分别确认版本；合并 main 不会自动更新线上。

桌面打包工作流产出开发 ZIP；未签名、未公证的制品不能作为正式商业发行。源码本地运行的验收与打包制品验收分别记录，不能互相替代。
