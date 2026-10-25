# base 模板（内置 T3）

`base` 是所有新沙箱的默认起点，T3、终端和文件能力属于基础能力；用途模板应从这个完整基础工件派生，而不是通过名称选择是否支持 T3。`images/linux-base` 仅构建底层 Guest OS；本目录在其之上加入 Node.js、T3 Server bundle 和 Codex/Claude CLI。镜像标签为 `isodo-base:0.1.0-dev`，系统模板正式名称为 `base`。T3 运行在 Guest `127.0.0.1:8080`，由 Node 的 `CreateSandboxAppLink` 通过 Vsock PortForward 暴露。

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

`/sbin/isodo-t3` 由 `/etc/inittab` respawn，以 uid/gid 1000 运行 T3；Guest Agent 以同一用户执行 pairing CLI。工作区是独立 ext4 盘，T3 bundle 不能放在 `/workspace`，否则会被工作盘挂载覆盖。

## 本地已核对的证据（2026-10-25）

在 macOS/OrbStack 上用 `--platform linux/amd64` 构建 `isodo-linux-base:0.1.0-dev` 和 `isodo-base:0.1.0-dev` 后：

- `node --version` → `v24.13.1`；
- `/opt/isodo/t3/bin.mjs`、`@ff-labs/fff-bin-linux-x64-gnu/libfff_c.so`、`codex`、`claude` 存在；
- 容器内启动 `/sbin/isodo-t3` 后 `curl http://127.0.0.1:8080/` → `200`，返回 T3 HTML；
- pairing CLI 返回 credential，scopes 含 `terminal:operate`、`orchestration:read`、`relay:read`。

这些是 Docker 容器内的镜像证据，不是 CHV 模板/Vsock/T3 浏览器端到端证据。

## 真实 CHV 验证（2026-10-25）

在 `jfcs1024`（Linux 5.15、KVM、CHV v54）和 `isodo-test`（Linux 6.8、KVM、CHV v54）上用 `isodo-template probe-t3` 验证了从候选工件冷启动到 T3 的真实 Vsock 链路：

```json
{"sandbox_id":"0f1d5e3f-7916-4124-bbfc-f252fdb87c7a","guest_epoch":"3660356c05f5d82143c7144c92f9d092","boot_to_ready_ms":7262,"boot_to_t3_ms":17792,"t3_http_status":200,"t3_html_ok":true,"pairing_ok":true,"credential_issued":true,"scopes":["orchestration:read","orchestration:operate","terminal:operate","review:write","relay:read"]}
```

```json
{"sandbox_id":"48c06866-4f6b-4a15-9d1d-160f6cbd26da","guest_epoch":"740ba6a6e4c39332a4aae01563d6db38","boot_to_ready_ms":15358,"boot_to_t3_ms":29405,"t3_http_status":200,"t3_html_ok":true,"pairing_ok":true,"credential_issued":true,"scopes":["orchestration:read","orchestration:operate","terminal:operate","review:write","relay:read"]}
```

同时在 `jfcs1024` 上通过 `probe-bound` 验证了两个 t3-base VM 的独立身份、跨绑定拒绝和 ACPI 回收（`boot_to_bound_ready_ms` 1815/1777，`cross_bind_rejected:true`，`shutdown_verified:true`）。

为减小发布体积，后续使用 **4 GiB rootfs** 的 `t3-base-003`（digest `78d5b97aee4fa553c9de3cf1c05a7595d4547ac7aee6debf961318c8158e9366`）。`probe-t3` 在 18.1 秒内返回 T3 HTTP 200/pairing；`probe-node-t3` 走真实 `CHVServer.ApplyOperation` → `CreateSandboxAppLink` → relay → T3，`create_to_app_link_ms=37114`、HTTP 200、pairing token 存在、stop/delete 成功。

`probe-api-t3` 进一步走真实 `sandbox.Handler` + `preview.Publisher` + `httpapi`：`POST /app-link` 返回 `http://8080-<id>.localhost:43123/`，signed cookie 后 Preview Gateway 反代 T3 返回 200，capabilities 为 `["terminal","files"]`，最后 stop/delete 成功。

`probe-node-t3` 还覆盖 **stop → start**：Start 复用同一实例盘做冷启动，产生新的 boot ID/guest epoch，T3 再次返回 200（`restart_to_app_link_ms=11882`），随后 stop/delete。

注意：`jfcs1024` 的候选使用宿主 5.15 内核，打包时用 `ISODO_ALLOW_NO_VMGENID=true` 生成冷启动 initramfs，因此不支持 warm snapshot；需要 snapshot 时应在带 VMGENID 的 guest kernel 上重新打包。

## 未验证

- `base` 的 S3 发布与 Registry 事务（`publish-system` 正在执行/待记录）。
- 非 root 加固、CLI 登录注入和凭证代理。
- ARM64 bundle：T3 当前 bundle 带 x64 原生库，本镜像固定 amd64。
- 云端 API/Preview Gateway 的浏览器端到端（需要启动 API/Node 并配置 wildcard DNS/TLS 或 SSH 端口转发）。

## 出网与 Registry（2026-10-25）

`t3-base-005`（4 GiB，digest `774db6cc221b24207f4e77be752d8e837bb34240dfcc08aa6f3304cb76261dbd`）在 initramfs 中加入 `virtio_net`，Guest `isodo-init` 根据 `isodo.net=<ip>,<gateway>` 配置 `eth0`；Host 侧使用 `scripts/setup-chv-network.sh` 建 `isodo0` bridge/NAT。真实 CHV 结果：`https://1.1.1.1` 301、`https://www.cloudflare.com` 200，T3 仍返回 200。

`probe-node-t3 --network-bridge isodo0` 验证 Node 自动分配 TAP/IP 后的 create→stop→start→T3；本地 SeaweedFS/PostgreSQL 上的 `publish-system` + `probe-node` 验证了 registry 上传/拉取和 cache reuse。生产 isodo-test 的发布仍未完成。
