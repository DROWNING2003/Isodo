# E2B Template 与 Sandbox API 调研

核查日期：2026-10-25。依据 E2B 官方文档及当前 JavaScript SDK v2.58.0 reference；先读了[参赛要求](competition-requirements.md)。未实际运行 E2B 构建或沙箱，官方能力不能作为 Isodo 已实现的演示证据。

## 核心比较

| 维度 | E2B 官方接口 | Isodo 当前证据与边界 |
| --- | --- | --- |
| 模板输入 | `Template().fromImage(image, credentials?)`、`fromDockerfile(contentOrPath)`、`fromTemplate(idOrAlias)`；可组合 copy、安装依赖、命令、用户、工作目录和 readiness。[SDK reference](https://docs.e2b.dev/sdk-reference/js-sdk/v2.58.0/template.md) | 公开 CreateTemplate 仅名称、描述、可见性；BuildRecipe 为 `source_build_id`、可选 `commands: string[][]`、`publish_tag`，没有 image、Dockerfile 或 context 上传字段。[公开 OpenAPI](../api/openapi/openapi-public.yaml#L1088) |
| 异步构建 | `buildInBackground` 返回 name/templateId/buildId，`getBuildStatus` 查询状态和日志。[Build](https://docs.e2b.dev/template/build.md) | 有构建模型和队列/租约框架；Worker 需要注入 Builder、Publisher，API 接受任务不能证明真实镜像构建已接通。[Worker](../internal/control/template/worker.go#L27) |
| 执行接线 | 官方 SDK 提供模板构建流程。[Build](https://docs.e2b.dev/template/build.md) | 主代理全仓核查未发现生产 Builder 实现或模板 worker 启动入口；API 入口仅挂载 `template.NewHandler(store)`。[入口](../cmd/isodo-api/main.go#L104) |
| 本地模板 | 模板构建生成云端预置环境快照。[How it works](https://docs.e2b.dev/template/how-it-works.md) | Node 另有 TemplateCatalog、guest argv 命令模板及本地 `/templates/{id}/build`；这是 Lima 本地路径，不能代替公开云端模板构建。[本地模板](../internal/node/localapi/templates.go#L24) |
| SDK runtime | `e2b` JS/Python SDK 提供生命周期、命令、文件、PTY 等；Code Interpreter 是另一个扩展接口。[官方索引](https://docs.e2b.dev/llms.txt) | 已核查 Web 客户端为 Orval 生成的 fetch client，位于被忽略的 app 原型；不能据此宣称正式公开 TS/Python SDK 已交付。[客户端](../app/apps/web/src/api/public.generated.ts#L1) |

## Docker image / Dockerfile 的实际限制

`fromImage` 使用 registry 镜像；支持通用私有 registry username/password，以及 AWS ECR、GCP Artifact Registry helpers。它不是已证实的任意本地镜像归档导入接口。[Base image](https://docs.e2b.dev/template/base-image.md)、[Private registries](https://docs.e2b.dev/template/private-registries.md)

通过 `/etc/os-release` 识别发行版，当前支持 Debian/Ubuntu、Fedora/RHEL 家族、Arch、Alpine。`scratch`、distroless、原始 `nixos/nix` 等不可直接使用；缺少所需包或受限仓库的极简镜像也可能不支持。NixOS 使用官方 `e2bdev/nixos`。[Base image](https://docs.e2b.dev/template/base-image.md)

**`fromDockerfile` 是向 Template SDK 定义的转换器，官方明确不支持多阶段 Dockerfile。** 不应宣传为兼容任意 Dockerfile / 完整 Docker build。[Base image](https://docs.e2b.dev/template/base-image.md)

| Dockerfile 指令 | 文档列明的转换 / 限制 |
| --- | --- |
| FROM | 设置基础镜像；每个模板只能选择一次 base image |
| RUN | 转成 runCmd |
| COPY / ADD | 转成 copy；不能据此推断具备 Docker ADD 的远程 URL、自动解压等完整语义 |
| WORKDIR / USER | 转成 setWorkdir / setUser |
| ENV | 转成 setEnvs，支持 key=value 和 key value；模板环境变量仅构建期有效 |
| CMD / ENTRYPOINT | 转成 start command，默认 readiness 等待 20 秒；不是每次创建沙箱再启动 |
| EXPOSE / VOLUME | 跳过，不支持 |

指令表：[Base image](https://docs.e2b.dev/template/base-image.md)；环境变量：[Defining template](https://docs.e2b.dev/template/defining-template.md)；启动语义：[Start & ready commands](https://docs.e2b.dev/template/start-ready-command.md)。官方支持表没有承诺 ARG、HEALTHCHECK、SHELL、ONBUILD、BuildKit RUN --mount、COPY --from 或各指令 flags 的兼容性；这些应标为**未核实**，不能推断为支持或一律报错。Parser 源码未完成核验：候选 raw 路径 404，GitHub contents API 限流 403。

复杂 Dockerfile 可考虑在外部 Docker/CI 构建并推送最终镜像，再 fromImage；这是集成建议，仍须满足发行版、镜像大小与 readiness 条件。官方 FAQ 建议用多阶段构建缩小**输入镜像**，不代表 fromDockerfile 转换器支持多阶段。[Build limits](https://docs.e2b.dev/faq/build-limits.md)

## 构建轮询与日志

- `Template.build` 等待完成；`buildInBackground` 返回标识后再轮询。选项包括 CPU、内存、cache、onBuildLogs；minFreeDiskMb 是构建后剩余空间目标，不是总盘大小或 Sandbox.create 参数。[Build](https://docs.e2b.dev/template/build.md)
- **状态以 reference 为准**：`TemplateBuildStatus = 'building' | 'waiting' | 'ready' | 'error'`。教程仅循环 building，直接照抄会遗漏排队状态。建议 waiting/building 继续，ready 成功、error 失败，加应用 deadline/backoff。[SDK reference](https://docs.e2b.dev/sdk-reference/js-sdk/v2.58.0/template.md)
- `getBuildStatus({templateId, buildId}, {logsOffset})` 返回 logEntries；官方示例每次 offset 加本批条目数，间隔 2 秒。reason 可含 message、step、相关日志；旧 `logs: string[]` 已弃用，V1 移除后始终为空。[Build](https://docs.e2b.dev/template/build.md)、[Reference](https://docs.e2b.dev/sdk-reference/js-sdk/v2.58.0/template.md)
- onBuildLogs 接收 timestamp/level/message；level 为 debug/info/warn/error，另有 debug 级 LogEntryStart/LogEntryEnd。默认 logger 支持 minLevel。[Logging](https://docs.e2b.dev/template/logging.md)
- **REST 与 SDK 分页不同**：`GET /templates/{templateID}/builds/{buildID}/logs` 使用毫秒时间戳 cursor、limit（默认/最大 100）、direction、level、source；返回 logs 内 timestamp/message/level/可选 step。不能混用 SDK status 的条目 offset。[Get build logs](https://docs.e2b.dev/api-reference/templates/get-build-logs.md)
- 固定限制：整个构建/单命令 1 小时、readiness 10 分钟、单次 copy 10 分钟、layer 上传 30 分钟、排队等待 40 分钟。同 name/tag 的新构建取消旧构建，并行应使用不同 name/tag。[Build limits](https://docs.e2b.dev/faq/build-limits.md)

## Sandbox SDK runtime

| 需求 | 用法与边界 |
| --- | --- |
| 从模板创建 | 构建快照含文件系统和运行中进程；start command 构建末尾只执行一次。每次创建需要的工作应在创建后 commands.run；创建时 envs 不会追溯进入已快照的进程。[How it works](https://docs.e2b.dev/template/how-it-works.md)、[Start & ready](https://docs.e2b.dev/template/start-ready-command.md) |
| 生命周期 | Sandbox.create/connect/setTimeout/kill；JS timeout 为毫秒、Python 秒。setTimeout 从现在重设；默认超时 kill，可配置 pause。[Lifecycle](https://docs.e2b.dev/sandbox.md)、[Persistence](https://docs.e2b.dev/sandbox/persistence.md) |
| 命令 | commands.run 支持 onStdout/onStderr；background 返回可 kill/wait、按 pid connect 的 handle。默认命令超时 60 秒；timeoutMs: 0 禁用命令超时，不取消 sandbox 寿命。[Streaming](https://docs.e2b.dev/commands/streaming.md)、[Background](https://docs.e2b.dev/commands/background.md) |
| 断线恢复 | 后台命令可在客户端退出后继续，前提是 command/sandbox 未超时。原始输出 stream 不保证向后来的连接重放；跨进程收集应存 sandboxId/pid 并重定向输出到文件。流需重连策略。[Background](https://docs.e2b.dev/commands/background.md) |
| 文件 | files.read、files.write 和批量 write；构建期 copy context 与运行期文件 API 分开。[Read & write](https://docs.e2b.dev/filesystem/read-write.md) |
| 暂停恢复 | 默认 pause 保存内存和文件；connect 自动恢复，可选 filesystem-only。kill 后不能恢复，客户端网络连接需要重连。[Persistence](https://docs.e2b.dev/sandbox/persistence.md) |

## 对 Isodo 的含义

1. 分别验证登记模板、提交构建、worker 执行并上传产物、ready 后创建沙箱；不能以队列状态或可注入接口代替生产执行链。
2. 镜像导入、Dockerfile 转换、基于已有 build 的 argv recipe 是三个能力；当前公开 schema 仅能证明第三类输入契约。
3. 构建日志与 runtime 输出分开设计：前者需要状态、错误与可恢复分页；Agent 长任务还需 sandboxId、pid、持久输出和断线恢复。
4. 若增加 Dockerfile 导入，公开支持子集、明确处理多阶段和未知指令，并验证 ENV、CMD/ENTRYPOINT、ADD、flags 的实际语义。本调研不代表这些能力已实现。

检索限制：按要求尝试 web_search，provider 返回 HTTP 402 Insufficient Balance；随后 web_fetch 读取官方 llms.txt 与上述页面。旧 e2b.dev/docs 域名跳转至 docs.e2b.dev。未执行 git 操作；本次仅新增本文件。
