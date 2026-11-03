# Guest 内部服务端口

| 服务 | 当前端口 | 原端口 |
| --- | ---: | ---: |
| Guest Agent：CHV Vsock / Lima Guest TCP | 31024 | 1024 |
| Guest HTTP 出站代理及 Vsock egress relay | 31080 | 1080 |
| T3 工作台 Guest loopback TCP | 38080 | 8080 |

Go 代码共用 `internal/runtimeports` 常量。镜像的 `/sbin/isodo-t3` 和开发脚本使用同一组值。对外工作台主机名相应为 `38080-<sandboxID>.<preview-domain>`；配对、签名 Cookie、Origin/CSRF 和 RelayPort 身份校验流程不变。通用 HTTP 服务仍使用其自己的 3000、5173、8000 等端口。

该端口策略只涉及 Guest 内部服务，不改变 Control 的宿主 HTTP 监听端口、数据库端口或入口 Nginx，不新建 VM 网卡或宿主 TCP 监听。

Node、Guest Agent、基础镜像和 API 必须使用匹配版本；现有镜像内的旧 Guest 不会自动改端口，也不做未经验证的旧端口回退。T3 镜像需要重新构建并发布新模板，旧运行实例的处理须另行取得部署授权。

新运行时不再监听旧的 1024/1080/8080，因此减少与应用内部监听的冲突。迁移期仍禁止把旧管理端口作为通用 HTTP Preview 公开，防止 API 与旧 Node/镜像混用时把旧管理服务匿名开放。新旧两组端口均在 Gateway、Node、Guest 的 http-port 校验及数据库配置约束中拦截；T3 使用独立的已认证入口。

当前新模板与 Node 已采用五位端口；具体部署快照见[当前状态](current-status.md)。旧镜像不自动迁移。
