# 云端 T3 沙箱运行手册

面向 Linux/KVM 部署主机。所有命令以 root 在 Isodo 仓库根目录执行；不要把真实秘密写进命令历史。协议与未实现边界见 [T3 云端运行时](../t3-cloud-runtime.md)。

## 1. 前置

- Linux x86_64，`/dev/kvm` 可用，Cloud Hypervisor v54（或与模板 manifest 匹配的版本）
- Docker（构建 `t3-base` 镜像）
- PostgreSQL 与 SeaweedFS S3（模板 catalog / artifact）
- Casdoor（真实浏览器登录）
- Guest kernel：warm snapshot 需要 `CONFIG_VMGENID`；冷启动可用无 VMGENID 内核
- Node 24 的 T3 bundle **必须 linux/x64 且包含 `dist/client`**

## 2. 构建 4 GiB t3-base

先构建基础镜像与 T3 镜像（见 [images/t3-base/README.md](../../images/t3-base/README.md)），然后：

```sh
# 冷启动内核（无 VMGENID）；有 VMGENID 时去掉 ISODO_ALLOW_NO_VMGENID
ISODO_BASE_IMAGE=isodo-t3-base:0.1.0-dev \
ISODO_BASE_NAME=t3-base \
ISODO_ALLOW_NO_VMGENID=true \
ISODO_ROOTFS_SIZE=4G \
sh scripts/build-base-template.sh /root/zkp/template-candidates/t3-base-003
```

产物包含 `manifest.json`、`vmlinux`、`initramfs.cpio`、`rootfs.raw`；digest 是 manifest 的 SHA-256。

## 2.1 CHV 出网

在 Node 宿主执行一次（需要 root；脚本只改 `isodo0` 相关规则）：

```sh
sh scripts/setup-chv-network.sh isodo0 10.77.0.0/16
# 在 .env 中设置
# ISODO_CHV_NETWORK_BRIDGE=isodo0
```

脚本会创建 `isodo0`、`10.77.0.1/16`、IP forward、NAT，并用 `ISODO-CHV` 链拒绝 10/8、172.16/12、192.168/16、169.254/16、100.64/10。Guest init 根据 Node 注入的 `isodo.net=<ip>,<gateway>` 配置 `eth0`、默认路由和 DNS；initramfs 必须包含 `virtio_net`。

## 3. 验证

```sh
TOOL=.tools/isodo-template
DIR=/root/zkp/template-candidates/t3-base-003
DIGEST=<manifest digest>
CHV=/root/zkp/.tools/cloud-hypervisor-v54

$TOOL verify --dir "$DIR" --digest "$DIGEST"
$TOOL probe --dir "$DIR" --digest "$DIGEST" --vmm-binary "$CHV"
$TOOL probe-t3 --dir "$DIR" --digest "$DIGEST" --vmm-binary "$CHV"
$TOOL probe-node-t3 --dir "$DIR" --digest "$DIGEST" --vmm-binary "$CHV"
$TOOL probe-api-t3 --dir "$DIR" --digest "$DIGEST" --vmm-binary "$CHV"
```

`probe-node-t3` 覆盖 create → app-link → stop → start → T3 → stop → delete；`probe-api-t3` 覆盖 app-link + Preview Gateway + T3。

## 4. 发布系统模板

在部署主机准备 `/root/zkp/.env`（`POSTGRES_*`、`SEAWEEDFS_*`，可选 `ISODO_DATABASE_URL`、`ISODO_TEMPLATE_S3_*`）：

```sh
sh scripts/publish-t3-template.sh "$DIR" "$DIGEST" "$CHV"
```

脚本构造：

- `ISODO_DATABASE_URL`（默认 `postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@127.0.0.1:$POSTGRES_PORT/$POSTGRES_DB?sslmode=disable`）
- `ISODO_TEMPLATE_S3_ENDPOINT=http://127.0.0.1:${SEAWEEDFS_PORT:-8333}`
- `ISODO_TEMPLATE_S3_REGION=us-east-1`
- `ISODO_TEMPLATE_S3_BUCKET=isodo-templates`

发布成功后，`system_template_catalog` 出现 `t3-base`，API 创建沙箱时可解析。

> 8 GiB rootfs 的发布曾把测试机 I/O 打满；优先使用 4 GiB，并先确认主机磁盘/SeaweedFS 健康。

## 5. 启动 API / Node

API 需要：

- `ISODO_DATABASE_URL`、Casdoor 配置、`ISODO_NODE_RPC_TARGET`
- `ISODO_PREVIEW_DOMAIN`（本地 `localhost`；云端 wildcard DNS）
- `ISODO_PREVIEW_SECRET`（随机 32+ bytes）
- `ISODO_PREVIEW_SCHEME`（`http`/`https`）
- Node mTLS 证书/密钥

Node 需要：

- `ISODO_NODE_BACKEND=chv`
- `ISODO_NODE_STATE_DIR`、`ISODO_CH_BINARY`
- `ISODO_TEMPLATE_S3_ENDPOINT`、`ISODO_TEMPLATE_S3_BUCKET`、`SEAWEEDFS_READ_*`
- mTLS server 证书/CA、Node ID

启动后：

1. 浏览器登录，创建沙箱选择“云端”和 `t3-base`。
2. 打开沙箱；API 调用 `POST /app-link`，Gateway 设置 cookie 并 302 到 `/pair`。
3. T3 进入工作台；终端/文件在 T3 内使用。
4. 停止/启动使用 `POST /v1/sandboxes/{sandboxID}/stop` 和 `/start`；两者返回 202，客户端轮询 `observed_state`。Stop 保盘，Start 产生新 boot ID/guest epoch。

本地浏览器访问远端 API 时，可用 SSH 端口转发把 API 端口映射到本机；`*.localhost` 会解析到本机，Host 头会经隧道到达 API。

## 6. 排障

| 现象 | 检查 |
| --- | --- |
| T3 `network is unreachable` | Guest 未启用 loopback；`images/linux-base/isodo-init` / `images/t3-base/isodo-t3` 需要 `ip link set lo up` |
| T3 `unexpected EOF` | 探针过早半关闭；依赖 `Connection: close`，不要先 `CloseWrite` |
| `Cannot find module @yuuang/ffi-rs-linux-x64-gnu` | bundle 是 arm64；用 `TARGET_ARCH=x64` 重建 |
| `503 No static directory...` | bundle 缺 `dist/client` |
| T3 启动 >60s | 512 MiB 内存过小；使用 2 GiB，检查 CPU 争用 |
| `publish-system` 主机失联 | rootfs 过大/磁盘 I/O；改用 4 GiB，先扩容或错峰 |
| app-link 501 | Node 不支持 `CreateSandboxAppLink`；检查 Node 版本/能力 |
| app-link 409 | 沙箱未 running |
| Preview 403/重定向循环 | `ISODO_PREVIEW_SECRET`/Host/Domain 不匹配；cookie 是 host-scoped |

## 7. 未实现

- 真实 API/Node 两个独立进程的 mTLS E2E
- Connection Gateway 的凭证注入（CHV 网络/出网已实现）
- 真实 Casdoor + 真实 PostgreSQL 的浏览器 E2E
- warm snapshot（需 VMGENID 内核）
- 生产 isodo-test 的 4G Registry 重发
