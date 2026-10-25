# 正常退出时自动暂停本地沙箱

实现位置：app/apps/desktop/src/shutdown.ts、main.ts。Electron before-quit 暂缓退出，通过现有本地 HTTP API 暂停桌面 Node 所有权索引里的活动沙箱，并等待实际 stopped/paused。暂停期间新出现的已管理沙箱也纳入处理。停止失败或超时提供重试、取消、明确强制退出选项。Node 在 will-quit 才关闭，避免窗口取消关闭后失去运行时。

验证：9 项桌面流程单元测试通过，覆盖多沙箱等待、已停止沙箱跳过、实例消失不误判成功、停止失败、重复退出合并、取消后再次退出、明确强制退出、窗口取消关闭后重新检查、等待期间出现新沙箱。桌面 TypeScript 类型检查与构建通过。

未执行真实桌面退出测试，也没有暂停用户当前正在运行的 fenno-test。用户明确选择保留当前运行、稍后重启；当前进程仍使用旧版本，新功能从下次启动生效。本次没有创建额外测试 VM。

补充：桌面 main.ts 的定向 lint 仍报告既有 Node namespace 命名、process.arch/platform 和未使用的旧 disable 指令；本轮未扩大范围处理这些历史问题。
