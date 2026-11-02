# HTTP 端口预览交接

通用端口的授权、Gateway、Node RelayPort 和 Guest PortForward 链路已实现。当前使用与验收边界见 [HTTP 端口预览](http-port-preview.md)，Public API 合同见 `api/openapi/openapi-public.yaml`。

- PR #24 继续承载本功能；Commands API 来自已合并的 #23。
- 入口为 `POST /v1/sandboxes/{sandboxID}/previews`，固定五分钟授权；API Key 需要独立 `sandboxes:preview` scope。
- 真实 CHV 测试使用独立 VM、mTLS Node、Guest HTTP/SSE/WebSocket 夹具；Control Store 为内存夹具。
- 2026-11-02 用户明确授权后，jfcs1024 演示环境已升级 Node/API 和 scope 约束，旧 `test` 已通过 API 停止。Nginx 未修改。配置、journal 和数据库已备份。
- 真实账号/API Key、Control worker 创建 CHV 沙箱、Commands API 启动 Python HTTP、公开 HTTPS 预览及浏览器授权跳转已验证；完整 Vite HMR 和 worker 驱动的 Stop/Start/Delete 全部组合仍未验证。
- Lima 后续可复用 Public API 和 RelayOpen 字段，当前保持关闭。

复跑入口：`scripts/test-http-port-preview-chv.sh`。合并仍由用户决定。
