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
