# 模板对象存储

Compose 使用 SeaweedFS `4.48` 的 `weed mini` 启动单节点 Master、Volume、Filer 与 S3。S3 默认监听宿主 `127.0.0.1:8333`；模板 bucket 为 `isodo-templates`。`isodo-seaweedfs` 持久卷挂载到 `/data`，同时保存文件数据和 filer 元数据。

```sh
make init
make infra-up
make seaweedfs-ready
make test-storage
# 可选：重建存储容器后校验数据仍可读（开发环境）
ISODO_TEST_S3_RECREATE=1 sh scripts/test-storage.sh
```

`make init` 为已有或新建 `.env` 补充随机存储凭证，并生成被 Git 忽略的 `s3.runtime.json`。构建服务使用 `SEAWEEDFS_ACCESS_KEY/SECRET_KEY`；Node 使用独立的 `SEAWEEDFS_READ_ACCESS_KEY/READ_SECRET_KEY`。宿主凭证文件保持 `0600`；启动脚本生成 `seaweed` 用户所有、`0400` 的容器内副本，然后交给官方入口降权运行。两者仅能访问配置的 bucket，Node 凭证只有 Read/List。bucket 由 SeaweedFS 启动时预创建，不开放匿名访问。

配置变量：`SEAWEEDFS_PORT`、`ISODO_TEMPLATE_S3_ENDPOINT`、`ISODO_TEMPLATE_S3_REGION`、`ISODO_TEMPLATE_S3_BUCKET`。修改 bucket 或凭证后运行 `make init` 并重建 SeaweedFS 服务。跨主机 Node 应使用受限网络中的 S3 endpoint 和 TLS。

模板文件使用 `templates/sha256/<manifest_digest>/` 前缀；完整性、发布顺序和回收边界见[模板存储规格](../../specs/template-storage.md)。`make infra-down` 保留数据卷。单节点开发配置不提供高可用或备份。

实现依据：[官方 mini 源码](https://github.com/seaweedfs/seaweedfs/blob/4.48/weed/command/mini.go)、[S3 bucket 范围凭证](https://github.com/seaweedfs/seaweedfs/wiki/S3-Credentials)、[官方 Docker 配置](https://github.com/seaweedfs/seaweedfs/blob/4.48/docker/README.md)。
