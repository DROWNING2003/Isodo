import type { InjectionRule } from "../../api/public.generated";
import { AGENT_LABELS, type AgentConfig } from "./agentConfigs";

export interface CredentialSelection {
  agentIds: string[];
  credentialIds: string[];
}
export const EMPTY_CREDENTIAL_SELECTION: CredentialSelection = { agentIds: [], credentialIds: [] };

export function CredentialSelectionFields({
  configs,
  credentials,
  value,
  onChange,
  disabled = false,
}: {
  configs: readonly AgentConfig[];
  credentials: readonly InjectionRule[];
  value: CredentialSelection;
  onChange: (value: CredentialSelection) => void;
  disabled?: boolean;
}) {
  const toggle = (key: keyof CredentialSelection, id: string, checked: boolean) => {
    const next = {
      ...value,
      [key]: checked ? [...new Set([...value[key], id])] : value[key].filter((item) => item !== id),
    };
    if (key === "agentIds" && checked)
      next.credentialIds = next.credentialIds.filter(
        (credentialId) => credentialId !== configs.find((config) => config.id === id)?.connectionId,
      );
    onChange(next);
  };
  const standalone = credentials.filter(
    (item) =>
      value.credentialIds.includes(item.id) ||
      !configs.some((config) => config.connectionId === item.id),
  );
  return (
    <div className="grid gap-4">
      <div className="grid gap-2">
        <h3 className="text-sm font-medium">Agent 配置</h3>
        {configs.map((config) => (
          <label
            key={config.id}
            className="flex items-start gap-3 rounded-lg border border-border p-3"
          >
            <input
              className="mt-1"
              type="checkbox"
              disabled={disabled || !credentials.some((item) => item.id === config.connectionId)}
              checked={value.agentIds.includes(config.id)}
              onChange={(e) => toggle("agentIds", config.id, e.target.checked)}
            />
            <span className="min-w-0 text-sm">
              <span className="font-medium">{config.name}</span>
              <span className="mt-1 block break-all text-xs text-muted-foreground">
                {AGENT_LABELS[config.agent]} · {config.defaultModel || "未指定模型"}
              </span>
            </span>
          </label>
        ))}
        {!configs.length ? (
          <p className="text-sm text-muted-foreground">凭证库中还没有 Agent 配置。</p>
        ) : null}
      </div>
      {(["http", "mcp"] as const).map((kind) => (
        <div key={kind} className="grid gap-2">
          <h3 className="text-sm font-medium">{kind === "mcp" ? "MCP 凭证" : "HTTP 凭证"}</h3>
          {kind === "mcp" ? (
            <p className="text-xs text-muted-foreground">
              授权后仍需在 Agent 中配置 MCP URL，使用占位符作为认证值。
            </p>
          ) : null}
          {standalone
            .filter((credential) => (credential.type === "mcp") === (kind === "mcp"))
            .map((credential) => (
              <label
                key={credential.id}
                className="flex items-start gap-3 rounded-lg border border-border p-3"
              >
                <input
                  className="mt-1"
                  type="checkbox"
                  disabled={disabled}
                  checked={value.credentialIds.includes(credential.id)}
                  onChange={(e) => toggle("credentialIds", credential.id, e.target.checked)}
                />
                <span className="min-w-0 text-sm">
                  <span className="font-medium">{credential.name}</span>
                  <span className="mt-1 block break-all text-xs text-muted-foreground">
                    {credential.base_url}
                  </span>
                </span>
              </label>
            ))}
          {!standalone.some((credential) => (credential.type === "mcp") === (kind === "mcp")) ? (
            <p className="text-sm text-muted-foreground">还没有可选凭证。</p>
          ) : null}
        </div>
      ))}
    </div>
  );
}
