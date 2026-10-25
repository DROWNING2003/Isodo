import { writeAgentConfigurationFile } from "./sandboxFiles";
import { useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createInjectionRule,
  deleteInjectionRule,
  getInjectionRuleMaterial,
  getSandboxInjections as getRemoteSandboxInjections,
  setSandboxInjections as setRemoteSandboxInjections,
  listInjectionRules,
  updateInjectionRule,
  type CreateInjectionRule,
  type InjectionRule,
  type InjectionRuleMaterial,
  type InjectionRuleType,
  type PatchInjectionRule,
} from "../../api/public.generated";
import { IsodoAPIError } from "../../api/transport";
import { getAuthGeneration, useAuth } from "./auth";

/** Mirror of `internal/node/injection.Rule`; secrets only exist in transit. */
export interface LocalInjectionSpec {
  type: InjectionRuleType;
  base_url?: string;
  header_name?: string;
  if_headers?: Record<string, string>;
  if_queries?: Record<string, string>;
}

export interface LocalInjectionSecret {
  api_key?: string;
  token?: string;
  headers?: Record<string, string>;
}

export interface LocalInjectionRule {
  key: string;
  name: string;
  kind: "persistent" | "one-off";
  persistent_id?: string;
  spec: LocalInjectionSpec;
  secret?: LocalInjectionSecret;
  secret_hint?: string;
}

export interface LocalInjectionStats {
  injected: number;
  last_host?: string;
  last_path?: string;
  last_rule?: string;
  last_injected_at?: string;
}

export interface LocalSandboxInjections {
  rules: LocalInjectionRule[];
  enabled: boolean;
  proxy_port?: number;
  stats: LocalInjectionStats;
}

export const injectionRulesQueryKey = ["isodo", "injection-rules"] as const;

export function useInjectionRules() {
  const auth = useAuth();
  return useQuery({
    queryKey: [...injectionRulesQueryKey, getAuthGeneration(), auth.user?.name ?? ""],
    enabled: auth.status === "authenticated",
    queryFn: async (): Promise<InjectionRule[]> => (await listInjectionRules()).items,
    staleTime: 10_000,
  });
}

export function useInjectionRuleMutations() {
  const client = useQueryClient();
  const invalidate = () =>
    client.invalidateQueries({ queryKey: injectionRulesQueryKey, exact: false });
  return {
    async create(input: CreateInjectionRule) {
      const created = await createInjectionRule(input);
      await invalidate();
      return created;
    },
    async update(id: string, input: PatchInjectionRule) {
      const updated = await updateInjectionRule(id, input);
      await invalidate();
      return updated;
    },
    async remove(id: string) {
      await deleteInjectionRule(id);
      // Revoke the local copy immediately so a running proxy cannot keep using
      // a rule that no longer exists in the account library.
      if (window.isodoDesktop) {
        try {
          await window.isodoDesktop.removePersistentInjection(id);
        } catch {
          throw new Error("账号规则已删除，但本地撤销失败。请再次点击删除以重试本地撤销。");
        }
      }
      await invalidate();
      await client.invalidateQueries({ queryKey: ["isodo", "injection-summary"] });
    },
  };
}

export function useInjectionSummary() {
  return useQuery({
    queryKey: ["isodo", "injection-summary"],
    enabled: Boolean(window.isodoDesktop),
    queryFn: async (): Promise<Record<string, number>> => {
      const bridge = window.isodoDesktop;
      if (!bridge) return {};
      const result = (await bridge.getInjectionSummary()) as {
        persistent?: Record<string, number>;
      };
      return result.persistent ?? {};
    },
    staleTime: 5_000,
  });
}

export function useSandboxInjections(sandboxId: string, enabled = true) {
  return useQuery({
    queryKey: ["isodo", "sandbox-injections", sandboxId],
    enabled:
      enabled &&
      Boolean(sandboxId) &&
      (!sandboxId.startsWith("local-") || Boolean(window.isodoDesktop)),
    queryFn: async (): Promise<LocalSandboxInjections> => {
      if (!sandboxId.startsWith("local-")) return getRemoteSandboxInjections(sandboxId);
      const bridge = window.isodoDesktop;
      if (!bridge) throw new Error("本地运行时不可用");
      return (await bridge.getSandboxInjections(sandboxId)) as LocalSandboxInjections;
    },
    staleTime: 5_000,
  });
}

export function useSandboxInjectionWriter(sandboxId: string) {
  const client = useQueryClient();
  return useCallback(
    async (rules: readonly LocalInjectionRule[]): Promise<LocalSandboxInjections> => {
      const bridge = window.isodoDesktop;
      if (sandboxId.startsWith("local-") && !bridge) throw new Error("本地运行时不可用");
      const result = sandboxId.startsWith("local-")
        ? ((await bridge!.setSandboxInjections(sandboxId, rules)) as LocalSandboxInjections)
        : await setRemoteSandboxInjections(sandboxId, { rules: [...rules] });
      client.setQueryData(["isodo", "sandbox-injections", sandboxId], result);
      // The write is complete. Background refreshes must not hold the save button.
      void client.invalidateQueries({ queryKey: ["isodo", "injection-summary"] }).catch(() => {});
      return result;
    },
    [client, sandboxId],
  );
}

export const writeSandboxAgentConfig = writeAgentConfigurationFile;

export async function fetchInjectionMaterials(
  ids: readonly string[],
): Promise<Map<string, InjectionRuleMaterial>> {
  const entries = await Promise.all(
    ids.map(async (id) => {
      try {
        return [id, await getInjectionRuleMaterial(id)] as const;
      } catch (error) {
        if (error instanceof IsodoAPIError && error.status === 404) return null;
        throw error;
      }
    }),
  );
  return new Map(
    entries.filter((entry): entry is readonly [string, InjectionRuleMaterial] => entry !== null),
  );
}

export function localRuleFromPersistent(
  rule: InjectionRule,
  material: InjectionRuleMaterial,
): LocalInjectionRule {
  return {
    key: `persistent:${rule.id}`,
    name: rule.name,
    kind: "persistent",
    persistent_id: rule.id,
    spec: {
      type: rule.type === "mcp" ? "http" : rule.type,
      ...(rule.base_url ? { base_url: rule.base_url } : {}),
      ...(rule.header_name ? { header_name: rule.header_name } : {}),
      ...(rule.if_headers ? { if_headers: rule.if_headers } : {}),
      ...(rule.if_queries ? { if_queries: rule.if_queries } : {}),
    },
    secret: {
      ...(material.api_key ? { api_key: material.api_key } : {}),
      ...(material.token ? { token: material.token } : {}),
      ...(material.headers ? { headers: material.headers } : {}),
    },
    secret_hint: rule.secret_hint,
  };
}

export function persistentIdFromLocal(rule: LocalInjectionRule): string | null {
  return rule.kind === "persistent" ? (rule.persistent_id ?? null) : null;
}

export function ruleMaterialIsSet(rule: LocalInjectionRule): boolean {
  if (rule.secret?.api_key || rule.secret?.token) return true;
  return Object.values(rule.secret?.headers ?? {}).some((value) => value.trim().length > 0);
}

export function ruleSecretPreview(rule: LocalInjectionRule): string {
  if (rule.secret_hint) return rule.secret_hint;
  if (rule.secret?.api_key) return `••••${rule.secret.api_key.slice(-4)}`;
  if (rule.secret?.token) return `••••${rule.secret.token.slice(-4)}`;
  const header = Object.values(rule.secret?.headers ?? {})[0];
  return header ? `••••${header.slice(-4)}` : "••••";
}

export function ruleTarget(rule: { spec: LocalInjectionSpec }): string {
  if (rule.spec.base_url) return rule.spec.base_url;
  switch (rule.spec.type) {
    case "openai":
      return "api.openai.com";
    case "anthropic":
      return "api.anthropic.com";
    case "gemini":
      return "generativelanguage.googleapis.com";
    case "qiniu":
      return "api.qnaigc.com";
    case "github":
      return "github.com · api.github.com";
    default:
      return "未设置目标";
  }
}

export function ruleTypeLabel(type: InjectionRuleType): string {
  switch (type) {
    case "mcp":
      return "MCP";
    case "http":
      return "HTTP Header";
    case "openai":
      return "OpenAI";
    case "anthropic":
      return "Anthropic";
    case "gemini":
      return "Gemini";
    case "qiniu":
      return "七牛 AI";
    case "github":
      return "GitHub";
  }
}
