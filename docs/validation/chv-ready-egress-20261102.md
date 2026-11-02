# CHV Ready 出站初始化验证（2026-11-02）

## 问题与修复

历史问题见 [自定义 MCP Web 验收](custom-mcp-web-chv-20261102.md)：新 Sandbox 首次通过 Commands 启动 Agent 时，没有代理环境文件及出站桥；进入工作台后才就绪。

本次将无网卡、允许出站的 CHV Sandbox 的代理、CA 与 vsock 桥准备前移到 Create/Start 完成之前。准备失败时操作失败，不发布 running。转发仍受运行状态、busy、generation、revision、boot_id、guest_epoch 限制。Stop/Delete 清理桥，Start 为新身份重建。

Commands 自动提供代理与 CA 环境，调用方显式 env 可以覆盖默认值；无需 source proxy.env。工作台仍保留自身 T3 进程环境刷新逻辑，但其打开与否不再决定 Commands 出站初始化。拒绝出站以及显式 NetworkBridge 路径保持原有策略。

## 真实执行范围

- 环境：jfcs1024，Linux/KVM，真实 Cloud Hypervisor 和 Guest。
- 独立临时 Node 状态、UUID VM、vsock 与代理；读取已缓存且完整校验的模板，不更改缓存、现有 Node、数据库或业务 VM。
- 模板 digest：`66e8d29e7aae3c1d08b153a4d3b1fa2b42a7ad6600d0adb7f6b047453f11a584`。
- Sandbox ID：`ab7e58ff-0943-4748-b652-a0fa86b8d5ba`。
- 测试：`TestRealCHVReadyEgress`，总耗时 27.29s（含模板校验），PASS。
- 全程没有创建 App Link 或进入工作台。Guest 内 Python 通过 Commands 的默认代理环境访问 Node 侧临时 HTTP 上游，返回固定标记；VM 无网卡。

| 步骤 | 耗时 | 结果 |
| --- | ---: | --- |
| Create → Ready，含出站准备 | 7.324s | 成功 |
| 首次 Commands 请求上游 | 0.103s | 成功 |
| Stop | 0.103s | 成功 |
| Start → Ready，新 boot 身份 | 2.109s | 成功 |
| Commands 再次请求上游 | 0.105s | 成功 |
| Delete | 0.405s | 成功，VM 目录已删除 |

前两轮测试暴露的是测试配置错误：请求 workspace 扩容不受支持，以及 Stop 携带创建配置被协议拒绝。修正测试后完整通过；失败轮的自有 VM 也由 deferred Close 和独立目录清理回收。

这属于 **真实 CHV 组件集成验证**：生命周期和 Commands 直接调用 Node 实现，上游是固定 HTTP fixture。没有使用公开 HTTP API、真实模型凭证或 Codex/MCP 调用，不能称为该完整链路的 E2E。尚未部署本修复，现有部署不能据此视为已修复。

## 回归与检查

- 增加 Ready 等待出站准备、初始化失败拒绝 Ready、API 模板不依赖 T3 的回归。
- 覆盖身份与生命周期 fence、Stop/Start 桥重建、Commands 默认环境与显式覆盖。
- `go test ./...`：通过。
- `go test -race ./internal/node/orchestrator ./internal/node/injection`：通过。
- `go vet ./...`：通过。
- `make generate-check`：通过，无协议生成差异。
- `git diff --check`：通过。
- 未修改前端与公开协议。

## 重跑

在具备 KVM 的隔离 Linux 主机准备可读的已缓存模板和 CHV binary，设置：

```sh
ISODO_CHV_INTEGRATION=1 \
ISODO_CHV_BINARY=/path/to/cloud-hypervisor \
ISODO_CHV_TEMPLATE_ARTIFACT=/path/to/cache/<manifest-digest> \
go test ./internal/node/orchestrator -run '^TestRealCHVReadyEgress$' -v -count=1 -timeout 6m
```

测试创建自有临时 Node 状态并删除自有 VM，失败时保留诊断目录但清理 VM 数据。验收完整产品链路还需在获准部署后，通过公开 API 新建 Sandbox，配置模型/MCP 并直接运行 Codex，全程不进入工作台。
