# 本地 Lima 实例清理

用户明确要求清理全部本地沙箱后重新创建。

已先停止，再使用 limactl 删除以下实例：

- agent-poc-main
- agent-poc-clone
- agent-poc-nested-kvm
- isodo-sbx-f5ab6fb4aebaa9388036f3ff327231b5
- isodo-sbx-base-a84841f33ca1（旧缓存）

保留停止状态的 isodo-sbx-base-ab894192fd84：根据当前模板标识、provisioner v2 与当前 Guest Agent 二进制计算的 BaseInstanceName 与它匹配，用于加速重新创建。

验证：limactl list 仅剩上述当前 base；本地 API /sandboxes 返回空列表。对应控制面记录已处于软删除状态，本次未重复更新；删除了该机器已删除沙箱的 2 条 sandbox credential documents。账号全局 Agent 配置与 API 密钥没有删除。Node journal 的历史操作记录保留，未直接修改运行中 journal 文件。

退出行为只读核对：Electron before-quit 终止 Node 子进程，LimaServer.Close 只停止后台 worker，没有执行 VM stop。macOS 关闭窗口不等于退出应用。本轮未更改退出自动暂停策略。

侧栏卡片改为自动高度，增加名称、状态、规格之间的间距；规格分段换行，不再挤在固定高度行内。10 项侧栏测试通过；未做桌面点击验证。不以 du 数值声称实际释放空间，因为 Lima 磁盘可能使用 APFS 共享块。
