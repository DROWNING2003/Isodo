import { useRef } from "react";
import type { CreateInjectionRule, InjectionRule } from "../../api/public.generated";
import type { AgentConfig } from "./agentConfigs";
import type { LocalInjectionRule } from "./injectionRules";
import { AgentConfigDialog } from "./AgentConfigDialog";
import { randomUUID } from "../../lib/utils";

export interface TemporaryAgent {
  config: AgentConfig;
  connection: InjectionRule;
  rule: LocalInjectionRule;
}
export function temporaryAgentConnection(
  input: CreateInjectionRule,
  id = `temporary-agent:${randomUUID()}`,
): { connection: InjectionRule; rule: LocalInjectionRule } {
  const now = new Date().toISOString();
  const header = input.type === "anthropic" ? "x-api-key" : "Authorization";
  const placeholder = `isodo-credential-${id}`;
  return {
    connection: {
      id,
      name: input.name,
      type: input.type,
      base_url: input.base_url ?? "",
      secret_set: true,
      secret_hint: `••••${input.secret.slice(-4)}`,
      created_at: now,
      updated_at: now,
    },
    rule: {
      key: id,
      name: input.name,
      kind: "one-off",
      spec: {
        type: input.type,
        base_url: input.base_url ?? "",
        if_headers: {
          [header]: header === "Authorization" ? `Bearer ${placeholder}` : placeholder,
        },
      },
      secret: { api_key: input.secret },
      secret_hint: `••••${input.secret.slice(-4)}`,
    },
  };
}
export function TemporaryAgentDialog({
  initial,
  onClose,
  onSave,
}: {
  initial?: TemporaryAgent;
  onClose: () => void;
  onSave: (value: TemporaryAgent) => Promise<void>;
}) {
  const draft = useRef(
    initial ? { connection: initial.connection, rule: initial.rule } : undefined,
  );
  return (
    <AgentConfigDialog
      open
      temporary
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      connections={initial ? [initial.connection] : []}
      {...(initial ? { initial: initial.config } : {})}
      onCreateConnection={async (input) => {
        draft.current = temporaryAgentConnection(input);
        return draft.current.connection;
      }}
      onUpdateConnection={async (_id, patch) => {
        const previous = draft.current;
        if (!previous) throw new Error("请先填写连接信息。");
        const secret = patch.secret || previous.rule.secret?.api_key;
        if (!secret) throw new Error("请重新填写 API Key。");
        draft.current = temporaryAgentConnection(
          {
            name: patch.name ?? previous.connection.name,
            type: previous.connection.type,
            base_url: patch.base_url ?? previous.connection.base_url ?? "",
            secret,
          },
          previous.connection.id,
        );
        return draft.current.connection;
      }}
      resolveConnectionMaterial={async () => draft.current?.rule.secret}
      onSave={async (config) => {
        if (!draft.current) throw new Error("请填写连接信息。");
        await onSave({ config, ...draft.current });
      }}
    />
  );
}
