# Guest root 运行与发布验证（2026-11-03）

用户授权本地和云端新沙箱默认 root，并授权部署；现有沙箱保持原模板及用户。功能分支为 `drowning/guest-root-runtime`；本记录区分部署验收与代码合并。

## 已发布

- Desktop 已重启并加载新版本地 Node/arm64 Guest；界面正常打开且登录态保留。
- Lima root T3 Base：`isodo-sbx-base-f6db648f5f03`。复用已安装 T3/CLI 的停止 Base 副本，更新 Guest 和 systemd PATH，核对 Guest SHA256 后停止，避免重复下载依赖。User=root，ProtectSystem=no，NoNewPrivileges=yes；宿主目录挂载仍关闭。
- jfcs1024 Node：`/opt/isodo/releases/guest-root-20261103-v2/isodo-node`。
- 云端默认 base Build：`1988be31-9f56-493e-81ee-9f065c4050c7`。
- manifest SHA256：`d7f770a824beed7f41a1922e0c4e7b9c8e183a974865f85122900d31a91ad67b`。
- Node 保留 PrivateMounts、BindReadOnlyPaths、受保护环境文件和 hosts 映射；实际命名空间 DNS、HTTPS Broker `/healthz` 检查成功。未更改 Web、DNS、Nginx、Control 数据库结构或已有 VM 磁盘。

## 真实验证

| 测试 | Sandbox ID | 结果 |
| --- | --- | --- |
| Lima Guest root 集成测试 | `root-test-1793682798558105000` | PASS；42.36 秒；UID/GID=0、HOME、系统目录写入、dpkg audit，测试 VM 删除 |
| CHV 独立运行时 | `77b98302-1d96-4e7c-b460-df3ea9b7e595` | PASS；Guest Ready 1.779 秒；UID/GID=0、/etc 写入、T3 root，独立进程/磁盘删除 |
| Desktop 本地 API 创建 + Guest RPC | `local-f16ddefa-a48a-4db5-8544-42677ac7a968` | PASS；工作台 app-link 成功；Guest/T3 UID=0；测试 deb 真实安装和卸载；exit 0 |
| 云端公开 API 默认模板 + Commands | `4886709f-0c0b-4083-8b2d-a33482054f6b` | PASS；选择最终 Build；UID/GID=0；/etc 写入；测试 deb 安装/卸载；T3 root；exit_code=0、stderr 空、output_incomplete=false |

最终云端首次创建约 67.1 秒，其中新模板冷缓存下载 59.8 秒、磁盘准备 5.0 秒、Guest Ready 2.046 秒；不代表每次创建都需要下载。模板发布器另行验证了 T3 HTTP 和 pairing。

本地 Desktop 重启前 API 列表已为空，`12` 已不在列表中，因此未重新创建或恢复不存在的记录。云端 `vnc` 在升级前 desired/observed 均为 stopped；保留其原 Build `a13ad294-3127-45a4-b4ba-404001a4f14a`、工作盘和停止状态。

## 发现与修复

1. 初次手工 CHV 探测未成功到达 Ready；离线写入脚本未正确处理 `/sbin` 符号链接。改用规范路径并使用仓库 templatebuilder/发布器进行验证，没有放宽身份校验。
2. 第一版 root 的 PATH 缺少 sbin，真实 dpkg 安装找不到 ldconfig/start-stop-daemon，exit 2。补齐 Guest、Lima systemd、T3 启动环境 PATH，增加回归测试；重新打包并发布最终 v2，真实安装/卸载通过。首个失败测试沙箱 `7756a31e-8834-4ba6-9062-309c89380918` 已完成删除。
3. 本地工作台首次请求可能早于 T3 就绪；按就绪状态重试后连接成功。首次失败本地测试 `local-41932fbb-bf62-435f-8ee3-7cc52bd8ed2b` 已另行删除，最终本地列表为空、两个测试 VM 均不存在。

## 自动化与边界

最终源码 `go test ./...`、相关包 race（Guest、Lima、templateartifact、orchestrator）、`go vet ./...`、Shell 语法、`git diff --check` 通过。日志位于本机 `/tmp/isodo-root-*-v2.log` 及云端 release 下的受保护测试日志。

本轮未执行真实模型推理、GitHub/Notion MCP tools/list 或联网 apt 软件源安装；验证的是 root 权限、系统写入、离线 dpkg 安装/卸载、T3 HTTP/pairing 和默认模板链路。云端两个 API 测试的 deletion.completed_at 均已确认，临时测试 API Key 已在验收清理后撤销，不进入源码、Guest 或日志。

回退保留旧 Node drop-in、环境和上一版模板指针。回退须先发布旧已验证模板，再在允许停机时切回旧 Node；不能改写共享模板或运行中 VM 磁盘。策略见 [Guest root 运行策略](../guest-root-runtime.md)。
