# 云端工作台 503：T3 代理环境重载卡住

## 现象与证据

2026-11-03，云端沙箱 `t`（`8c5b7837-adec-4192-a641-173e89d5ead5`）显示「无法连接到沙箱，操作失败，请稍后重试」。Control 数据库 desired/observed 均为 running；Node 记录创建耗时 6277ms，Guest Ready 1852ms，API、Node、Nginx 健康。

通过现有 mTLS 直接调用 Node 的 CreateSandboxAppLink，返回 `Unavailable: Sandbox egress proxy could not be configured`。Guest T3 在 127.0.0.1:38080 监听，PID=167、UID=0；Node 需要重启 T3 以加载刚写入的 proxy.env。原重载脚本发送 SIGTERM 后等待 200×50ms，T3 仍为 sleeping，PID 未退出，最终报错。SIGTERM 已被 T3 捕获；本轮未定位 T3 内部哪个清理任务阻塞，不据此断言 root 用户本身导致死锁。

## 当前恢复

确认 Guest identity、PID 文件以及 `/proc/<pid>/cmdline` 指向 Node + bin.mjs serve 后，仅对卡住的 T3 PID 执行 SIGKILL，BusyBox supervisor 自动拉起新 T3（PID=275），加载已存在的代理环境。未停止 Node、VM，未改写工作盘或用户文件。

- Node app-link：HTTP ready 5137ms、pairing 2121ms，调用成功；配对凭证未输出。
- Desktop 刷新后：2026-11-03 14:49:20 HTTP ready 18ms、14:49:22 pairing 成功。
- 真实 Desktop 已进入沙箱 t 的新任务编辑页，模型选择器、输入框、工作台导航可见；未发送模型请求。

## 防回归代码（尚未部署）

`internal/node/orchestrator/t3_restart.py` 替换仅等待 SIGTERM 的内联逻辑：保留固定 PID 文件与可执行文件/CLI 标记校验，使用 Linux pidfd 绑定被验证的原进程，优雅退出超时后只对同一进程升级 SIGKILL；不会因数值 PID 复用而误杀新进程。PID 1 与不匹配进程均拒绝。脚本只在 Guest 内由 Node 固定参数调用。

Linux 验证主机上 4 个真实子进程回归通过：正常退出、忽略 SIGTERM 后升级、拒绝不匹配进程、拒绝 PID 1。此结果是进程级回归，不是新代码的 CHV 端到端部署验收。

当前服务靠 T3 进程恢复已可用；本记录不声明兜底代码已经部署。部署该 Node 修复会中断其管理的活动 VM，必须在维护窗口或取得具体中断授权后进行，不能为部署补丁再次打断已恢复的用户会话。

后续发布必须额外验证“开启出站代理的新 Sandbox → Control/客户端 app-link → 网关认证 → 真实工作台”，不能只用 Commands 的 root/dpkg 成功或直接模板 T3 pairing 代替完整链路。
