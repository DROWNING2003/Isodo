# base 模板（内置 T3）

当前开发版内部端口已迁移为 Guest 管理 31024、出站代理 31080、T3 38080；Node/Guest/镜像需要匹配升级。以下历史验证记录使用旧版端口，不能当作新端口完整镜像验收。见 [端口迁移说明](../../docs/runtime-service-ports.md)。

`base` 是所有新沙箱的默认起点，T3、终端和文件能力属于基础能力；用途模板应从这个完整基础工件派生，而不是通过名称选择是否支持 T3。`images/linux-base` 仅构建底层 Guest OS；本目录在其之上加入 Node.js、T3 Server bundle 和 Codex/Claude CLI。镜像标签为 `isodo-base:0.1.0-dev`，系统模板正式名称为 `base`。T3 运行在 Guest `127.0.0.1:38080`，由 Node 的 `CreateSandboxAppLink` 通过 Vsock PortForward 暴露。

## 构建

先在 Linux/amd64 构建宿主上构建 `isodo-linux-base`（见 `../linux-base/README.md`），然后把 T3 bundle 放进本目录：

```sh
# 关键：镜像固定 linux/amd64，T3 bundle 也必须按 x64 构建。
# app/ 的 build:bundle 默认使用宿主机架构；在 Apple Silicon 上直接构建会得到
# arm64 bundle，缺少 @yuuang/ffi-rs-linux-x64-gnu，T3 启动会 MODULE_NOT_FOUND。
cd app && TARGET_ARCH=x64 pnpm --filter t3 build:bundle && cd ..
cp app/artifacts/sandbox/t3-server-dist.bundle images/base/t3-server-dist.bundle
docker build --platform linux/amd64 \
  --build-arg BASE_IMAGE=isodo-linux-base:0.1.0-dev \
  -t isodo-base:0.1.0-dev images/base
rm images/base/t3-server-dist.bundle
```

`t3-server-dist.bundle` 不提交到仓库；它由 `app/` 的 T3 server bundle 构建产生。Node 版本、CLI 版本和 bundle 都必须固定，不使用浮动 tag。`TARGET_ARCH` 由 `apps/server/scripts/build-sandbox-node-pty.sh` 和 `build:bundle` 读取；若上游脚本没有该变量，需要按目标架构分别构建 bundle。

bundle 必须同时包含 `dist/client`（T3 Web UI）。缺少它时 T3 启动正常，但 `/` 返回 `503 No static directory configured`；`apps/server` 的 `build:bundle` 应先构建 `apps/web` 并复制到 `dist/client`，再打包整个 `dist`。

## 一键打包（Linux/amd64）

在已准备好 Guest kernel/modules 的 Linux/amd64 构建宿主上，以 root 运行：

```sh
sudo sh scripts/build-base-template-with-t3.sh /var/lib/isodo/template-candidates/base-001
```

脚本会构建 Guest Agent、`isodo-linux-base`、`isodo-base`，检查 bundle 是 x64 且包含 `dist/client`，再调用 `scripts/build-base-template.sh` 生成 `base` 候选目录。`ISODO_T3_BUNDLE` 可覆盖 bundle 路径。

## 打包候选与发布

```sh
sudo env ISODO_BASE_IMAGE=isodo-base:0.1.0-dev ISODO_BASE_NAME=base \
  sh scripts/build-base-template.sh /var/lib/isodo/template-candidates/base-001

.tools/isodo-template verify --dir /var/lib/isodo/template-candidates/base-001 --digest '<manifest-sha256>'
.tools/isodo-template probe --dir /var/lib/isodo/template-candidates/base-001 --digest '<manifest-sha256>' --vmm-binary /usr/local/bin/cloud-hypervisor

.tools/isodo-template publish-system --name base \
  --dir /var/lib/isodo/template-candidates/base-001 \
  --digest '<manifest-sha256>' --vmm-binary /usr/local/bin/cloud-hypervisor
```

发布后，Control API 的模板列表会出现 `base`；选择它创建云端沙箱后，`POST /v1/sandboxes/{id}/app-link` 会返回 T3 pairing 链接。

## Guest 内布局

| 路径 | 用途 |
| --- | --- |
| `/opt/isodo/t3` | T3 Server bundle（只读） |
| `/workspace/.t3` | T3 数据目录（持久工作盘） |
| `/workspace/.home` | T3/Agent HOME |
| `/workspace/.codex` | Codex 配置 |
| `/workspace/.isodo/t3-server.log` | T3 启动日志 |

`/sbin/isodo-t3` 由 `/etc/inittab` respawn，以 Guest 内 uid/gid 0 运行 T3；Guest Agent 以同一用户执行 pairing CLI。工作区是独立 ext4 盘，T3 bundle 不能放在 `/workspace`，否则会被工作盘挂载覆盖。

## 当前验证与发布

新标准模板使用 Guest root，本地 Lima 与远程 CHV 均完成系统写入、测试包安装/卸载及 T3 用户验证。具体 Sandbox ID 和部署版本见[当前状态](../../docs/current-status.md)。

当前内部端口为 Guest 31024、出站代理 31080、T3 38080，镜像、Node 和 API 必须匹配；旧镜像不自动迁移。CHV 模型与 Broker 出站复用 Vsock，不要求额外 VM 网卡或宿主 TCP 监听。

镜像存在、容器探针成功和模板发布是不同阶段。发布前必须真实验证 Guest Ready、工作台配对、模型请求和 MCP 工具调用；只操作专用测试 VM。早期容器及 CHV 探针输出可由 Git 历史追溯。
