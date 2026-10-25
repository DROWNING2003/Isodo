# jfcs1024 隔离 SeaweedFS

本轮使用 `compose.remote-demo.yaml`，固定 SeaweedFS 4.48、Linux amd64，运行 `weed mini`。仅 S3 映射到宿主 `127.0.0.1:18333`，Master、Volume、Filer 留在 Compose 网络内。数据独立保存在隔离目录的 `docker-store/`，不复用旧二进制服务的数据目录。

## 镜像中转

在网络可用的机器执行；Mac 必须显式选择 amd64：

```sh
docker pull --platform linux/amd64 docker.m.daocloud.io/chrislusf/seaweedfs:4.48
docker tag docker.m.daocloud.io/chrislusf/seaweedfs:4.48 \
  aslan-spock-register.qiniu.io/las/isodo-seaweedfs:4.48-amd64
docker push aslan-spock-register.qiniu.io/las/isodo-seaweedfs:4.48-amd64
```

本轮推送并在 jfcs1024 拉取的镜像：

```text
aslan-spock-register.qiniu.io/las/isodo-seaweedfs@sha256:8abd9f9e5790b92065d98c75b346445bd40f2233e4b2f91fce632ec080e28b4d
```

## 本轮已部署目录

```text
/root/zkp/isodo-unify-demo-20261025-s3/
  docker-deploy/compose.remote-demo.yaml
  docker-deploy/entrypoint.sh
  docker-deploy/demo.env
  docker-s3.runtime.json
  s3-docker-private-env.json
  docker-store/
```

`demo.env` 只包含隔离目录和镜像 digest。两个 JSON 凭证文件权限为 `0600`，不提交 Git。写入凭证与 Node 的 Read/List 凭证独立，均限制到 `isodo-unify-demo` bucket；Node 原凭证保持不变。入口脚本将配置复制为容器内服务用户所有的 `0400` 文件，再通过官方入口降权。

后续操作应复用现有 Compose 项目；先确认容器与端口，不启动第二份服务：

```sh
ssh jfcs1024 'docker compose \
  --env-file /root/zkp/isodo-unify-demo-20261025-s3/docker-deploy/demo.env \
  -p isodo-unify-demo-s3 \
  -f /root/zkp/isodo-unify-demo-20261025-s3/docker-deploy/compose.remote-demo.yaml ps'
```

若需要恢复该项目，在确认端口仍属于本次隔离服务、凭证配置存在后，使用同一命令前缀运行 `up -d --pull never --wait --wait-timeout 60`。不要对现有 Node/CHV 或其他 Compose 项目执行停止、重建、清理操作。旧 `store/` 与原配置保留，未删除。

## 已定位的问题

旧二进制使用 `server -volume.max=4`。Master 实际状态显示 4 个卷都属于默认 collection，Free=0；模板 bucket 的 collection 没有可写卷。S3 因此返回 500，即使 `/healthz` 是 200。宿主空闲约 101GiB，不能将它解释为宿主磁盘耗尽。

Docker `mini` 预创建 bucket。发布辅助程序不应将 `CreateBucket` 的 `BucketAlreadyOwnedByYou` 当作上传失败；本轮隔离辅助程序直接调用现有 `S3Store.Publish`，没有修改正式存储代码。

运行证据见 [本轮验收](../../docs/validation/unify-seaweedfs-docker-20261025.md)。部署命令参考现有仓库配置及 [SeaweedFS 4.48 mini 源码](https://github.com/seaweedfs/seaweedfs/blob/4.48/weed/command/mini.go)。
