import { randomUUID } from "../../lib/utils";
import type { InjectionRule } from "../../api/public.generated";

export type SupportedAgent = "codex" | "claude" | "opencode" | "cursor" | "grok";

export interface AgentConfig {
  id: string;
  name: string;
  agent: SupportedAgent;
  connectionId: string;
  modelNames: string[];
  defaultModel: string;
  filePath: string;
  content: string;
  updatedAt: string;
}

export interface AgentConfigDraft {
  name: string;
  agent: SupportedAgent;
  connectionId: string;
  modelNames: string[];
  defaultModel: string;
  filePath: string;
  content: string;
}

export const AGENT_LABELS: Record<SupportedAgent, string> = {
  codex: "Codex",
  claude: "Claude Code",
  opencode: "OpenCode",
  cursor: "Cursor",
  grok: "Grok",
};

export const AGENT_DEFAULT_PATHS: Record<SupportedAgent, string> = {
  codex: "~/.codex/config.toml",
  claude: "~/.claude/settings.json",
  opencode: "~/.config/opencode/opencode.json",
  cursor: "~/.cursor/settings.json",
  grok: "~/.config/grok/config.json",
};

export const AGENT_CONFIGS_STORAGE_KEY = "isodo.agent-configs.v1";
export const SECRET_PLACEHOLDER = "${ISODO_SECRET}";

export function newAgentConfigId(): string {
  return `agent-config:${randomUUID()}`;
}

export function replaceSecretsWithPlaceholder(content: string, secrets: readonly string[]): string {
  return secrets
    .filter((secret) => secret.trim().length > 0)
    .sort((a, b) => b.length - a.length)
    .reduce((result, secret) => result.split(secret).join(SECRET_PLACEHOLDER), content);
}

export function detectAndRedactConfig(
  content: string,
  connection: Pick<InjectionRule, "secret_hint"> & { secret?: string },
): { content: string; detected: boolean } {
  const secret = connection.secret?.trim() ?? "";
  if (secret) {
    const redacted = replaceSecretsWithPlaceholder(content, [secret]);
    return { content: redacted, detected: redacted !== content };
  }
  // Common pasted bearer/API key fields are redacted before the value can be
  // persisted in the configuration record. The actual value belongs to the
  // referenced service connection.
  const redacted = content
    .replace(/(Authorization\s*[:=]\s*["']?Bearer\s+)[^"'\s,}]+/gi, `$1${SECRET_PLACEHOLDER}`)
    .replace(
      /(["']?(?:api[_-]?key|token|x-api-key)["']?\s*[:=]\s*["'])[^"']+(["'])/gi,
      `$1${SECRET_PLACEHOLDER}$2`,
    );
  return { content: redacted, detected: redacted !== content };
}

export function createAgentConfig(draft: AgentConfigDraft): AgentConfig {
  const now = new Date().toISOString();
  return { ...draft, id: newAgentConfigId(), updatedAt: now };
}

export function readLegacyAgentConfigs(storage: Storage = localStorage): AgentConfig[] {
  try {
    const parsed: unknown = JSON.parse(storage.getItem(AGENT_CONFIGS_STORAGE_KEY) ?? "[]");
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((item): item is AgentConfig => {
      if (!item || typeof item !== "object") return false;
      const value = item as Partial<AgentConfig>;
      return (
        typeof value.id === "string" &&
        typeof value.name === "string" &&
        typeof value.agent === "string" &&
        typeof value.connectionId === "string" &&
        Array.isArray(value.modelNames) &&
        typeof value.filePath === "string" &&
        typeof value.content === "string"
      );
    });
  } catch {
    return [];
  }
}

export function modelsFromResponse(value: unknown): string[] {
  if (!value || typeof value !== "object") return [];
  const data = value as { data?: unknown; models?: unknown };
  const list = Array.isArray(data.data) ? data.data : Array.isArray(data.models) ? data.models : [];
  return list.flatMap((item) => {
    if (typeof item === "string") return [item];
    if (item && typeof item === "object" && typeof (item as { id?: unknown }).id === "string") {
      return [(item as { id: string }).id];
    }
    return [];
  });
}

export function modelEndpoint(baseUrl: string): string {
  return `${baseUrl.replace(/\/$/, "")}/models`;
}
