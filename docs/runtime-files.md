# Sandbox Files API

Files 通过公开 HTTP → Control 所有权/scope/状态检查 → Node mTLS Runtime RPC → Guest Files RPC 操作 `/workspace`。不通过 Shell 模拟文件操作，不新增宿主机端口或 VM 网卡。

## 接口与契约

| 功能 | HTTP | API Key scope |
| --- | --- | --- |
| 上传 | `POST /v1/sandboxes/{id}/files?path=...&overwrite=true` | `sandboxes:write` |
| 下载 | `GET /v1/sandboxes/{id}/files?path=...` | `sandboxes:read` |
| Stat | `GET /v1/sandboxes/{id}/files/stat?path=...` | `sandboxes:read` |
| ListDirectory | `GET /v1/sandboxes/{id}/directories?path=...&page_size=100&page_token=...` | `sandboxes:read` |
| 创建目录 | `POST /v1/sandboxes/{id}/directories` | `sandboxes:write` |
| Remove | `DELETE /v1/sandboxes/{id}/files?path=...&recursive=false` | `sandboxes:write` |
| Move | `POST /v1/sandboxes/{id}/files/move` | `sandboxes:write` |

交互式会话也可使用，沿用现有 Cookie/CSRF 校验。API Key 不能跨账号操作 Sandbox；其他账号资源返回 404。停止中、已停止、启动中和删除中的 Sandbox 不接受 Files；生命周期冲突返回 409。

Node 对 generation、revision、boot_id、guest_epoch 与 running/busy 状态进行校验，Guest 再校验完整 RuntimeIdentity。调用完成后 Node 再检查生命周期，不向客户端确认过期实例的结果。

### 元数据

Stat/Move 返回 `path`、`name`、`kind`（`regular`、`directory`、`symlink`）、`size_bytes`、十进制权限位 `mode`、RFC3339 `modified_at`。符号链接还返回原始 `symlink_target` 元数据，不沿最终链接读取文件。Stat 可查询 `/workspace` 自身。

### 分页

ListDirectory 返回 `{entries: [...], next_page_token: "..."}`；空目录的 entries 是空数组。条目按名称字典序排列，page_size 默认 100，范围 1–500。next_page_token 非空时原样传回 page_token。每次调用重新读取有界目录列表，token 是“最后名称”游标，不能视为并发变更期间的一致性快照。单目录最多 4096 条目，超限返回 413；无效 token 返回 400。

### 删除

Remove 成功返回 204，不存在路径也成功。文件及最终符号链接删除本身；空目录可直接删除，非空目录必须显式 recursive=true。递归删除预检查最多 4096 个条目，超限返回 413。删除不是事务：并发文件变更、I/O 故障可能产生部分完成，调用方应重新查询确认。

`/workspace` 根、`.isodo`、`.t3` 及其子路径禁止删除/移动。Remove/Move 的父路径逐段通过 no-follow 目录 FD 固定，拒绝符号链接父目录，避免通过别名修改保护目录。`../`、非规范路径、工作区外路径被拒绝，Guest 的根目录边界进一步限制路径解析与递归操作。

### 移动与覆盖

Move 请求体：

```json
{"source":"/workspace/project/a.txt","destination":"/workspace/project/b.txt","overwrite":true}
```

公开 API 默认 overwrite=true，与已有上传约定一致；Guest 底层 bool 默认值不变，由 Node 显式传递公开契约的值。overwrite=false 使用 Linux renameat2(RENAME_NOREPLACE) 原子拒绝已存在目标；macOS 测试使用对应的排他重命名。

只有普通文件可覆盖已有普通文件，目录不被覆盖。目标父目录必须已存在；移动相同路径返回当前元数据。原路径不存在返回 404，拒绝覆盖返回 409。移动期间若连接断开或生命周期变化，客户端须通过 Stat 查询两端，不能将失败响应视为未发生变更。

## 调用示例

```sh
curl -fsS -H "X-API-Key: $ISODO_API_KEY" \
  "$ISODO_API/v1/sandboxes/$SANDBOX_ID/files/stat?path=/workspace/project/a.txt"

curl -fsS -H "X-API-Key: $ISODO_API_KEY" \
  "$ISODO_API/v1/sandboxes/$SANDBOX_ID/directories?path=/workspace/project&page_size=10"

curl -fsS -X POST -H "X-API-Key: $ISODO_API_KEY" -H 'Content-Type: application/json' \
  -d '{"source":"/workspace/project/a.txt","destination":"/workspace/project/b.txt","overwrite":false}' \
  "$ISODO_API/v1/sandboxes/$SANDBOX_ID/files/move"

curl -fsS -X DELETE -H "X-API-Key: $ISODO_API_KEY" \
  "$ISODO_API/v1/sandboxes/$SANDBOX_ID/files?path=/workspace/project/b.txt"
```

错误沿用 Files 的 JSON envelope：`error.code/message/retryable`。主要映射为 400 INVALID_FILE_REQUEST、403 FILE_FORBIDDEN、404 FILE_NOT_FOUND、409 FILE_EXISTS / SANDBOX_STATE_CONFLICT、413 FILE_TOO_LARGE、501 FILE_OPERATION_UNSUPPORTED、503 FILE_BACKEND_UNAVAILABLE。Control 的权限与生命周期拒绝使用现有 Control 错误码。

## 能力与验证范围

CHV 与 Lima 共用 Guest Files backend 和 Node Runtime RPC，本地 Node HTTP 同步接线；本轮真实验收覆盖远程 CHV。Lima 与 Electron 界面实际操作未新增实机验收；未改变工作台文件面板。

OpenAPI 是公开契约源；Go/TypeScript 客户端、Node Proto 生成代码及文档页面由现有生成器更新。Node Proto 接收 SandboxTarget，Guest Proto 接收完整 RuntimeIdentity；不将 Guest 身份字段暴露给公开 HTTP 客户端。

部署本功能需要更新 API/Node，并使用包含 Guest 删除/移动安全修复的新模板。不会自动改写运行中 Guest。真实验收及重跑命令见 [Files CHV 记录](current-status.md)。
