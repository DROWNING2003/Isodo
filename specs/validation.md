# Runtime 技术验证记录

日期：2026-10-24（Asia/Shanghai）。本次只执行 Gate 1：真实 CHV 启动、Vsock、最小 Guest 执行、停机清理。不是完整 Runtime 或安全验收。

## 环境与范围

- 用户授权：`ssh jfcs1024`，工作区 `/root/zkp`。
- 测试目录：`/root/zkp/isodo-validation/`；未修改现有 `nomad-jobs/`。
- 宿主：Ubuntu 22.04.1，x86_64，Linux 5.15.0-46-generic，cgroup v2，`/dev/kvm` 可用。
- CHV：官方 release `v54.0` 的 `cloud-hypervisor-static`，仅下载到测试目录，没有全局安装。
- Guest：复制宿主 kernel 与匹配 vsock 模块，使用 BusyBox + 自制 initramfs + 静态 C 探针。不是正式 Golden Image，没有持久磁盘。
- 资源：2 vCPU、512 MiB Guest 内存；独立 systemd unit `isodo-validation-chv`，MemoryMax=768M、CPUQuota=200%、最长运行 900 秒。
- 无 Guest 网卡、无宿主共享目录；本次 VMM 以 root 运行，仅用于功能探针，未验证生产非特权隔离。

来源：[官方 release](https://github.com/cloud-hypervisor/cloud-hypervisor/releases/tag/v54.0)、[Vsock 协议](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/7913750ed89f25192d920d2a1cef8a8c80f638f0/docs/vsock.md)。本地工件 SHA-256 见 [artifacts.sha256](../artifacts/validation/chv-vsock/artifacts.sha256)，这是下载/生成后的内容指纹，不是额外验证发行签名。

## 实测结果

| 项目 | 结果 | 证据 |
| --- | --- | --- |
| 创建真实 KVM VM | 通过 | CHV PID 30162，持有 /dev/kvm、kvm-vm、两个 kvm-vcpu FD |
| Guest 启动 | 通过 | 串口 `ISODO_PROBE_READY`，CHV vm.info=Running |
| 宿主到 Guest Vsock | 通过 | 连接独立 UDS，发送 CONNECT 1024，收到 OK 响应 |
| Guest 固定命令执行 | 通过（有限） | 两次新连接均收到 stdout/stderr 标记、内核版本和真实 waitpid 退出码 7 |
| 跨 SSH 客户端连接保持 VM | 通过（有限） | 后续独立 SSH 请求仍连接到同一 systemd 托管 VM；不等于 Node/Nomad 重启验证 |
| CHV vm.shutdown | 通过 | API 返回后 vm.info.state=Created；这表示 VM 已 shutdown，VMM 进程仍需退出 |
| 临时资源清理 | 通过 | systemd unit inactive，测试 socket 已清理，日志和工件保留 |

Guest 和 Host 使用相同 `uname -r`，但真实 KVM VM 已由宿主 FD、CHV API、Guest 通道共同确认；不得将内核版本不同当作 VM 必要条件。

## 尚未验证

- stdout/stderr 独立帧、任意 argv、超时/取消、输出背压：探针将两个流合并，只执行固定命令。
- PTY、resize、Ctrl+C、正式 Guest gRPC/HTTP2 协议。
- rootfs/workspace 持久化、Stop/Start 后文件保留。
- Isodo API/Node、journal、幂等、reconcile 和 Node 重启接管。
- Nomad allocation 重启/滚动发布隔离：本次没有提交测试 Job，也没有重启现有 Nomad 服务。
- 非 root VMM、多租户隔离、受控出口、Secretless MCP、快照恢复。
- 启动 P50/P95：串口内核时间不是 API 到 Guest Ready 的端到端性能结果。

## 可复现文件与证据

- [Guest 探针](../tests/validation/vsock-probe.c)
- [准备脚本](../tests/validation/prepare-probe.sh)：在已具备匹配模块、gcc、静态 BusyBox 的该节点上使用。
- [Vsock 断言脚本](../tests/validation/check-probe.py)
- [Guest 串口日志](../artifacts/validation/chv-vsock/logs/chv.log)
- [两次调用结果](../artifacts/validation/chv-vsock/logs/probe.jsonl)
- [运行时 VM 配置](../artifacts/validation/chv-vsock/logs/vm-info.json)
- [KVM FD](../artifacts/validation/chv-vsock/logs/kvm-evidence.txt)
- [清理结果](../artifacts/validation/chv-vsock/logs/cleanup.txt)

启动命令（仅本次测试参数，已有同名 unit 时先检查其归属；不要覆盖正在运行的测试）：

```sh
systemd-run --unit=isodo-validation-chv \
  --property=MemoryMax=768M --property=CPUQuota=200% --property=RuntimeMaxSec=900 \
  --property=StandardOutput=append:/root/zkp/isodo-validation/logs/chv.log \
  --property=StandardError=append:/root/zkp/isodo-validation/logs/chv.log \
  /root/zkp/isodo-validation/bin/cloud-hypervisor \
  --api-socket /root/zkp/isodo-validation/api.sock \
  --kernel /root/zkp/isodo-validation/kernel \
  --initramfs /root/zkp/isodo-validation/initramfs.cpio \
  --cmdline 'console=ttyS0 rdinit=/init panic=1' \
  --cpus boot=2 --memory size=512M --serial tty --console off \
  --vsock cid=3,socket=/root/zkp/isodo-validation/vsock.sock
python3 /root/zkp/isodo-validation/check-probe.py
```

## 对 spec 的反馈

可以按 Linux CHV + Vsock 路线继续细化 Runtime 契约，CHV v54.0 是该宿主的初始功能验证候选版本，不是生产认证。平台 Stop 应明确确认 VMM unit 退出，不能把 CHV `vm.shutdown` 后的 `Created` 状态误认为整个 VMM 进程已消失。

下一步先固定 Guest Exec/PTY 流式协议并验证真实取消/断连行为；独立测试 Node Supervisor 接管和 Nomad 更新；最后接入一种真实模型/MCP 服务进行授权撤销验证。未完成这些项目之前，不宣称基础规格全部验证通过。
