# 统一 API 接手与 Docker 存储验收

日期：2026-10-25（Asia/Shanghai）。工作分支：`drowning/unify-sandbox-api`；实际 worktree 为 `/Users/drowning/.codex/worktrees/unify-sandbox-api/Isodo`。入口目录 `/Users/drowning/Desktop/qiniu/Isodo` 在 `main`，两个目录的既有修改均保留。

## 接手时核实

- 统一 API、Control/Node/Guest Files、App client/transport、IPC 分块上传、CHV Vsock 出站与注入实现已存在于未提交修改中，本轮没有重复实现。
- 远端独立 Node PID 50961，监听 `127.0.0.1:19191`；旧 Node PID 1285，监听 `127.0.0.1:9190`。本轮未重启或修改这两份 Node，也未创建、停止或删除 VM。
- 原 `probe-node-t3.log` 记录真实 Guest Ready、T3 HTTP 200、重启后的 T3 HTTP 200、停止和删除成功；这是历史探针证据，本轮没有重跑，不能代替 App/Agent 验收。
- 本机 Control `127.0.0.1:8080/healthz` 返回 200；已有 SSH 转发监听本机 `19191` 与 `18333`。
- 本轮使用 Control 的 CA、客户端证书与私钥，经现有 SSH 转发握手独立 Node：证书链与 ServerName 校验通过，TLS 1.3、ALPN h2。这里只证明 mTLS 传输，尚未证明真实生命周期 RPC。
- 当前 Electron 页面可见已登录账号及本地已停止沙箱，但 Vite PID 24311 的 cwd 为主目录 `app/apps/web`，尚未加载统一 API worktree。

## 本轮处理与验证

仅替换本次隔离 SeaweedFS。停止前核对其 PID、exe 与隔离数据目录；旧数据保留。Compose 项目为 `isodo-unify-demo-s3`，容器为 `isodo-unify-demo-s3-seaweedfs-1`，S3 仍使用 `127.0.0.1:18333`。

镜像已通过仓库中转并在远端按 digest 拉取；部署配置、目录与复用命令见 [运行说明](../../deploy/seaweedfs/remote-demo.md)。容器最终状态为 running/healthy，S3 health 返回 200。

| 验收 | 实际结果 |
| --- | --- |
| 4GiB rootfs 与配套模板工件发布 | `S3Store.Publish` 成功 |
| Node Read/List 凭证从 S3 下载全部工件 | `S3Store.Fetch` 后 `Verify` 成功 |
| 68MiB multipart、不可变重复发布、匿名拒绝、只读写入拒绝、损坏工件拒绝 | `TestSeaweedFSArtifactRoundTrip` 通过，127.35 秒 |
| Control sandbox/sandboxworker、Node localapi/orchestrator、sandboxfiles、egressproxy | 六个相关包 `go test` 全部通过 |
| Compose 配置 | `docker compose config --quiet` 通过 |

已发布 manifest digest：`edf495245891879fed47e8ac7bd4789b406c5ad14af66d84750e000529c705e8`。

本机 Control 新注册系统模板：`base-unify-demo-20261025`，template ID `11b6b30d-f6aa-4801-ad02-b4d01d9baaad`，build ID `73591551-8061-4451-aa71-d5c34472cc4f`。未替换原 `base`。

脱敏结果及工具保留在本地忽略目录 `.tools/unify-demo-candidate/`：`s3-docker-roundtrip.log`、`register-docker.log`、`storage-operator/`；远端结果为 `publish-docker.log` 和 `fetch-docker.log`。原始服务日志可能含认证材料，不能直接复制进交付物。

## 尚未完成

- 将 Electron/Vite 开发服务切换到统一 API worktree。
- 本轮真实 Control → SSH/mTLS → 独立 Node → CHV 创建和 Files 验收。
- 全新配对会话、Preview Cookie/descriptor/WebSocket、真实 Agent 模型任务。
- Docker 重建后的持久性故障演练、完整双端 demo、最终仓库交付核对。
- 赛方截止时区仍待确认；现有要求只记录 2026-10-26 09:00。

用户交接约束最后一句不完整，已请求确认隔离 VM 创建/清理及本地开发服务切换范围；确认前不执行这些动作。本轮没有 Git commit、Git 分支推送或创建 PR；镜像推送已执行。
