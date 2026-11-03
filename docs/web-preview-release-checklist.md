# Web 沙箱预览配置与发布检查

## 现象与根因

页面显示「无法连接到沙箱：预览网关域名与 App 配置不匹配」，但沙箱可能仍在正常运行。该错误来自 app/apps/web/src/api/preview.ts：bootstrapSandboxPreview 会在发出网关认证请求前，检查预览地址是否属于构建时指定的域名及合法端口/Sandbox ID 格式。

2026-11-02 已发生漏传构建参数；2026-11-03 发布 MCP Web 时再次直接执行 vp build，仅提供远程模式和 docs URL，漏掉预览域名及完整工作台模式。问题是构建流程遗漏，不应通过关闭 URL 校验、放开 Cookie 来源或重建 VM 来修复。

Vite 参数会编译进 JavaScript。只修改服务器环境或重启 Nginx 无法修复旧 Web 包，必须重新构建并发布。

## 固定配置

| 层 | 配置 | 当前 drowning.top 部署值 |
| --- | --- | --- |
| Web 构建 | VITE_ISODO_PREVIEW_DOMAIN | drowning.top（不含协议、端口） |
| Web 构建 | VITE_BOUNDLY_WEB_SURFACE | full |
| Web 构建 | VITE_ISODO_REMOTE_ONLY | true |
| Web 构建 | VITE_ISODO_DOCS_URL | https://docs.drowning.top:18443/docs |
| Control | ISODO_PREVIEW_DOMAIN | drowning.top |
| Control | ISODO_PREVIEW_SCHEME | https |
| Control | ISODO_PREVIEW_PUBLIC_PORT | 18443 |

预览地址形如 https://38080-<SandboxID>.drowning.top:18443；端口以实际 App Link 返回值为准。Nginx 的通配预览路由、证书、客户端 DNS/VPN 也需要匹配。该表是当前部署配置，不应照搬到其他环境或 Electron 本地模式。

## 发布流程

1. 核对工作区、发布源、目标服务器与现有 Web 软链接；保留可回退目录。
2. 在仓库根目录只使用以下构建入口：

   ```bash
   bash scripts/build-drowning-top-web.sh
   python3 scripts/check-drowning-top-web-build.py app/apps/web/dist
   ```

   脚本固定四项参数，构建成功后生成 isodo-build.json，记录非敏感配置和产物 SHA256。不要手写清单为旧包补签；清单证明固定脚本的产物一致性，不代替运行时验收。

3. 上传整个 dist（包括 isodo-build.json）到新的远程 release 目录。切换前在服务器执行：

   ```bash
   python3 /path/to/check-drowning-top-web-build.py /opt/isodo/web/releases/<release>
   ```

   缺清单、配置不符或任何文件摘要不符，立即停止。不要把新旧 assets 目录合并。
4. 校验通过后原子切换 /opt/isodo/web/current，保留旧目录。Web 静态包更新不需要重启 Node、操作 VM 或修改数据库。
5. 从实际 HTTPS 入口获取 index.html 与 isodo-build.json，核对与待发布包一致，再检查 API /healthz。
6. 使用专用测试沙箱验证：网关 Cookie 引导成功、descriptor/session 发现成功、工作台加载、文件及终端可访问。记录 Sandbox ID、客户端、结果与清理情况；未运行这一阶段就明确标记未验证，不能用首页200代替。
7. 页面仍加载旧包时强制刷新。确认网络中的 assets 哈希已更新，再判断是否有其他问题。

## 定位与回退

- 此精确报错发生在前端域名校验阶段：先核对构建包，勿先重启 Node。
- 「预览网关认证未完成」则已通过域名检查，应查授权、Cookie、CORS、路由及生命周期。
- DNS NXDOMAIN、TLS 错误、超时分别检查解析、证书和网络，不能统一归为构建配置问题。
- 回退时将 current 原子指向保存的旧目录；保留问题包与构建记录以便追查。

当前发布必须使用固定脚本重建；既有真实工作台验收与版本快照见[能力状态](current-status.md)。不得因文档中存在检查项就声称其已执行。

## Node 到 MCP Broker 的域名解析

Web 能打开不代表 Node 能访问 Broker。当前 Node 使用 ISODO_MCP_BROKER_URL=https://api.drowning.top:18443；内部 DNS 尚未提供该域名时，systemd 私有挂载通过发布目录的 hosts 文件解析。升级 Node 时必须保留原有 PrivateMounts、BindReadOnlyPaths 及其他非版本配置，只替换二进制和对应环境文件路径；不能用只含 ExecStart/EnvironmentFile 的片段覆盖旧 drop-in。

2026-11-03 MCP 升级遗漏了 hosts 挂载，造成 Guest MCP 请求502。恢复 api.drowning.top → 10.210.10.32 的解析后，真实运行沙箱中的 GitHub tools/list 返回200和13个只读工具。已恢复下一次启动所需的 systemd hosts 挂载；本次没有重启活动 Node/VM。

发布检查应包含：在 Node 的实际运行命名空间内解析 Broker 域名并验证 HTTPS /healthz，然后从专用 Guest 验证 initialize 和 tools/list。不要通过关闭 TLS、忽略证书或把秘密放进 Guest 来绕过网络故障。

已有 Codex 会话若启动时 MCP 连接失败，恢复网络后需验证新任务是否重新加载工具；resources/templates 为空不等于 tools/list 为空，验收必须直接检查工具发现或工具调用结果。
