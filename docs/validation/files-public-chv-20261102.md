# Files 公开 API：真实 CHV 验收（2026-11-02）

## 结论与环境

- 测试：`TestFilesPublicAPIRealCHV`，**PASS，32.87s**（含模板完整校验）；公开 API 流程 15.95s。
- Sandbox ID：`6352c8bb-c1cf-4204-9a96-3d612a7f9769`。
- 环境：jfcs1024，Linux amd64/KVM；独立 PostgreSQL 16、真实 SQL Store / API Key 鉴权 / outbox worker、mTLS Node、真实 CHV Guest。API 与 Node 监听测试进程自有的 loopback 临时端口。
- 测试模板从缓存 `66e8d29e7aae3c1d08b153a4d3b1fa2b42a7ad6600d0adb7f6b047453f11a584` 创建独立副本，仅在副本中安装本轮 Guest，并重新生成、校验 manifest/rootfs 摘要；未修改共享缓存或运行中镜像。
- Guest binary SHA256：`eee421c1a0227f10010ecabc6a898800902797e07404f892ccf0f6fbba5e0fe5`。
- 分支：`drowning/files-runtime-api`，基于 main 的 `77d1192`。本记录对应未提交的开发代码；未推送、未部署、未重启现有服务、未修改现有数据库或业务 VM。

## 关键改动

- `internal/sandboxfiles/backend.go`、`metadata.go`：四项 backend 与共享 HTTP DTO/参数/错误处理。
- `internal/control/sandbox/files.go`、`internal/control/orchestrator/files.go`：所有权、运行状态和 Node mTLS 接线。
- `internal/node/orchestrator/files.go`、`internal/node/localapi/files.go`：CHV/Lima 共用 Guest RPC 与本地 HTTP 接线，完整身份/生命周期 fence。
- `internal/guest/agent/files.go`、`files_mutation.go`、平台 rename 文件：复用既有文件实现，补空目录 EOF、安全父路径与原子 no-replace。
- `api/openapi/openapi-public.yaml`、Node `runtime.proto` 及其 Go/TS/文档生成结果。
- `scripts/test-files-chv-e2e.sh`、`files_chv_e2e_test.go`：可重跑隔离真实验收。

## 已验证

所有 Sandbox 生命周期和文件操作均通过公开 HTTP API；真实 API Key、PostgreSQL Store 和 Node 不使用 Mock 或内存替身。测试初始化使用专用账号、模板记录和临时 API Key；模板来源是本机已校验缓存，不覆盖 S3 下载与模板发布流程。

| 流程 | 结果 |
| --- | --- |
| POST 创建 Sandbox，等待 running | 成功，真实 CHV 启动 |
| 上传文本与 75,000 字节二进制文件 | 成功，真实分块传输；下载逐字节验证 |
| Stat | 文件大小、普通文件类型正确 |
| ListDirectory | page_size=1 两页正确，游标透传，第二页无下一页 |
| Move 文件 | 新路径内容正确，旧路径 404；overwrite=false 冲突 409；省略 overwrite 默认覆盖成功 |
| Move 目录 | 非空目录移动成功，旧路径 404 |
| Remove | 文件删除后 Stat 404；重复删除 204；非空目录未递归 409；递归删除和空目录删除成功 |
| 身份、权限和路径 | 无凭证 401；跨账号 404；只读 Key 修改 403；越界/父目录跳转 400；无效分页 token 400 |
| 保护目录与符号链接 | 根目录、.isodo 及指向运行时/工作区外目录的 symlink 父路径修改返回 403 |
| Node mTLS 旧目标 | 旧 generation 明确返回 NotFound；Stop/Start 后旧 revision 明确返回 FailedPrecondition |
| Stop / Start | 停止请求接受后及已停止期间 Files 返回 409；重启后文件逐字节保留 |
| Delete | 删除中 Files 返回 409；deleted_at 完成，Node live/egress 均清理，VM 目录消失 |

Commands 仅在 Guest 内创建对抗性 symlink 测试夹具，没有用 rm/mv/ls 或 Shell 模拟 Files 操作。测试不进入工作台，不调用真实模型或 GitHub MCP。

## 清理与可重复性

脚本使用 tmpfs PostgreSQL 数据目录，并删除自有容器、模板副本和临时 Node/VM 目录；失败时终止测试自有进程组，清理磁盘与数据库，保留诊断日志。首轮准备遇到迁移工作目录和 macOS 打包元数据问题，修正后完整重跑成功；前置失败不计入成功验收。早期临时容器产生的匿名卷也已按专用库名确认归属并清理。

收尾独立检查：测试 VM 目录 0，所属 VMM 进程 0，测试 PostgreSQL 容器 0，测试运行目录 0。临时 API Key 随专用数据库销毁，没有使用现有账号凭证。

重跑（Linux amd64/KVM，需 Docker、debugfs、Python 3、Go）：

```sh
ISODO_CHV_BINARY=/path/to/cloud-hypervisor \
ISODO_CHV_TEMPLATE_ARTIFACT=/path/to/cache/<manifest-digest> \
bash scripts/test-files-chv-e2e.sh
```

脚本默认构建本分支 Guest 与测试程序。模板必须为 cold-boot、raw ext4、非分区格式。新库名必须以 `isodo_files_e2e_` 开头，且不存在已有 Sandbox；脚本不连接现有数据库。

## 其他质量检查与边界

- `go test ./...`、相关 `go test -race`、`go vet ./...`：通过。
- `make generate-check`：通过；OpenAPI、Go/TypeScript 客户端、Node Proto 和生成文档一致。
- Web TypeScript 检查：通过（包含已有 Effect suggestion，不是错误）。
- Isodo 前端测试：30 文件、146 测试通过。
- `git diff --check`、验收脚本 `bash -n`、Linux Guest 与测试二进制构建：通过。
- 单元/组件回归另覆盖原子 no-replace 竞态、完整 Guest 身份字段拒绝、所有不可用生命周期状态、默认覆盖、空目录与保护路径。

本轮完成 Files Stat/ListDirectory/Remove/Move 的公开链路和真实远程 CHV 验收。Lima 共享 backend/RPC 接线，但未新增实机验收；Electron 文件面板未改动或新增 E2E。完整 Runtime API 的终端、进程、网络流式转发、Node/Guest 故障恢复、传输中断及真实 Agent/模型调用仍有独立验收边界，不因本次 Files 成功而标记全部完成。

部署需配套更新 API、Node 和包含本轮安全修复的 Guest 模板；现有运行中 Guest 不会自动获得修复。

## 完整执行日志

下面保留每步 Sandbox ID、状态、耗时和生命周期日志。测试初始化及清理不含真实业务凭证。

```text
=== RUN   TestFilesPublicAPIRealCHV
    files_chv_e2e_test.go:219: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes 状态=202 耗时=11.523929ms
    files_chv_e2e_test.go:222: 运行环境=jfcs1024 隔离 PostgreSQL + 真实 CHV + mTLS；Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769
    files_chv_e2e_test.go:240: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=4.371611ms
2026/11/02 23:12:14 INFO sandbox operation started operation_id=0599fdd5-11d6-4679-8a20-e0b9f14b2e33 sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 queue_ms=3
2026/11/02 23:12:14 INFO sandbox artifact fetched operation_id=0599fdd5-11d6-4679-8a20-e0b9f14b2e33 sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 duration_ms=0 succeeded=true
2026/11/02 23:12:14 INFO sandbox runtime phase started sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 boot_id=1dfca860-a47b-48de-a880-35f0285bd9cd stage=artifact_verify
2026/11/02 23:12:14 INFO sandbox runtime phase completed sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 boot_id=1dfca860-a47b-48de-a880-35f0285bd9cd stage=artifact_verify duration_ms=0 succeeded=true
2026/11/02 23:12:14 INFO sandbox runtime phase started sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 boot_id=1dfca860-a47b-48de-a880-35f0285bd9cd stage=disk_prepare
    files_chv_e2e_test.go:240: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=2.755674ms
    files_chv_e2e_test.go:240: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=4.89294ms
    files_chv_e2e_test.go:240: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=3.938372ms
    files_chv_e2e_test.go:240: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=3.946099ms
    files_chv_e2e_test.go:240: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=3.123327ms
    files_chv_e2e_test.go:240: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=3.191484ms
2026/11/02 23:12:19 INFO sandbox runtime phase completed sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 boot_id=1dfca860-a47b-48de-a880-35f0285bd9cd stage=disk_prepare duration_ms=5173 succeeded=true
2026/11/02 23:12:19 INFO sandbox runtime phase started sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 boot_id=1dfca860-a47b-48de-a880-35f0285bd9cd stage=vmm_boot
2026/11/02 23:12:19 INFO sandbox runtime phase completed sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 boot_id=1dfca860-a47b-48de-a880-35f0285bd9cd stage=vmm_boot duration_ms=136 succeeded=true
2026/11/02 23:12:19 INFO sandbox runtime phase started sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 boot_id=1dfca860-a47b-48de-a880-35f0285bd9cd stage=guest_ready
    files_chv_e2e_test.go:240: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=2.704305ms
    files_chv_e2e_test.go:240: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=3.768426ms
2026/11/02 23:12:22 INFO sandbox runtime phase completed sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 boot_id=1dfca860-a47b-48de-a880-35f0285bd9cd stage=guest_ready duration_ms=2508 succeeded=true
2026/11/02 23:12:22 INFO sandbox operation completed operation_id=0599fdd5-11d6-4679-8a20-e0b9f14b2e33 sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 action=OPERATION_ACTION_CREATE duration_ms=7902
    files_chv_e2e_test.go:240: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=2.482975ms
    files_chv_e2e_test.go:253: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/directories 状态=200 耗时=9.069658ms
    files_chv_e2e_test.go:256: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=%2Fworkspace%2Fe2e%2Fa.txt 状态=200 耗时=10.004414ms
    files_chv_e2e_test.go:257: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=%2Fworkspace%2Fe2e%2Fb.bin 状态=200 耗时=15.16205ms
    files_chv_e2e_test.go:260: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/stat?path=/workspace/e2e/a.txt 状态=200 耗时=5.374079ms
    files_chv_e2e_test.go:268: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/directories?path=/workspace/e2e&page_size=1 状态=200 耗时=5.508652ms
    files_chv_e2e_test.go:271: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/directories?path=/workspace/e2e&page_size=1&page_token=YS50eHQ 状态=200 耗时=5.942378ms
    files_chv_e2e_test.go:274: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/stat?path=/workspace/e2e/a.txt 状态=401 耗时=340.43µs
    files_chv_e2e_test.go:275: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/stat?path=/workspace/e2e/a.txt 状态=404 耗时=1.936332ms
    files_chv_e2e_test.go:277: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/stat?path=%2Fworkspace%2F..%2Fescape 状态=400 耗时=2.187756ms
    files_chv_e2e_test.go:277: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/stat?path=%2Fetc%2Fpasswd 状态=400 耗时=2.197576ms
    files_chv_e2e_test.go:279: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/directories?path=/workspace/e2e&page_token=!!! 状态=400 耗时=5.561371ms
    files_chv_e2e_test.go:281: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/move 状态=403 耗时=1.620741ms
    files_chv_e2e_test.go:282: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/move 状态=404 耗时=1.963663ms
    files_chv_e2e_test.go:283: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/move 状态=200 耗时=5.740564ms
    files_chv_e2e_test.go:284: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/stat?path=/workspace/e2e/a.txt 状态=404 耗时=5.276076ms
    files_chv_e2e_test.go:285: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=/workspace/e2e/b.bin 状态=200 耗时=13.257479ms
    files_chv_e2e_test.go:288: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=%2Fworkspace%2Fe2e%2Fdestination.txt 状态=200 耗时=8.674046ms
    files_chv_e2e_test.go:289: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/move 状态=409 耗时=5.340913ms
    files_chv_e2e_test.go:290: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/move 状态=200 耗时=8.095645ms
    files_chv_e2e_test.go:291: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=/workspace/e2e/destination.txt 状态=200 耗时=8.603179ms
    files_chv_e2e_test.go:294: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 DELETE /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=/workspace/e2e/b.bin 状态=204 耗时=5.499571ms
    files_chv_e2e_test.go:295: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/stat?path=/workspace/e2e/b.bin 状态=404 耗时=5.063078ms
    files_chv_e2e_test.go:296: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 DELETE /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=/workspace/e2e/b.bin 状态=204 耗时=5.210894ms
    files_chv_e2e_test.go:297: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 DELETE /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=/workspace/e2e 状态=409 耗时=5.534185ms
    files_chv_e2e_test.go:298: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 DELETE /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=/workspace/.isodo&recursive=true 状态=403 耗时=2.136831ms
    files_chv_e2e_test.go:299: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 DELETE /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=/workspace&recursive=true 状态=403 耗时=2.070127ms
    files_chv_e2e_test.go:301: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/commands/run 状态=200 耗时=31.005005ms
    files_chv_e2e_test.go:302: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 DELETE /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=/workspace/runtime-alias/proxy.env 状态=403 耗时=4.773196ms
    files_chv_e2e_test.go:303: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/move 状态=403 耗时=4.452677ms
    files_chv_e2e_test.go:312: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/stop 状态=202 耗时=9.16817ms
    files_chv_e2e_test.go:313: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/stat?path=/workspace/e2e/a.txt 状态=409 耗时=1.961843ms
    files_chv_e2e_test.go:314: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=1.917434ms
2026/11/02 23:12:23 INFO sandbox operation started operation_id=ffd2f020-64b9-42fc-a573-ab50f81380b4 sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 queue_ms=1
2026/11/02 23:12:23 INFO sandbox operation completed operation_id=ffd2f020-64b9-42fc-a573-ab50f81380b4 sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 action=OPERATION_ACTION_STOP duration_ms=68
    files_chv_e2e_test.go:314: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=2.60956ms
    files_chv_e2e_test.go:314: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=2.840624ms
    files_chv_e2e_test.go:315: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/directories?path=/workspace 状态=409 耗时=2.310806ms
    files_chv_e2e_test.go:316: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/start 状态=202 耗时=6.817132ms
    files_chv_e2e_test.go:317: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=2.080177ms
2026/11/02 23:12:24 INFO sandbox operation started operation_id=c05f59af-42a6-494e-b190-3e5ac7763dc5 sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 queue_ms=2
    files_chv_e2e_test.go:317: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=2.7957ms
    files_chv_e2e_test.go:317: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=2.786241ms
2026/11/02 23:12:26 INFO sandbox operation completed operation_id=c05f59af-42a6-494e-b190-3e5ac7763dc5 sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 action=OPERATION_ACTION_START duration_ms=2007
    files_chv_e2e_test.go:317: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=200 耗时=3.004741ms
    files_chv_e2e_test.go:321: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=/workspace/e2e/destination.txt 状态=200 耗时=7.55347ms
    files_chv_e2e_test.go:324: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/move 状态=200 耗时=6.084458ms
    files_chv_e2e_test.go:325: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files/stat?path=/workspace/e2e 状态=404 耗时=5.100692ms
    files_chv_e2e_test.go:326: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 DELETE /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=/workspace/moved-directory&recursive=true 状态=204 耗时=5.792654ms
    files_chv_e2e_test.go:327: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 POST /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/directories 状态=200 耗时=5.58371ms
    files_chv_e2e_test.go:328: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 DELETE /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/files?path=/workspace/empty 状态=204 耗时=6.208004ms
    files_chv_e2e_test.go:329: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 DELETE /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769 状态=202 耗时=13.66002ms
    files_chv_e2e_test.go:330: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 GET /v1/sandboxes/6352c8bb-c1cf-4204-9a96-3d612a7f9769/directories?path=/workspace 状态=409 耗时=1.985791ms
2026/11/02 23:12:28 INFO sandbox operation started operation_id=430f2e37-cd73-4899-b5a0-00db97cea1ce sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 queue_ms=1
2026/11/02 23:12:29 INFO sandbox operation completed operation_id=430f2e37-cd73-4899-b5a0-00db97cea1ce sandbox_id=6352c8bb-c1cf-4204-9a96-3d612a7f9769 action=OPERATION_ACTION_DELETE duration_ms=343
    files_chv_e2e_test.go:355: Sandbox ID=6352c8bb-c1cf-4204-9a96-3d612a7f9769 Files 公开 API E2E 成功，总耗时=15.950709101s；VM/磁盘/出站桥已清理，数据库由运行脚本销毁
--- PASS: TestFilesPublicAPIRealCHV (32.87s)
PASS
清理完成：专用数据库、模板副本和临时目录已删除。
```
