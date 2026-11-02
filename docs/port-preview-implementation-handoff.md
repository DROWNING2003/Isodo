# HTTP 端口预览交接

通用端口的授权、Gateway、Node RelayPort 和 Guest PortForward 链路已实现。当前使用与验收边界见 [HTTP 端口预览](http-port-preview.md)，Public API 合同见 `api/openapi/openapi-public.yaml`。

- PR #24 继续承载本功能；Commands API 来自已合并的 #23。
- 入口为 `POST /v1/sandboxes/{sandboxID}/previews`，固定五分钟授权；API Key 需要独立 `sandboxes:preview` scope。
- 真实 CHV 测试使用独立 VM、mTLS Node、Guest HTTP/SSE/WebSocket 夹具；Control Store 为内存夹具。
- 未变更现有生产 Node、VM、Nginx。生产升级需先 migration 0014，再配套升级 Node/API。
- 完整浏览器跳转、真实 Python/Vite 应用及生产 lifecycle worker 联调不在已验证范围中；不能用夹具通过替代这些验收。
- Lima 后续可复用 Public API 和 RelayOpen 字段，当前保持关闭。

复跑入口：`scripts/test-http-port-preview-chv.sh`。合并仍由用户决定。
