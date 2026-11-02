# HTTP Port Preview visibility 验证（2026-11-02）

实现位于 `drowning/http-preview-visibility`，基于 main `7ead4d1` 的独立 worktree；没有携带上一轮未提交的 Runtime Files 改动。验证期间没有部署或合并。

## 行为与契约

- 未配置的 Sandbox/端口默认 public，直接访问 URL，无需签发、Token 或 Cookie。
- `GET /v1/sandboxes/{sandboxID}/previews/{port}` 查询策略，需要所有权和 `sandboxes:read`。
- `PUT` 同一路径写入 `{"visibility":"private"}` 或 `public`，需要所有权和 `sandboxes:preview`；保留 session Origin/CSRF 检查。
- 现有 POST `/previews` 在 public 下返回 url、visibility；private 下额外返回 authorization_url 和 expires_at。策略 revision 绑定私有令牌，切换后旧授权失效。
- 每个新请求读取当前数据库策略；当前 Gateway 在修改响应前取消已有流。其他 Gateway 的既有流沿用 100ms 轮询检查，无法保证跨进程的零延迟断开。
- 每 Gateway、每 Sandbox/端口 10 rps / burst 20、8 个活跃连接；全局 100 rps / burst 200、256 个活跃连接。预算在 public 的 Node inspection/relay 前获取，private 不消耗 public 预算。超限返回 429 / Retry-After: 1。最多 2048 个桶，空闲一分钟后回收；多副本分别计数。
- 31024、31080、38080 及迁移期旧 1024/1080/8080 禁止通用预览。两种模式都保留 Node RuntimeIdentity、generation、revision、boot_id、guest_epoch、running/busy/VMM 检查。T3 授权逻辑不变。
- migration 0015 持久化策略，以 Sandbox 行锁串行化修改和删除，并由删除意图/逻辑删除 trigger 清理配置。Stop/Start 保留配置，不新增宿主 TCP 监听或 VM 网卡。

## 执行结果

| 检查 | 结果与范围 |
| --- | --- |
| `go test ./...` | 通过；需要外部环境且未设置变量的测试按自身条件跳过 |
| 相关 `go test -race` | preview、sandbox、auth、postgres、Node orchestrator/guesttransport/Lima、Guest Agent、egressproxy 通过 |
| `go vet ./...` | 通过；同时修正已有 process 测试直接复制 protobuf 锁的问题 |
| `make generate-check` | 通过，正式生成 OpenAPI Go HTTP 客户端/服务端、TS 客户端及参考页 |
| Web TypeScript | 退出码 0；原有 Schema.Finite suggestions 保留 |
| Web 连接相关测试 | 3 文件、13 项通过 |
| Fumadocs MDX | 编译通过；未声称完整 Next.js 生产构建通过 |
| `git diff --check` | 通过 |
| 真实 CHV 转发 | 4.05 秒通过，详见下面的日志 |
| 独立 PostgreSQL | 0.07 秒通过，migration 0015 Down→Up 通过 |

### 真实 CHV

运行环境：jfcs1024，Linux amd64/KVM，Cloud Hypervisor 54.0，匹配的 5.15.0-46-generic kernel/Vsock 模块。新建无网卡 RAM-root VM、临时 mTLS Node 服务及临时 Gateway；Sandbox ID `f323e70d-cd11-4a02-a245-6b4ed2ce1e31` 只属于该测试的内存 Store/journal，不向现有业务 API 发出操作。

`TestRealCHVHTTPPortPreview` 覆盖：签发前直接匿名访问 public 成功；private 匿名 401；私有授权正常访问；public↔private 双向切换及旧策略令牌拒绝；3000/5173/8000 GET/POST、SSE、WebSocket、活跃连接撤销，以及真实 VM shutdown/reboot/delete 后身份隔离。

这是实际 Gateway → mTLS Node RelayPort → Guest PortForward → Guest loopback 转发。Control Store/生命周期记录采用内存夹具，不代表真实 PostgreSQL worker 驱动的整轮 E2E，也未验证生产 Nginx、DNS、浏览器或 Electron GUI。本轮没有修改现有 Node、VM、数据库或 Nginx。VM/VMM 和临时证书由测试清理。

### PostgreSQL

专用新容器 PostgreSQL 17.4，专用数据库 `isodo_preview_visibility`，随机临时密码，未读取 `.env`。Sandbox ID `f3e43d95-ec3e-4dc3-beee-992d853ee069`。

`TestPortPreviewPersistenceAndDeletion` 验证：未配置 public、private 写入、Store 重连后的持久化、数据库 stopped/running 状态变更不丢策略、跨租户拒绝、策略版本递增、保留端口拒绝、并发策略写入与 Delete 意图清理，以及删除后不能重建配置。数据库状态切换测试不等同于真实 VM Stop/Start；真实 VM 生命周期隔离由上述 CHV 测试覆盖。

专用容器及密码文件已清理。原始安全日志保留在本 worktree 的 `.tools/preview-visibility-reports/chv-five-digit.log` 和 `postgres-five-digit.log`，未记录 API Key 或预览令牌。

## 复现

- 数据库：`scripts/test-preview-visibility-postgres.sh`，新建容器，不连接已有数据库。
- 真实 CHV：`scripts/test-http-port-preview-chv.sh`，需 Linux amd64/KVM、Go、静态 BusyBox、cpio、匹配 kernel/Vsock 模块，详见 [端口预览文档](../http-port-preview.md)。
- 部署前需在获授权的目标数据库应用 migration 0015，并更新 API；保持已有 Node fencing 确认协议。本次没有执行生产迁移或部署。

## 五位数内部端口补充

本轮按后续要求将 Guest 管理、出站代理、工作台分别迁移为 31024、31080、38080，Go 共用 `internal/runtimeports` 常量，镜像/脚本/探针同步更新。新 Guest 与 Node 在真实 CHV 上通过上述 4.05 秒验收，实际使用新的管理 Vsock 端口 31024。所有新旧保留端口拒绝通用 HTTP 转发，T3 的配对、Cookie、Origin/CSRF 回归测试通过。

完整 T3 bundle 在 38080 的真实镜像构建及浏览器操作、真实 Lima VM、31080 的真实外部模型出站调用没有在本轮另外执行。镜像脚本与 Go 代码已更新，完整镜像需在获授权的发布阶段重建，不能把旧镜像当成新端口镜像使用。Control 宿主监听端口保持原值，现有部署未修改。
