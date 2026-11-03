# 文档索引

本目录描述当前产品、接口与维护流程。历史讨论、被替代的实现计划和逐次联调记录已从当前目录移除，原内容可通过 Git 历史查询；当前结论集中在以下入口。

## 建议阅读顺序

1. [项目首页](../README.md)：产品用途、功能、演示与启动方式。
2. [当前功能与验收](current-status.md)：能力矩阵、真实验证、部署状态与边界。
3. [架构与模块分工](../docs-site/content/docs/concepts/architecture.mdx)：控制面、运行面、访问面和数据归属。
4. [中文 API 参考](../docs-site/content/docs/api-reference/index.mdx)：公开接口与调用指南。
5. [路演与冻结清单](roadshow.md)：2026-11-04 路演的步骤和准备项。

## 功能说明

| 主题 | 文档 |
| --- | --- |
| 产品与交互 | [产品概念](product-context.md)、[产品决策](product-decisions.md) |
| 运行时 | [命令](runtime-commands.md)、[文件](runtime-files.md)、[服务端口](runtime-service-ports.md)、[Guest root](guest-root-runtime.md) |
| 应用访问 | [HTTP 端口预览](http-port-preview.md)、[T3 云端运行](t3-cloud-runtime.md) |
| 模型与工具 | [安全连接](secure-connections.md)、[GitHub MCP](github-mcp-market.md)、[Notion MCP](mcp-dcr-providers.md)、[自定义 MCP](custom-mcp.md)、[Skill](skill-market.md) |
| 部署维护 | [托管环境](deployment-drowning-top.md)、[单机部署](deployment-single-host.md)、[云端运行手册](runbooks/cloud-t3.md)、[发布检查](web-preview-release-checklist.md) |
| 契约与来源 | [协议入口](../specs/README.md)、[上游来源](../specs/references.md)、[交付要求](competition-requirements.md) |

API 源文档为 `api/openapi/*.yaml`；Node/Guest 内部接口为 `api/proto/`。`docs-site/content/docs/api-reference/endpoints/` 是生成结果，使用 `make generate` 更新。字段名、operationId、错误码与协议标识保留原样，面向用户的说明使用中文。
