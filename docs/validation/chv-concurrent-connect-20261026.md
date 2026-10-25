# CHV 并发连接失败复现与修复

## 已确认的问题

用户报告工作台连接间歇失败，前端只显示“操作失败，请稍后重试”。不录制屏幕也会发生；没有证据把故障归因于 Screen Studio。

在真实演示 Node 上，新建独立诊断沙箱，使用同一份 `base` 快照及 ALLOW egress，串行完成 3 次创建、6 次 app-link 请求，均成功。随后在另一个诊断沙箱上并发调用两次 `CreateSandboxAppLink`：

| 请求 | 结果 |
| --- | --- |
| 并发请求 1 | 成功，8299 ms |
| 并发请求 2 | Unavailable / Sandbox egress proxy could not be configured，364 ms |
| 后续单独重试 | 成功，2420 ms |

诊断沙箱均由本次测试单独创建，结束后 stop/delete 清理，没有操作用户 App 或现有实例。复现脚本和输出留在本机 worktree 的 `.tools/repro-cloud-connect*.{py,log}`；不保存 pairing URL/token。

## 代码原因

`prepareCHVEgress` 在锁内查看 `s.egress` 后释放锁，再创建 `<vsock>_1080` Unix socket、配置 Guest、重启 T3，最后才登记 `s.egress`。两个请求同时进入时，都可能看到“尚未初始化”；第二次创建同一个 socket 失败。这个错误在 Node 被包装为通用 Unavailable，再被前端转换成“操作失败”。

已确认此并发缺陷能造成相同症状；用户最初失败请求的具体前端触发时序没有保留，不能据此认定所有历史连接失败均由这一处导致。

## 修复与本地验证

- 以 sandbox ID、ownership generation、boot ID、Guest epoch 为 key，合并同一启动实例的并发初始化；新启动不会复用旧实例的初始化结果。
- 初始化受 Node 生命周期和 90 秒上限控制；单个调用者取消请求，不会取消其他调用者共享的初始化。
- 已取消的请求不会启动新初始化；失败不缓存，后续请求可以重试。
- 回归测试覆盖并发合并、新 boot 隔离、失败后重试、Node 退出取消，以及已取消请求不启动初始化。
- `go test ./...`、相关 `go test -race`、Linux amd64 Node 构建、`git diff --check` 通过。

## 隔离 Node 真实对照

修复版 Linux amd64 Node 在 `jfcs1024` 独立端口 19192、独立 state 目录运行，复用同一只读快照工件，在独立诊断实例上重复相同并发用例：

| 请求 | 修复后结果 |
| --- | --- |
| 并发请求 1 | 成功，8506 ms |
| 并发请求 2 | 成功，8634 ms |
| 后续单独重试 | 成功，2146 ms |

回归测试额外运行 10 次 race 检查通过。对照完成后，诊断沙箱全部 stop/delete，临时 Node 和本机专用 SSH 转发已停止。没有操作用户 App、现有沙箱或正在服务的 Node。

正式演示 Node 尚未替换：用户此前要求停止控制和服务重启，因此对当前运行会话的部署暂停请求已发出。修复代码位于本 PR 分支，正式演示 Node 的替换仍需按部署窗口执行。
