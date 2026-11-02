# MCP 管理与自定义 HTTPS 服务

MCP 在全局入口管理，Agent 工作台侧栏不再展示 Skill/MCP 市场。管理页提供市场、我的 MCP、搜索、服务详情和添加入口；沙箱“安全连接”仍负责选择并统一保存要启用的服务。

## GitHub 仓库范围

账号重新 OAuth 授权与 GitHub App 安装范围是两份配置。详情中的“仓库访问 → 在 GitHub 配置”打开 GitHub 的 Installed GitHub Apps 页面；找到 Isodo 对应的 App，点击 Configure，选择 All repositories 或 Only select repositories。组织安装需要相应管理权限。不会通过卸载 App 强迫重新选仓库，也不会自动扩大仓库权限。入口不硬编码某个用户的 installation ID。

参考：[GitHub 官方安装管理说明](https://docs.github.com/en/apps/using-github-apps/reviewing-and-modifying-installed-github-apps)。

## 添加自定义 MCP

1. 在全局 MCP 管理页点击“添加 MCP”。
2. 填写名称和公网 HTTPS Streamable HTTP 地址，限 443 端口。地址不能包含用户名、密码、查询参数或 fragment。
3. 选择无需认证或 Bearer Token；Token 用密码输入框填写，不发送到聊天中。
4. Control 执行 initialize / initialized / tools/list。验证成功才保存；添加本身不授予沙箱权限。
5. 在指定远程 CHV 沙箱的“安全连接”勾选，点击右下角统一保存。新启动的 Codex 会话加载配置。

自定义服务仅当前账号可见，自动列入“我的 MCP”。当前提供添加与删除；更新认证需要移除后重新添加。第一版不支持自定义 OAuth、stdio、SSE-only 旧传输、私网地址或其他端口。原有 GitHub OAuth 继续独立运行，不使用自定义服务的网络策略。

## 安全与运行链路

```text
Guest Codex
  → Guest loopback:31080/isodo-mcp/{marketID}
  → 现有 Vsock 出站桥 / Node
  → 固定 HTTPS Control Broker（绑定 capability）
  → 用户配置的公网 HTTPS MCP（Control 注入 Token）
```

- Control 使用现有 AES-256-GCM Sealer 保存 Token。API 响应、Node RPC 和 Guest 配置不包含真实 Token。
- Node 仅接收加密保存的绑定 capability；Control 只保存 capability 哈希。复用现有 mTLS SyncMCP、RuntimeIdentity、generation/revision、boot_id/guest_epoch 与 running 校验。
- Guest 仅写内部 URL 和固定占位符。多个 MCP 合并到已有 Codex 配置，保留模型及其他手工配置；路径、文件类型、符号链接、TOML、锁和原子写入检查保持不变。
- 自定义 HTTPS 客户端不使用环境代理、不跟随重定向；每次建连解析并验证所有 DNS 结果，再直接拨号已验证的公网 IP，TLS 仍按原始主机名验证。拒绝 loopback、私网、链路地址、常见保留/元数据及混合私网 DNS 结果。
- GitHub 保持固定只读工具 allowlist。自定义服务保存首次发现的工具集合，仅允许调用该集合；工具可以包含写操作，启用前须信任服务及其权限。自定义能力不意味着 GitHub 管理连接可绕过只读限制。
- 每个账号最多 63 个自定义服务，每个沙箱连同 GitHub 最多 64 个绑定。单消息、响应、工具数、连接超时仍有上限。
- 解绑或删除后立即拒绝新请求，在途调用按现有 200ms 授权轮询取消。删除配置会撤销所有相关沙箱绑定；旧 Guest 配置可能保留到下一次同步，但无法继续调用。
- Stop/Start 保留配置；沙箱删除触发器清理两类绑定。账号删除自定义服务会通过外键清理其绑定。

这些控制不阻止已授权的 Agent 读取/处理服务返回的数据，也不消除工具输出中的提示注入。原始 Token 的有限替换不是对恶意服务任意编码输出的信息流证明。

## API 与部署

- `POST /v1/mcp/custom`：仅账号会话，输入 name、endpoint、auth_kind 与可选 write-only token。
- `DELETE /v1/mcp/custom/{customID}`：仅本人可删除，幂等撤销。
- 现有 catalog 包含本人私有服务，`source=custom`，ID 为 `custom-{UUID}`；其他账号不会看到。
- 继续复用 `/v1/sandboxes/{sandboxID}/mcp/{marketID}` 的绑定、解绑和同步。

部署顺序：备份 → `0017_custom_mcp.sql` → 以迁移 owner 执行 `deploy/sql/grant-mcp-market.sql`（明确 app_role）→ 匹配的 API/Node → Web。Guest RPC 和模板不需改变；不新增 VM 网卡、Nginx 规则或公网监听。更新 Node 前需通过公开 API 停止允许操作的专用测试沙箱；不能重启承载未知业务 VM 的 Node。

## 验证边界

真实 PostgreSQL 测试覆盖加密、账号隔离、会话限制、工具调用、未知工具/旧 generation 拒绝、停止准入、删除及在途撤销；其中上游和 Node 是 fixture，不能称为 CHV E2E。组件测试覆盖多 MCP 配置保留、非法 ID 和 symlink 拒绝。前端覆盖搜索/管理接线、自定义认证表单、账号切换、全局与工作台入口隔离。

真实公网服务、CHV 与 Codex 已按 [部署验收记录](validation/custom-mcp-web-chv-20261102.md) 完成无认证服务调用及解绑 403 验证；新沙箱仍需首次工作台连接初始化出站桥，纯 API 冷启动链路尚未完成验收。Bearer Token 的第三方真实验收需要目标服务提供的测试凭证；本轮不能用合成 fixture 冒充。
