# Agent 工作台集成：决策记录

状态：延后；2026-10-24。用户已明确先完成沙箱服务，桌面端规格与 T3 工作台集成后续再讨论。本文保留参考，不阻塞沙箱服务实现。

## 已确认

用户指定 [DROWNING2003/fork.t3code](https://github.com/DROWNING2003/fork.t3code) 为 Q1（首版真实操作入口）的参考项目。产品仍以 Sandbox 为主体；此前原型定义的 Task 包含对话、执行与产出。

本地参考：`/Users/drowning/Desktop/qiniu/fork.t3code`，读取版本 `0a26a291a234e4bcfefc7ad9ce6ffd872052106c`。未修改参考仓库。

## 已读取的上游依据

[架构文档](https://github.com/DROWNING2003/fork.t3code/blob/0a26a291a234e4bcfefc7ad9ce6ffd872052106c/docs/internals/overview.md) 描述：执行、Provider 进程、终端与文件属于工作环境服务端；客户端通过认证 RPC 控制；Provider 差异经 adapter 归一化；命令意图持久化与实际执行完成分开。

以上是文档依据，不能据此认定 Isodo 已有对应实现，也不代表必须复制 T3 的事件存储或整体技术栈。

## 待确认的集成边界

1. 参考交互及协议思想、自行实现，还是实际复用 T3 Web/Server/Provider adapter 代码？
2. 首个接入的 Agent 以及必须支持的对话、审批、取消、恢复事件。
3. 浏览器断连与任务执行的生命周期关系。
4. Provider 会话与 Sandbox/Task 的标识、存储和恢复关系。
5. 普通公网访问与凭证代理授权的关系。

现有 Runtime spec 的“默认先终端闭环”只是先前建议，不是用户已接受的范围裁剪。Q1 需要以本轮 T3 参考范围确认结果更新；暂不把 PTY 当作结构化 Agent 会话协议。
