# drowning.top：VPN HTTPS 与远程沙箱 Web 部署

核查日期：2026-11-02，客户端时区 Asia/Shanghai。服务器日志时钟与客户端有差异；证书时间以 X.509 UTC 字段为准。

## 已部署的入口

| 入口 | 地址 | 当前验证 |
| --- | --- | --- |
| Web | https://web.drowning.top:18443/sandboxes | 定向解析返回 HTML 200；远程专用构建 |
| API | https://api.drowning.top:18443 | /healthz 200，TLS 验证成功 |
| Casdoor | https://auth.drowning.top:18443 | Discovery 的 issuer/endpoints 已切换 HTTPS |
| Preview | https://8080-UUID.drowning.top:18443 | 缺凭证 401，非法凭证 403；真实工作台待登录验收 |

API、Casdoor 的配置修改和重启已经得到用户明确授权并实施。没有操作原有 Node、数据库服务、Sandbox、公司 Nginx、E2B/client-proxy、公司 DNS 或防火墙。源码与部署配置通过工作分支 PR 交付。

**DNS 尚未就绪，不能直接把域名浏览器访问视为可用。** 用户选择先部署并定向解析验收。curl --resolve 可以通过 VPN 验证 HTTPS；内置浏览器曾报 ERR_NAME_NOT_RESOLVED；用户随后确认 Safari 可以打开 Web，Arc 仍有解析问题。尚未完成 GitHub 登录、T3 Workspace、终端和文件操作 E2E。

## 网络拓扑与隔离

```text
Mac / 浏览器，连接公司 VPN
  Web https://web.drowning.top:18443
      /auth、/v1 同源代理到 Isodo API
  Electron http://app.isodo.localhost:5734
      桌面本地服务代理到 https://api.drowning.top:18443
  两者的 Preview fetch / WebSocket
      → https://<port>-<sandboxID>.drowning.top:18443
                      │ 公司 VPN
                      ▼
jfcs1024 10.210.10.32:18443，独立 isodo-nginx.service
  web  → /opt/isodo/web/current 静态资源；/auth、/v1 → API :5004
  api  → Isodo API :5004
  auth → Casdoor :18000
  <port>-<uuid> → Preview Gateway :5004
                     → Node gRPC/mTLS 127.0.0.1:19192
                     → CHV Guest → T3 :8080
```

核查时 80、443、18443 均没有监听者；原有 Nginx 位于容器内，监听 50001，master PID 3973；client-proxy PID 10360，监听 3001/3002；Node PID 29361，测试 unit 为 isodo-node-pr18-test.service。后续核查这三个 PID 未变，client-proxy /health 前后均 200。

采用 18443 隔离端口：443 空闲不等于已确认归 Isodo 使用，本次未接管。未修改 /etc/nginx/nginx.conf、nginx.service 或原有 Nomad Nginx Job。API Job 仅更新 isodo-api-pr18-test。SeaweedFS S3 仍在 127.0.0.1:18334，未操作。

## DNS 与定向解析

权威 NS 是 dns9.hichina.com、dns10.hichina.com。业务域名没有在 Mac 当前 DNS 中解析出地址；没有完整枚举 DNS zone，不能据此声称没有其他用途的记录。未向阿里云公网 DNS 写入私网 A 记录。

公司内部 DNS 管理员后续需配置或确认：

```text
web.drowning.top   A 10.210.10.32
api.drowning.top   A 10.210.10.32
auth.drowning.top  A 10.210.10.32
*.drowning.top    A 10.210.10.32
```

如内部 DNS 接管整个 zone，保留其他必要记录或配置转发，不覆盖根域现有用途。公网发布私网地址会暴露内部网络信息，可能触发 rebinding 防护，也不提供公网连通性；需公司政策和单独授权。

API 容器仅通过 Nomad Docker extra_hosts 映射 auth/api 到 10.210.10.32，供自身 OIDC 请求使用。这不是公司 DNS 修改，不解决浏览器的通配解析。

Mac 定向验收：

```bash
curl --noproxy '*' --resolve api.drowning.top:18443:10.210.10.32 \
  https://api.drowning.top:18443/healthz
curl --noproxy '*' --resolve auth.drowning.top:18443:10.210.10.32 \
  https://auth.drowning.top:18443/.well-known/openid-configuration
curl --noproxy '*' --resolve web.drowning.top:18443:10.210.10.32 \
  https://web.drowning.top:18443/sandboxes
```

`scripts/open-drowning-top-web-check.sh` 为 Mac Chrome 准备独立测试 profile，以进程内 `--host-resolver-rules='MAP *.drowning.top 10.210.10.32'` 打开 Web；不改 /etc/hosts、DNS 或 TLS 校验。脚本通过 bash -n；Chrome 独立 profile 的实际浏览器流程仍待验证。Arc 未应用这些启动参数，因此脚本不再将 Arc 作为回退。

## TLS 与续期

Let's Encrypt ECDSA 证书 SAN：drowning.top、*.drowning.top；issuer YE2。有效期 UTC：2026-11-01 15:38:58 至 2027-01-30 15:38:57。

- /etc/ssl/drowning.top/fullchain.pem
- /etc/ssl/drowning.top/privkey.pem，root:600；父目录 700。
- /opt/isodo/acme，root:700；account.conf 为 600，含 DNS 续期凭证，不可复制到仓库、聊天或日志。

acme.sh 固定 3.1.2，提交 40290ad42a37aba57eb246e507c11944a52c0f68；源码在 /opt/isodo/acme-src。安装未修改 shell profile、未添加 cron。用户已通过交互式隐藏输入完成首次 DNS-01 申请；AK/SK 未进入脚本参数或聊天。

`deploy/drowning-top/issue-certificate.sh` 只用于首次安装；发现已有证书即退出，**不要重复申请**。专用 RAM 用户仅授予固定版本 dns_ali 所需的 DescribeDomainRecords、AddDomainRecord、DeleteDomainRecord，资源尽量限制为 drowning.top；具体资源限制支持范围按阿里云权限模型核实。

isodo-acme-renew.timer 每日服务器本地时间 03:15 加最多一小时随机延迟，Persistent=true。执行 acme.sh --cron，成功安装后调用 renew-reload.sh，只重载独立 Isodo Nginx。已实际执行一次检查，Result=success、ExecMainStatus=0；未到续期窗口，不等于真实续签已验收。

```bash
systemctl list-timers isodo-acme-renew.timer
systemctl start isodo-acme-renew.service
systemctl show isodo-acme-renew.service -p Result -p ExecMainStatus
```

不要启用 acme debug 或发布包含 DNS 签名请求、账户配置的日志。

## Nginx 和静态 Web

主机没有可直接复用的 Nginx 二进制。复用本机已安装容器镜像的固定 digest：

```text
aslan-spock-register.qiniu.io/las/nginx@sha256:e6b408afd0fcc6786c31e80825f4d2c6ca76934aa3e9280152addb23202ffd5d
```

实际版本 1.28.3。新容器名 isodo-nginx，由独立 systemd service 管理；host network，但只监听 10.210.10.32:18443。只读根文件系统，临时目录 tmpfs，削减 capabilities。

配置 /opt/isodo/nginx/nginx.conf，日志 /opt/isodo/nginx/logs，PID /opt/isodo/nginx/run/nginx.pid。配置源文件及管理脚本在仓库 deploy/drowning-top/。

只将明确的 api/auth/web 域和 `<数字端口>-<UUID>` 预览域路由到固定上游，未知 Host 返回 421。保留原始 Host；设置 X-Forwarded-Proto=https、Port=18443，覆盖 X-Forwarded-For，清除外部 Forwarded。预览服务仍由应用校验签名、Sandbox 和端口，不允许请求选择任意内网代理地址。

HTTP/1.1、WebSocket Upgrade、禁用请求体/响应缓冲、一小时读写超时已配置；请求体上限 256 MiB。SSE、终端和文件操作的端到端效果仍待真实工作台验收。访问日志不记录 URI/query/Cookie，避免 Preview 路径凭证泄露；error log 按敏感运维数据保护。日志轮转尚未配置。

Web 使用 /opt/isodo/web/current 指向 /opt/isodo/web/releases/20261102-hosted。资源挂载只读；/auth、/v1 保留原始 Web Host 代理至 API；其余路径使用 SPA fallback。构建产物未发布 .map/.vite，Nginx 同时拒绝 sourcemap 和隐藏路径。远程 Web 构建使用根路径 base=/，避免深层路由刷新时相对 assets URL 错误。

```bash
/opt/isodo/nginx/check-config.sh
systemctl status isodo-nginx.service
systemctl reload isodo-nginx.service
```

## API、身份与 OAuth 配置

测试 API 本地镜像 isodo-api:drowning-top-20261102，镜像 ID：

```text
sha256:d28045b051e1342c38f412ed9ab285247f2bea9dd925e374287a97ef5f395a3c
```

本地交叉编译静态 linux/amd64 API 二进制，在服务器基于原测试镜像 ID 叠加构建；Nomad force_pull=false，未推送镜像仓库。构建目录 /opt/isodo/build-api-20261102。Nomad Job validate 成功，部署成功、Healthy=1。运行配置 /root/zkp/isodo-pr18-test/api.env：

```dotenv
ISODO_CASDOOR_ISSUER=https://auth.drowning.top:18443
ISODO_CASDOOR_INSECURE_HTTP_HOST=
ISODO_IDENTITY_NAMESPACE=http://10.210.10.32:18000
ISODO_WEB_ORIGIN=http://app.isodo.localhost:5734
ISODO_ADDITIONAL_WEB_ORIGINS=https://web.drowning.top:18443
ISODO_PREVIEW_DOMAIN=drowning.top
ISODO_PREVIEW_SCHEME=https
ISODO_PREVIEW_PUBLIC_PORT=18443
ISODO_PREVIEW_CROSS_SITE_COOKIE=true
```

数据库、Preview secret、Node RPC、mTLS 与注入密钥保持原值。IDENTITY_NAMESPACE 只保留旧本地账户映射键：Casdoor token 仍必须通过新 HTTPS issuer 的认证，外来 issuer 不允许映射。避免仅因 provider URL 更换就为同一用户创建新租户；未执行身份数据 SQL 迁移。

额外页面 Origin 对应独立 Web 登录/CSRF handler，由请求原始 Host 的精确配置表选择，忽略 X-Forwarded-Host；默认 API 域继续使用 Electron 原 Origin。Preview 仅接受 Electron 原 Origin、明确配置的 Web Origin 和当前预览自身 Origin。没有通配 CORS。

Casdoor Compose 仅重建 casdoor service，origin 改为 https://auth.drowning.top:18443。应用保留 Electron callback，并添加 https://web.drowning.top:18443/auth/callback。Casdoor 启动会重新导入 init_data.json，因此已同时修改持久化初始化文件和运行中应用；仅改 API 数据会在重启时丢失新增 callback。

已实际检查两个入口的登录重定向：Web callback 为 Web HTTPS 地址，状态 Cookie Secure/HttpOnly/SameSite=Lax；API/Electron callback 仍为 http://app.isodo.localhost:5734/auth/callback。两者 PKCE 均 S256。PKCE、state、nonce 和回调校验没有放松。GitHub→Casdoor provider 回调仍需真实登录核对，不能与 Casdoor→应用 callback 混用。

## 远程专用 Web 与 Electron

Web 发布必须先阅读 [Web 预览配置与发布检查](web-preview-release-checklist.md)，使用固定脚本并检查产物：

```bash
bash scripts/build-drowning-top-web.sh
python3 scripts/check-drowning-top-web-build.py app/apps/web/dist
```

不要直接执行 `vp build` 手工拼环境变量。`VITE_ISODO_PREVIEW_DOMAIN` 与 `VITE_BOUNDLY_WEB_SURFACE=full` 缺失时，首页仍可能返回200，但工作台无法连接。上传后、切换 Web 软链接前，再在服务器验证完整发布目录的配置清单和文件摘要。

远程专用模式隐藏本地列表/创建入口，不发起本地列表或模板请求，忽略缓存的本地卡片和演示沙箱；未登录时禁止创建演示沙箱。登录后模板与创建走现有远程 API。Electron 保留原有本地能力，不设置 VITE_ISODO_REMOTE_ONLY=true。

`scripts/dev-drowning-top-desktop.sh` 准备 Electron 静态构建，API URL 为 https://api.drowning.top:18443，页面仍在 http://app.isodo.localhost:5734；不创建 SSH 隧道、不操作远端 Node。需 DNS 就绪后运行。原有 dev-pr18-test-desktop.sh 针对旧域名、会建立隧道，不用于新流程。当前已有 Electron 进程未重启。

Preview Cookie：跨站模式仅允许 HTTPS 且必须有明确 AppOrigin；host-only、HttpOnly、Secure、SameSite=None，不扩大 Cookie Domain。无 Origin 的写操作拒绝；外来 Origin 拒绝。Gateway 转发至 Guest 前继续删除 isodo_preview 和 isodo_session。前端 fetch 使用 credentials:include，bootstrap 移除 Pairing fragment，T3 凭证不会随 HTTP 路径发送。浏览器第三方 Cookie 策略仍须真实 Electron 检查，不能凭 Go 测试断言通过。

## 验收结果与限制

| 项目 | 结果 |
| --- | --- |
| Nginx 语法、独立启动、reload | 通过 |
| TLS SAN/有效期/私钥权限 | 通过；Mac curl 验证为 0 |
| API HTTPS /healthz | 200 |
| Web SPA HTML 与深层路径 | 200；根路径 JS/PNG 返回 200 且 MIME 正确，缺失 JS 和 .map 返回 404 |
| OIDC Discovery | HTTPS issuer、authorization/token/JWKS endpoint |
| 两种页面 callback、PKCE | 重定向检查通过；完整 GitHub 登录未验证 |
| Preview 缺凭证/非法凭证 | 401 / 403 |
| 外来 Origin/无 Origin 写操作 | 403 / 403 |
| Web 合法 CORS preflight | 204 |
| 未知 Host | 421 |
| 新 HTTPS app-link / bootstrap | 端口、Cookie、ready=204 的 Go 测试通过；真实用户链路未验证 |
| 创建 CHV、Pairing、Workspace、WS101 | 未验收；未创建或操作现有 Sandbox |
| Terminal、上传下载、Stop/Start | 未验收 |
| 非法/过期/外租户 token | Go 测试通过；实时网关额外检查非法凭证拒绝 |
| 原有 E2B/client-proxy | 已检查的 /health 200，PID/监听保持；不是全业务 E2E |
| 无隧道浏览器/Electron | 定向 HTTP 验收通过；浏览器缺 DNS，完整链路待验 |
| 自动续期 | timer enabled，检查成功；真实到期续签尚未发生 |

前端相关 28 项测试通过；Web typecheck 通过（存在仓库原有 suggestion 诊断，退出码 0）；远程构建通过。Go auth/preview/sandbox/API 测试通过；auth/preview/API race 检查通过。已检查 bash -n、systemd 配置、git diff --check。

下一步必须用独立验收 Sandbox 测试：GitHub 登录/回调 → create → HTTPS app-link scheme/端口 → bootstrap204 → Pairing → Workspace → WS101 → 连续终端输入 → 文件内容校验 → Stop/Start 重入 → 非法/过期 token 拒绝。未强行启用额外 client_credentials grant，也未借用、伪造用户登录绕过 OAuth 验收。

## Node 升级与 MCP 检查

升级 Node 前必须保留并比较有效 systemd 配置，尤其是 PrivateMounts、BindReadOnlyPaths 和 hosts 映射。2026-11-03 遗漏 hosts 挂载造成 Node 无法解析 Broker，GitHub MCP 返回502；账号授权和绑定仍正常。按 [发布检查中的 Node/MCP 步骤](web-preview-release-checklist.md#node-到-mcp-broker-的域名解析) 验证实际 Node 命名空间与 Guest 工具发现，不以进程 active 或首页200作为完成依据。

## 排障和回滚

备份目录 /opt/isodo/backups/drowning-top-20261102，权限 700，配置文件 600。包含原 api.env、compose.yaml、API Job、Casdoor 应用和 init_data、旧独立 Nginx 配置/service。这些文件可能含密钥，不可贴到聊天或提交。

- API 回滚：还原备份 api.env，再使用备份 api-job.json 重新注册仅 isodo-api-pr18-test；恢复旧镜像、配置和原 issuer。不要改动 Node。
- Casdoor 回滚：还原原 compose.yaml 与 init_data，按备份恢复应用 redirectUris，再只重建 casdoor service；不执行整个 compose down/up，不重启数据库。
- Web 回滚：原子切换 /opt/isodo/web/current 到保留版本；首次部署前没有 Web，无需删除现有业务。
- 新入口完全停用：systemctl disable --now isodo-nginx.service；不操作 nginx.service 或其他容器。
- 停止证书维护：另执行 systemctl disable --now isodo-acme-renew.timer；保留受保护证书与账户配置供恢复。

新预览 401 检查 Cookie；403 检查 Origin/签名/端口绑定；502 检查 Node relay/Guest 服务，不擅自重启 Node。证书失败检查 SAN、DNS、系统时间和监听，不使用 -k/webSecurity 绕过。若迁移 443，先确认端口归属，协调修改 listen、Forwarded-Port、issuer、callback、API URL 与 PUBLIC_PORT，不字符串替换生成后的 app-link。

参考：[acme.sh DNS API](https://github.com/acmesh-official/acme.sh/wiki/dnsapi)、[Nginx WebSocket](https://nginx.org/en/docs/http/websocket.html)、[Casdoor API 认证](https://casdoor.ai/docs/basic/public-api/)、[Casdoor 应用更新](https://casdoor.ai/docs/application/overview/)。

## Mac 本机 DNS 修复（准备完成，尚未启用）

用户再次反馈 NXDOMAIN 后核查：Arc 主进程没有 host-resolver-rules 参数，原临时浏览器启动方法未生效；定向 curl 仍返回 Web HTTPS 200、TLS 校验 0。Mac 已有 Homebrew dnsmasq 2.92。

`scripts/setup-drowning-top-local-dns.sh` 已通过 bash -n 及 dnsmasq 配置语法检查，尚未执行系统修改。用户可在终端运行：

```bash
sudo bash /Users/drowning/Desktop/qiniu/Isodo/scripts/setup-drowning-top-local-dns.sh install
```

该脚本建立独立 LaunchDaemon，仅监听 127.0.0.1:15353；通过 /etc/resolver/drowning.top 将 drowning.top 及子域映射为 10.210.10.32。其他域的 DNS 路径不变，不修改公司 DNS，不开放外部 DNS 端口，不关闭 TLS 验证。规则重启后保留，也覆盖未来的动态 Sandbox 子域；会覆盖本机对整个 drowning.top 域的解析。检测到其他用途的同名 resolver 或服务配置会停止。需用户输入管理员密码。完成后完全退出并重新打开 Arc，保持 VPN 连接，访问带 :18443 的 URL。

移除：同一命令把 install 改为 remove；只移除带本脚本标记的 resolver 和 LaunchDaemon。


## 文档服务与凭证库调整（2026-11-02）

- 文档站入口：`https://docs.drowning.top:18443/docs`；Web 侧栏“文档”通过 `VITE_ISODO_DOCS_URL` 跳转此域名。
- Linux Node.js 24 / Next.js standalone，独立 `isodo-docs.service`，DynamicUser、只读发布文件和独立 CacheDirectory；仅监听 `127.0.0.1:13000`，由现有独立 Isodo Nginx 代理。
- 当前文档发布 `/opt/isodo/docs/releases/docs-20261102-v2`，`/opt/isodo/docs/current` 为原子软链接。发布包含 standalone、static、public 和独立公开 OpenAPI 文件。配置见 `deploy/drowning-top/isodo-docs.service`，发布步骤见 `docs-site/README.md`。
- Web 发布 `/opt/isodo/web/releases/ui-docs-20261102`，凭证库移除旧 MCP 凭证区块，既有数据库记录未删除，MCP 市场入口保留。
- Nginx 配置及旧 Web 指向备份在 `/opt/isodo/backups/before-docs-20261102/`；只平滑重载 Isodo Nginx，未重启 API、沙箱 Node 或操作 VM。
- 首页已更新 Skill/MCP 验收边界，新增 Skill 使用指南。原生 Linux 构建与文档运行成功；Web 类型检查、生产构建及 15 项相关前端测试通过。Next 构建含已有 highlight 选择器 CSS 优化警告，不影响构建成功。
- HTTPS 定向解析验收：`/docs`、`/docs/guides/skills`、`/docs/api-reference/endpoints/ListSkills`、`/api/search?query=Skill` 和 `/api/openapi/public` 均返回 200；公开 schema 包含 Skill 和自定义 MCP 接口。API 原有 `/healthz` 仍为 200。
- 当前内置浏览器已实际打开文档域名，并验证凭证库不再显示旧区块、侧栏文档链接正确；这不表示所有客户端/服务器 DNS 均已配置。
- 用户明确选择不改 DNS、先定向解析验收。内部 DNS 后续需要 `docs.drowning.top A 10.210.10.32`，没有将私网记录写入公网 DNS，也没有修改本机 hosts。

定向验收命令：

```bash
curl --noproxy '*' --resolve docs.drowning.top:18443:10.210.10.32 \
  --fail https://docs.drowning.top:18443/docs
```

回退时将 Web / docs 的 current 指向保留的上一个版本；文档目录切换后只重启 isodo-docs.service。Nginx 可从上述备份恢复，先 nginx -t 再 reload；无需回滚数据库。
