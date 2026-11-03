# Guest root 运行策略

本地 Lima 与远程 CHV 的新标准镜像以 Guest 内 root（UID/GID 0）运行 Guest Agent、Commands、终端及 T3 子进程，允许在 VM 内安装系统软件。宿主 Node 权限、mTLS、RuntimeIdentity、generation、boot_id、guest_epoch、沙箱所有权及 Files `/workspace` 路径限制保持原有检查。

标准 PATH 包含 `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`，保证 root 包管理器可以找到系统辅助工具。

HOME 继续使用 `/workspace/.home`，Codex 配置继续使用 `/workspace/.codex`。GitHub/Notion 长期凭证仍由 Control/Broker 管理；Guest root 可以读取该 VM 内所有文件，不能用 Unix 用户隔离保护同一 Guest 内的秘密。用户执行的 root 命令也能破坏本 VM 的 Agent 和系统配置。

## 生效与旧沙箱

- Lima Base provisioner 版本为 `v3-root`，与旧 nobody Base 分离。首次构建新 Base 需要一次初始化，后续复用。
- CHV 必须重新构建 linux-base 和 base 镜像，再打包、探测并发布新模板。仅更新 manifest 或重启旧模板不会变成 root。
- manifest 接受新 `root:0:0` 和历史 `isodo:1000:1000` 的完整组合，不接受混合身份。历史模板仍保留其实际用户。
- 现有 VM 不会自动升级；迁移前确认中断、保留工作盘、使用新模板创建并验证。不要修改共享模板磁盘或离线修改正在运行的 VM 磁盘。
- 只有 `/workspace` 的持久化由现有生命周期契约保证；不要将系统包修改当成可跨模板重建保留的数据。

## 验证与发布

本地使用隔离测试：

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -o /tmp/isodo-root-guest-arm64 ./cmd/isodo-guest
ISODO_LIMA_ROOT_INTEGRATION=true ISODO_GUEST_AGENT_BINARY=/tmp/isodo-root-guest-arm64 go test -v ./internal/node/lima -run '^TestLimaGuestRootIntegration$' -count=1 -timeout=10m
```

必须在实际 Guest 检查 `id -u`、`id -g` 为 0、`/etc` 写入、包管理器，并验证终端和 Agent。源码或镜像启动脚本检查不能替代真实 VM 验收。发布仍遵守 Web/Node 发布清单，保留 Node 的 hosts 私有挂载和 Broker 解析；不需要更改宿主 root 权限或公开管理端口。

2026-11-03 的部署和真实验证见 [发布验证记录](current-status.md)。
