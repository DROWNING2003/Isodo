import { describe, expect, it } from "vite-plus/test";

import { formatSandboxConnectionError, sandboxConnectionStageLabel } from "./sandboxConnection";

describe("sandbox connection feedback", () => {
  it("labels each stage of the sandbox-to-Codex connection", () => {
    expect(sandboxConnectionStageLabel("bundle")).toBe("准备当前 T3 Server");
    expect(sandboxConnectionStageLabel("server")).toBe("启动沙箱内 T3 Server");
    expect(sandboxConnectionStageLabel("pairing")).toBe("生成一次性配对码");
    expect(sandboxConnectionStageLabel("codex")).toBe("连接 Codex 工作区");
  });

  it("preserves actionable errors and has a fallback for unknown failures", () => {
    expect(formatSandboxConnectionError(new Error("503 envd unavailable"))).toBe(
      "503 envd unavailable",
    );
    expect(formatSandboxConnectionError("bundle missing")).toBe("bundle missing");
    expect(formatSandboxConnectionError({})).toBe("沙箱连接失败，请重试。");
  });
});
