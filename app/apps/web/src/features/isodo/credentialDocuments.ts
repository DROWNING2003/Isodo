import type { TemporaryAgent } from "./TemporaryAgentDialog";
import type { LocalInjectionRule } from "./injectionRules";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { isodoFetch } from "../../api/transport";
import { getAuthGeneration, useAuth } from "./auth";
import {
  AGENT_CONFIGS_STORAGE_KEY,
  readLegacyAgentConfigs,
  type AgentConfig,
} from "./agentConfigs";
import type { CredentialSelection } from "./sandboxCredentialSelection";
import type { AppliedAgentModels } from "./sandboxModels";

export interface SandboxCredentialDocument extends CredentialSelection {
  models: AppliedAgentModels[];
  temporaryAgents?: TemporaryAgent[];
  oneOff?: LocalInjectionRule[];
}
type Kind = "agents" | "sandboxes";
export const credentialDocumentsKey = ["isodo", "credential-documents"] as const;
export const CREDENTIAL_DOCUMENTS_CHANGED = "isodo:credential-documents-changed";
export async function putCredentialDocument(kind: Kind, id: string, value: unknown) {
  await isodoFetch(`/v1/credential-documents/${kind}/${encodeURIComponent(id)}`, {
    method: "PUT",
    signal: AbortSignal.timeout(20_000),
    body: JSON.stringify(value),
  });
  window.dispatchEvent(new Event(CREDENTIAL_DOCUMENTS_CHANGED));
}
async function listDocuments<T>(kind: Kind): Promise<T[]> {
  const result = await isodoFetch<{ items: { id: string; value: T }[] }>(
    `/v1/credential-documents/${kind}`,
  );
  return result.items.map((item) => item.value);
}
export async function fetchServerAgentConfigs(): Promise<AgentConfig[]> {
  const items = await listDocuments<AgentConfig>("agents");
  const legacy = readLegacyAgentConfigs();
  if (legacy.length) {
    for (const config of legacy)
      if (!items.some((item) => item.id === config.id)) {
        await putCredentialDocument("agents", config.id, config);
        items.push(config);
      }
    localStorage.removeItem(AGENT_CONFIGS_STORAGE_KEY);
  }
  return items;
}
export async function fetchServerSandboxCredentials(): Promise<
  Record<string, SandboxCredentialDocument>
> {
  const result = await isodoFetch<{ items: { id: string; value: SandboxCredentialDocument }[] }>(
    "/v1/credential-documents/sandboxes",
  );
  const docs = Object.fromEntries(result.items.map((item) => [item.id, item.value]));
  // Import legacy metadata only after Agent references have been migrated.
  const agents = await fetchServerAgentConfigs();
  const prefix = "isodo.sandbox-credential-selection.v1:";
  const keys = Array.from({ length: localStorage.length }, (_, i) => localStorage.key(i)).filter(
    (key): key is string => Boolean(key?.startsWith(prefix)),
  );
  for (const key of keys) {
    const id = key.slice(prefix.length);
    if (!docs[id]) {
      const selection = JSON.parse(
        localStorage.getItem(key) || "null",
      ) as CredentialSelection | null;
      if (
        !selection ||
        !Array.isArray(selection.agentIds) ||
        !Array.isArray(selection.credentialIds)
      )
        continue;
      const stored = localStorage.getItem(`isodo.sandbox-agent-models.v1:${id}`);
      const models = stored
        ? JSON.parse(stored)
        : agents.filter((agent) => selection.agentIds.includes(agent.id));
      docs[id] = { ...selection, models };
      await putCredentialDocument("sandboxes", id, docs[id]);
    }
    localStorage.removeItem(key);
    localStorage.removeItem(`isodo.sandbox-agent-models.v1:${id}`);
  }
  return docs;
}
export function useServerAgentConfigs() {
  const auth = useAuth();
  return useQuery({
    queryKey: [...credentialDocumentsKey, "agents", getAuthGeneration()],
    enabled: auth.status === "authenticated",
    queryFn: fetchServerAgentConfigs,
  });
}
export function useServerSandboxCredentials() {
  const auth = useAuth();
  return useQuery({
    queryKey: [...credentialDocumentsKey, "sandboxes", getAuthGeneration()],
    enabled: auth.status === "authenticated",
    queryFn: fetchServerSandboxCredentials,
  });
}
export function useAgentConfigMutations() {
  const client = useQueryClient();
  return {
    async save(config: AgentConfig) {
      await putCredentialDocument("agents", config.id, config);
      await client.invalidateQueries({ queryKey: credentialDocumentsKey });
    },
    async remove(id: string) {
      await isodoFetch(`/v1/credential-documents/agents/${encodeURIComponent(id)}`, {
        method: "DELETE",
      });
      await client.invalidateQueries({ queryKey: credentialDocumentsKey });
    },
  };
}
