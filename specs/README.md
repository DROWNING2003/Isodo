# 当前架构与协议索引

本目录保留维护所需的规格入口。过时讨论和逐次验收日志已删除，历史可由 Git 追溯。

- [当前能力与验收边界](../docs/current-status.md)
- [整体架构与模块分工](../docs-site/content/docs/concepts/architecture.mdx)
- [目录职责](directory-responsibilities.md)
- [Control 与 Node](control-node-contract.md)
- [Guest 协议](guest-protocol.md)
- [模板存储](template-storage.md)
- [登录与 API Key](web-auth-api-keys.md)
- [上游参考与许可证](references.md)

机器契约以 `api/openapi/openapi-public.yaml`、`api/openapi/openapi.yaml` 与 `api/proto/` 为准。Go/TypeScript 客户端和 API 文档通过 `make generate` 生成，不手工修改生成结果。已有 Guest RPC 不等于公开 HTTP API 已支持该能力。
