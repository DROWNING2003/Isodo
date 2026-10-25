# CHV 原生快照恢复验证（2026-10-26）

## 环境与结论

`jfcs1024` 已从宿主 `5.15.0-46-generic` 升级到 Ubuntu HWE `6.8.0-138-generic`，继续使用原来的 CHV v54.0 二进制。SSH、Docker、containerd、kubelet、etcd、Vault 均 active，Kubernetes 节点 Ready；两个手动启动的 Isodo Node 已恢复监听，开发机的 19191/18333 SSH 转发已重建。旧 5.15 内核及 initramfs 保留。

新构建的 runtime snapshot 已通过真实 CHV 双克隆及 Node 工作台探针。恢复过程中直接链接原始 `vm-state.json`，没有时钟状态兼容改写。探针完成后该文件 SHA-256 仍与 manifest 一致，保存的 KVM clock flags 为 14。

上述探针完成时，尚未发布新 digest、切换默认 catalog、部署 Node 预热或验证真实浏览器配对。下述探针耗时应与后续 App 部署验收区分；部署后的证据见文末补充。

## 两个独立的时钟问题

1. 旧宿主 5.15 上，CHV v54 原生恢复报 `KVM_SET_CLOCK EINVAL`。与上游 [#9101](https://github.com/cloud-hypervisor/cloud-hypervisor/issues/9101) 一致，上游选择最低宿主内核 6.8。升级 Guest 内核无法改变宿主 KVM 的兼容性。
2. 升级宿主后，原生恢复成功，但默认 Guest `clocksource=tsc` 的时间仍落后约 143939 ms，触发探针失败。构建源 Guest 改为 `clocksource=kvm-clock` 后，原生恢复的时间差检查通过（阈值 5 秒）。生产代码不保留实验性时钟 flags 清理或手动补偿。

内核升级改变了 `/proc/cpuinfo` 暴露的特征，因此旧快照 CPU 签名不匹配。此次从同一冷模板重建快照，没有绕过 CPU 或 VMM 摘要校验。构建/恢复入口新增宿主内核 ≥ 6.8 检查。

## 工件

- 远端目录：`/root/zkp/isodo-api-unify/warm-20261026/warm-native-kvmclock-6.8`
- manifest digest：`9eca7ffb5fc8f124c734af4d72feba7d1c0abce9cf51a1c76d9f5096713a7920`
- 资源：2 vCPU、2 GiB 内存；每个实例使用私有 QCOW2 磁盘和内存 CoW。
- 原始 VM state digest：`fa5be8715e0314ca9d1a72c6f50b09670967d06dfe63cb43b2305795f11d3de2`

## 实测

均为工件已校验后的单次探针样本，不是分位数或生产承诺。工件全量校验单独计时，下载耗时不在本表中。

| 检查 | 结果 |
| --- | --- |
| `probe-artifact` 全量校验 | 42448 ms |
| 克隆 1 / 2 到 Guest Ready | 225 / 219 ms |
| 克隆 1 / 2 到首条 exec 完成 | 342 / 322 ms |
| Guest 时钟差 ≤ 5 秒 | 两个克隆通过 |
| 新 Guest epoch / 随机数 | 通过 |
| 磁盘 marker 隔离 | 通过 |
| `probe-node-t3` 全量校验 | 42616 ms |
| Node 创建到 Guest Ready | 403 ms |
| Node 创建到工作台链接可用 | 8162 ms |
| stop → start 到工作台链接可用 | 7091 ms |
| 创建/重启后的 T3 HTTP 与 HTML | 两次 200，HTML 检查通过 |
| pairing token 签发 | 通过；未验证浏览器实际兑换 |
| 重启后 boot ID / Guest epoch | 均更新 |
| 停止和删除 | 通过 |

首个用户请求若仍执行全量校验，仍会额外等待约 42 秒。此次代码增加 `ISODO_TEMPLATE_PRELOAD_DIGESTS`，在 Node 监听前下载/校验工件，并复用进程内验证凭据；后续部署记录见文末。

## 证据与本地检查

原始探针输出位于远端 `/root/zkp/isodo-api-unify/warm-20261026/`：`build-native-kvmclock.json`、`probe-artifact-native-kvmclock.{json,log}`、`probe-node-native-kvmclock.{json,log}`。Node 探针 JSON 含临时 app URL/token，保留在服务器，不复制进仓库。

本地 `go test ./...`、Linux amd64 CLI 构建、`git diff --check` 通过。宿主版本门禁在本地单测和 Linux 编译中验证；此次远端探针二进制在加入版本门禁前编译，其余恢复及 Guest 时钟逻辑与本次源码一致。

## 后续部署与桌面验收（北京时间 02:06–02:11）

新 digest 已上传至隔离 SeaweedFS 桶 `isodo-unify-demo`。Node 使用只读凭证下载/校验，首次耗时 143303 ms；同进程二次读取为 0 ms，复用验证凭据。演示 Node 升级前检查 journal：活动沙箱和未完成操作均为 0。旧测试沙箱已被用户删除，没有恢复这些已删除记录。

新版演示 Node 保持 `isodo-jfcs1024-unify-demo`、端口 19191 和原 state 目录。启动预热耗时 41825 ms，完成后 mTLS `GetNodeStatus` 通过。另一个端口 9190 的 Node 未变更。

系统 catalog 新增 `base`，build ID 为 `a7a07645-ccc4-4811-aef3-be0704332d46`，对应本文新 digest；旧 `base-unify-demo-20261025` 保留。旧模板不含 runtime snapshot，用户此前创建的 `fenno` 在 Node 阶段耗时 41.745 秒。

用户随后从真实 Electron App 创建 `fenno`（`1202a915-523a-4d2a-83d4-e5f3c1e54850`），数据库确认已固定到新 build/digest，状态 running。Node 日志：

| 阶段 | 耗时 |
| --- | --- |
| Node 排队 | 3 ms |
| 工件获取 / 验证凭据检查 | 0 / 0 ms |
| 私有磁盘 CoW 准备 | 23 ms |
| CHV restore 调用 | 125 ms |
| Guest template Ready 等待 | 22 ms |
| Node CREATE 总耗时 | 243 ms |
| 工作台 HTTP Ready 等待 | 5455 ms |
| pairing credential 签发 | 2444 ms |

243 ms 是 Node CREATE 耗时，**不包含** Control 轮询、工作台 HTTP、pairing 和前端模块加载，不能描述为用户进入工作台的总耗时。没有采集此次点击到工作台的精确前端计时。

真实桌面观察已从“正在连接沙箱”进入任务编辑器，显示 `fenno` 云端运行中及 `gpt-6.1-sol` 模型选择器；配对与工作台进入通过。未发送 Agent 推理请求，因此尚未验证模型服务调用或生成结果。

此次同时修复了验收环境的三处启动不一致：

- 旧 main 的 Electron preload 没有 `sandboxRequest`，与新前端混用会导致列表失败；现已从同一 worktree 构建前端、Electron 和本地 Node。
- App 原先打开 `http://127.0.0.1:5734`，API 只接受 `http://app.isodo.localhost:5734`，写请求触发 INVALID_CSRF；现统一到后者，保留 CSRF 校验。
- 前端缺少 `VITE_ISODO_PREVIEW_DOMAIN`，拒绝合法预览链接；两个 checkout 的忽略文件 `app/apps/web/.env.local` 已设置为 `isodo.localhost`。

启动入口：本 worktree 的 `.tools/start-acceptance-web.sh`、`.tools/start-acceptance-desktop.sh`；远端 `/root/zkp/isodo-api-unify/warm-20261026/start-warm-node.py` 保留预热设置。启动脚本和私有环境留在各主机，凭证未入库。远端 `node-warm.log`、`publish-native.json`、`fetch-native.json` 保留部署与实测证据。
