# CHV 原生快照恢复验证（2026-10-26）

## 环境与结论

`jfcs1024` 已从宿主 `5.15.0-46-generic` 升级到 Ubuntu HWE `6.8.0-138-generic`，继续使用原来的 CHV v54.0 二进制。SSH、Docker、containerd、kubelet、etcd、Vault 均 active，Kubernetes 节点 Ready；两个手动启动的 Isodo Node 已恢复监听，开发机的 19191/18333 SSH 转发已重建。旧 5.15 内核及 initramfs 保留。

新构建的 runtime snapshot 已通过真实 CHV 双克隆及 Node 工作台探针。恢复过程中直接链接原始 `vm-state.json`，没有时钟状态兼容改写。探针完成后该文件 SHA-256 仍与 manifest 一致，保存的 KVM clock flags 为 14。

这验证了快照构建与 Node runtime 路径；**尚未向 S3 发布这个新 digest，未切换 Control 的默认 catalog，未在演示 Node 部署本轮预热代码，也未完成真实浏览器配对。** 不应据此宣称 App 默认创建已达到这些耗时。

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

首个用户请求若仍执行全量校验，仍会额外等待约 42 秒。此次代码增加 `ISODO_TEMPLATE_PRELOAD_DIGESTS`，在 Node 监听前下载/校验工件，并复用进程内验证凭据；这个部署步骤尚未执行。

## 证据与本地检查

原始探针输出位于远端 `/root/zkp/isodo-api-unify/warm-20261026/`：`build-native-kvmclock.json`、`probe-artifact-native-kvmclock.{json,log}`、`probe-node-native-kvmclock.{json,log}`。Node 探针 JSON 含临时 app URL/token，保留在服务器，不复制进仓库。

本地 `go test ./...`、Linux amd64 CLI 构建、`git diff --check` 通过。宿主版本门禁在本地单测和 Linux 编译中验证；此次远端探针二进制在加入版本门禁前编译，其余恢复及 Guest 时钟逻辑与本次源码一致。
