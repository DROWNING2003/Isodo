# 安全连接：账号持久规则与沙箱单次注入

状态：2026-10-25 本地沙箱已实现并验证注入链路；云端沙箱尚未接入宿主机代理。

## 产品模型

Isodo 的安全连接对应 fork.sandbox 的 injection 能力，分为两类：

| 类型 | 归属 | 生命周期 | 使用方式 |
| --- | --- | --- | --- |
| 持久规则 | 账号（凭证库） | 跟随登录用户，跨沙箱复用 | 沙箱在“安全连接”中引用并保存授权 |
| 单次规则 | 当前沙箱 | 只属于该沙箱，删除沙箱时清理 | 在沙箱内直接新建、编辑或删除 |

- **凭证库（Home > 凭证库）** 管理账号级持久规则。密钥不出账号服务端，列表只返回脱敏提示。
- **沙箱 > 安全连接** 勾选要引用的持久规则，或新建单次规则。两类规则在沙箱内合并成同一份有效规则集。
- 持久规则被删除后，沙箱下次保存或打开时会丢弃失效引用；不会把旧密钥继续下发。
- 单次规则可以保存真实密钥，但只写入本地 Node 的加密存储，不写沙箱文件，也不进入快照。

## 数据与接口

控制面新增 `injection_rules` 表。密钥材料使用 AES-256-GCM 信封加密，数据库只保存 nonce、密文和四位脱敏提示。主密钥来自 `ISODO_INJECTION_KEY`（base64 32 字节）或开发环境的 `ISODO_INJECTION_KEY_FILE`（首次启动生成 0600 文件）。

| 接口 | 说明 |
| --- | --- |
| `GET /v1/injection-rules` | 列出当前账号的持久规则，永不返回密钥 |
| `POST /v1/injection-rules` | 新建持久规则，保存加密后的密钥 |
| `GET/PATCH/DELETE /v1/injection-rules/{id}` | 读取元数据、更新或删除；PATCH 省略 `secret` 表示保留原密钥 |
| `GET /v1/injection-rules/{id}/material` | 仅交互式登录可读；返回明文材料，`Cache-Control: no-store` |

API Key 不能读取或修改安全连接，避免 `sandboxes:*` 密钥越权拿到账号凭证。

本地 Node 的 HTTP API 暴露沙箱规则：

| 接口 | 说明 |
| --- | --- |
| `GET /sandboxes/{id}/injections` | 返回脱敏后的有效规则、代理端口和注入统计 |
| `PUT /sandboxes/{id}/injections` | 覆盖保存规则；空密钥会复用同 key 的已存密钥 |
| `DELETE /sandboxes/{id}/injections` | 删除规则并关闭该沙箱代理 |
| `GET /injections/summary` | 持久规则被多少沙箱引用 |
| `GET /injections/ca` | 返回本地代理 CA 证书，用于诊断 |

本地规则整体加密保存在 Node 状态目录的 `injections/injections.enc`，密钥在同目录 `injections.key`（0600）。

## 注入链路（本地 Lima）

```text
Guest 内的 curl / git / Agent CLI
  → HTTPS_PROXY=http://192.168.5.2:<port>
  → 宿主 127.0.0.1:<port> 的沙箱级代理
  → 命中规则：用本地 CA 终止 TLS、改写 Header、转发上游
  → 未命中：CONNECT 隧道直通，不终止 TLS
```

- Node 为每个沙箱启动独立代理（随机回环端口），CA 只保存在宿主，Guest 只拿到 CA 证书。
- T3 启动时写入 `/workspace/.isodo/injection-ca.crt`，并注入 `HTTPS_PROXY`、`NO_PROXY`、`NODE_EXTRA_CA_CERTS`、`SSL_CERT_FILE`、`CURL_CA_BUNDLE`、`REQUESTS_CA_BUNDLE`、`GIT_SSL_CAINFO` 等变量。
- T3 终端与 Agent 子进程继承这些变量。规则变更只更新运行中的代理，不需要重启 T3。
- 已存在的 T3 进程如果启动时还没有注入环境，Node 会在下一次 app link 检查 CA 标记并重启一次 T3。
- 支持 `http`、`openai`、`anthropic`、`gemini`、`qiniu`、`github` 六种规则；支持 `base_url` host/path 匹配以及 `if_headers` / `if_queries` 条件。
- 只修改请求 Header，不记录请求/响应 body，不尝试支持不遵循代理环境变量的客户端。

### 已验证

- 单元测试：CA/叶证书、CONNECT MITM、Header 注入、条件匹配、未命中隧道、密文落盘、空密钥复用、API 脱敏。
- PostgreSQL 集成测试：创建、列表、材料读取、更新保留密钥、删除。
- 真实 Lima Guest：在 Guest 内通过宿主代理请求 `https://httpbin.org/headers`，确认注入 Header 出现在上游响应；该测试同时覆盖本地 Node API、规则存储和代理端口。

### 未验证 / 边界

- 云端 CHV 沙箱尚未接入宿主机注入代理；页面会明确提示。
- 不同 Agent CLI 对 `HTTPS_PROXY` 和自定义 CA 的支持取决于其 HTTP 客户端；已验证 curl 路径，Claude Code / Codex 需要在目标镜像中继续验收。
- 本地代理假定 Lima 宿主网关为 `192.168.5.2`；其他网络模式需要配置 `ISODO_INJECTION_HOST_GATEWAY`。
- 代理只处理 HTTP/1.1；不覆盖 WebSocket 升级、QUIC/HTTP3、非 HTTP 协议或客户端自带证书固定（certificate pinning）。
- 沙箱内进程本来就能发起获批的请求；注入保证的是“不把长期密钥写进沙箱”，不是阻止沙箱使用被授权的服务。

## 运行与验证

```sh
# 1. 迁移数据库并启动 API（首次会生成 certs/injection.key）
make db-migrate
make api

# 2. 重启本地 Node / 桌面端，让 isodo-node 使用新代码
make node-local-t3
# 或重新构建并打开 Electron
cd app && pnpm dev:desktop

# 3. 打开 凭证库 新建持久规则；进入沙箱 > 安全连接 勾选或新建单次规则
```

手工验证代理：

```sh
curl -sS --proxy http://192.168.5.2:<proxy_port> \
  --cacert /tmp/isodo-injection-ca.crt \
  https://httpbin.org/headers
```

`GET /sandboxes/{id}/injections` 返回的 `proxy_port` 和 `GET /injections/ca` 可用于该验证。
