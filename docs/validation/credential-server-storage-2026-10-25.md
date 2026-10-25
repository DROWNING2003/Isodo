# 凭证配置服务端持久化

## 问题

旧实现将密钥保存在服务端，却把 Agent 元数据保存在 renderer localStorage。Agent 元数据缺失后，分组过滤把关联密钥显示为普通 HTTP 凭证。协议未改变，分类依据丢失了。

## 实现

新增 `0010_credential_documents.sql`，按账号、类别、ID 保存加密配置文档。公开契约见 `api/openapi/openapi-public.yaml` 中 `/v1/credential-documents/{kind}` 与 `{kind}/{id}`。`agents` 保存 AgentConfig；`sandboxes` 保存 agentIds、credentialIds 和应用时的 models 快照。只有交互式账号会话或登录 bearer 可访问；沙箱 API Key 禁用，数据库查询与更新始终包含 tenant。

Agent 列表、创建沙箱选择、运行中配置选择、模型目录同步均读取服务端数据。保存对话框等待服务器确认；加载失败不会将未知分类的记录当作 HTTP 数据展示。API 凭证卡提供引用原凭证创建 Agent 的入口。

## 验证

- PostgreSQL 迁移已执行。真实数据库测试使用临时 tenant 完成加密写入、重新创建 Store 后读取、跨账号隔离和删除，测试后清理 tenant。
- Handler 测试覆盖持久化后重新读取、密文检查、跨账号读取/写入/删除限制、API Key 拒绝。
- 前端 7 个文件、24 项聚焦测试通过，Web 类型检查通过。测试覆盖服务端读取、迁移失败保留旧副本、成功迁移清理、不用旧数据覆盖服务端配置，以及列表/创建/模型同步流程。
- API 已重建并加载新接口；匿名访问配置接口返回 401。
- 当前新沙箱没有旧 `/workspace/.codex/config.toml`，未恢复旧 Agent 模型列表；密钥仍保留。没有操作桌面 UI 做点击验证，也没有推送或创建 PR。
