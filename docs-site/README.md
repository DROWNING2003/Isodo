# Isodo Docs

基于 Next.js 和 Fumadocs 的产品文档站，提供指南、产品概念和由公开 OpenAPI 契约生成的 API Reference。

## 本地运行

需要 Node.js 22.12 或更新版本。

```bash
npm install --prefix docs-site
make docs
```

打开 <http://127.0.0.1:3000/docs>。

## 生成与构建

在仓库根目录运行 `make generate`，统一生成 OpenAPI Go/TypeScript 绑定和公开 API 文档页面。运行 `make generate-check` 检查生成结果是否漂移；运行 `make docs-build` 构建文档站。

公开 API 页面由 `api/openapi/openapi-public.yaml` 生成到 `content/docs/api-reference/endpoints/`。控制面定义 `api/openapi/openapi.yaml` 不会进入用户文档站。生成文件不要手动编辑。

根目录 `make generate` 是仓库所有生成器的统一入口。引入 protobuf/gRPC 生成时，新增 `generate-proto` 子目标并将它加入 `generate` 聚合目标。无需更换生成入口，也无需让 gRPC 生成依赖 Fumadocs。

## jfcs1024 HTTPS 部署

文档域名：`https://docs.drowning.top:18443/docs`。使用现有通配证书和独立 Isodo Nginx；Next.js 只监听 `127.0.0.1:13000`，由 `deploy/drowning-top/isodo-docs.service` 托管，不重启 API、Node 或现有沙箱。

在 Linux 上运行 `npm ci` 和 `npm run build`，配置已启用 Next standalone 输出。发布目录布局：

```text
/opt/isodo/docs/releases/<version>/
  docs-site/             # .next/standalone 的内容
    server.js
    .next/static/        # 额外复制 .next/static
    public/              # 额外复制 public（存在时）
  api/openapi/openapi-public.yaml
```

运行时 OpenAPI 文件需保留在 `docs-site/../api/openapi/`。将 `docs-site/.next/cache` 链接到 `/var/cache/isodo-docs`，由 systemd 的 DynamicUser / CacheDirectory 管理；源码和发布文件保持只读。`/opt/isodo/docs/current` 原子链接到目标版本，更新前备份旧指向和 Nginx 配置，先验证 loopback 服务，再执行 `nginx -t` 与 graceful reload。

DNS 需要将 `docs.drowning.top` 指向 VPN 内的 `10.210.10.32`；无内部 DNS 入口时使用定向解析验收，不将私网 IP 自动写入公网 DNS。Web 构建脚本 `scripts/build-drowning-top-web.sh` 设置 `VITE_ISODO_DOCS_URL`，侧栏“文档”跳转此独立域名。
