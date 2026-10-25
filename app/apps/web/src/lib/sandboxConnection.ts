export type SandboxConnectionStage = "bundle" | "server" | "pairing" | "codex";

const STAGE_LABELS: Readonly<Record<SandboxConnectionStage, string>> = {
  bundle: "准备当前 T3 Server",
  server: "启动沙箱内 T3 Server",
  pairing: "生成一次性配对码",
  codex: "连接 Codex 工作区",
};

export function sandboxConnectionStageLabel(stage: SandboxConnectionStage): string {
  return STAGE_LABELS[stage];
}

export function formatSandboxConnectionError(error: unknown): string {
  if (error instanceof Error && error.message.trim()) return error.message;
  if (typeof error === "string" && error.trim()) return error;
  return "沙箱连接失败，请重试。";
}
