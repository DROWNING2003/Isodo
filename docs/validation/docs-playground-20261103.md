# Docs 在线调试地址修复（2026-11-03）

用户截图显示 CreateSandbox 请求发送到 docs.drowning.top:18443/v1/sandboxes，得到文档站 HTML404，cURL 示例还显示 example.com。公开 OpenAPI 缺少 servers 导致调试器和代码示例分别采用相对来源及占位域名。

修复：公开 schema 声明 https://api.drowning.top:18443；文档调试器使用固定目标代理 /api/playground，避免现有 API 不支持 docs Origin 跨域预检的问题。代理仅接受该 API origin 的 /v1/，保留调用者明确填写的认证头，禁止跨来源和任意地址转发，剥离 Cookie/Set-Cookie，请求体上限8 MiB。无服务端认证注入，TLS 校验保持开启。

验证：

- 本机5项 Node测试通过：转发目标/请求体/显式认证头、Cookie隔离、禁止其他host/内部路径/跨站来源、请求大小限制、跨origin重定向拒绝、schema与代理API地址一致。
- 文档 TypeScript 检查、Linux Next生产构建、make generate-check、git diff --check通过。
- 发布目录 /opt/isodo/docs/releases/docs-api-url-20261103，旧目录记录在该目录 previous-current.txt。先用127.0.0.1:13001候选服务校验，再切换current并仅重启isodo-docs。
- 正式HTTPS入口定向解析验收：/api/openapi/public 的servers为API域名；POST调试代理、不带认证返回JSON401及UNAUTHENTICATED，不是HTML404。候选CreateSandbox文档页面包含API域名。
- 未使用用户截图中的API Key，未发出已授权创建请求，没有创建VM，API/Node与活动沙箱未重启。用户浏览器已登录实际创建仍需自行重试。
