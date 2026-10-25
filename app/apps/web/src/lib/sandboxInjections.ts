import type { SandboxCredentials } from "@t3tools/contracts/sandbox";
import type {
  GithubInjection,
  HttpInjection,
  InjectionById,
  SandboxInjection,
} from "@t3tools/shared/sandbox";

import { getAdditionalInjections } from "./sandboxCredentialStore";

export type EditableSandboxInjectionType =
  | "http"
  | "openai"
  | "anthropic"
  | "gemini"
  | "qiniu"
  | "github";

export interface SandboxInjectionDraft {
  readonly id: number;
  readonly type: EditableSandboxInjectionType;
  readonly baseUrl: string;
  readonly secret: string;
  readonly header: string;
  readonly ifHeaders?: Readonly<Record<string, string>>;
  readonly ifQueries?: Readonly<Record<string, string>>;
  readonly wasMasked: boolean;
}

export interface SandboxInjectionDrafts {
  readonly drafts: ReadonlyArray<SandboxInjectionDraft>;
  readonly fixed: ReadonlyArray<InjectionById>;
}

const EDITABLE_TYPES = new Set<EditableSandboxInjectionType>([
  "http",
  "openai",
  "anthropic",
  "gemini",
  "qiniu",
  "github",
]);

export function getConfiguredSandboxInjections(
  credentials: SandboxCredentials,
): ReadonlyArray<SandboxInjection> {
  return [
    ...getAdditionalInjections(),
    ...(credentials.openaiApiKey?.trim()
      ? [
          {
            type: "openai" as const,
            api_key: credentials.openaiApiKey,
            base_url: credentials.openaiBaseUrl,
          },
        ]
      : []),
  ];
}

function isEditableType(type: string): type is EditableSandboxInjectionType {
  return EDITABLE_TYPES.has(type as EditableSandboxInjectionType);
}

function injectionSecret(injection: SandboxInjection): string {
  if ("api_key" in injection) return injection.api_key;
  if ("token" in injection) return injection.token;
  if ("headers" in injection) return Object.values(injection.headers ?? {})[0] ?? "";
  return "";
}

function injectionBaseUrl(injection: SandboxInjection): string {
  return "base_url" in injection ? (injection.base_url ?? "") : "";
}

function injectionHeader(injection: SandboxInjection): string {
  return "headers" in injection ? (Object.keys(injection.headers ?? {})[0] ?? "Authorization") : "";
}

function injectionKey(injection: SandboxInjection): string {
  return `${injection.type}:${injectionBaseUrl(injection)}`;
}

function isMaskedSecret(secret: string): boolean {
  return secret.includes("****");
}

function draftFromInjection(
  injection: SandboxInjection,
  configured: ReadonlyArray<SandboxInjection>,
  id: number,
): SandboxInjectionDraft | null {
  if (!isEditableType(injection.type)) return null;

  const configuredMatch = configured.find(
    (candidate) => candidate.type !== "id" && injectionKey(candidate) === injectionKey(injection),
  );
  const remoteSecret = injectionSecret(injection);
  const configuredSecret = configuredMatch ? injectionSecret(configuredMatch) : "";
  const secret = configuredSecret || (isMaskedSecret(remoteSecret) ? "" : remoteSecret);

  return {
    id,
    type: injection.type,
    baseUrl: injectionBaseUrl(injection),
    secret,
    header: injectionHeader(injection) || (configuredMatch ? injectionHeader(configuredMatch) : ""),
    ...("if_headers" in injection && injection.if_headers
      ? { ifHeaders: injection.if_headers }
      : {}),
    ...("if_queries" in injection && injection.if_queries
      ? { ifQueries: injection.if_queries }
      : {}),
    wasMasked: isMaskedSecret(remoteSecret) && !configuredSecret,
  };
}

export function createSandboxInjectionDrafts(
  runtimeInjections: ReadonlyArray<SandboxInjection>,
  configuredInjections: ReadonlyArray<SandboxInjection>,
): SandboxInjectionDrafts {
  if (runtimeInjections.length === 0) {
    const drafts = configuredInjections
      .flatMap((injection, id) => draftFromInjection(injection, configuredInjections, id))
      .filter((draft): draft is SandboxInjectionDraft => draft !== null);
    return {
      drafts,
      fixed: configuredInjections.filter(
        (injection): injection is InjectionById => injection.type === "id",
      ),
    };
  }

  const drafts = runtimeInjections
    .flatMap((injection, id) => draftFromInjection(injection, configuredInjections, id))
    .filter((draft): draft is SandboxInjectionDraft => draft !== null);
  return {
    drafts,
    fixed: runtimeInjections.filter(
      (injection): injection is InjectionById => injection.type === "id",
    ),
  };
}

export function validateSandboxInjectionDrafts(
  drafts: ReadonlyArray<SandboxInjectionDraft>,
): string | null {
  for (const [index, draft] of drafts.entries()) {
    if (draft.type === "http" && !draft.baseUrl.trim()) {
      return `第 ${index + 1} 条 HTTP 注入需要 Base URL。`;
    }
    if (!draft.secret.trim()) {
      return `第 ${index + 1} 条注入需要重新填写密钥。`;
    }
  }
  return null;
}

export function serializeSandboxInjectionDrafts(
  drafts: ReadonlyArray<SandboxInjectionDraft>,
  fixed: ReadonlyArray<InjectionById>,
): SandboxInjection[] {
  return [
    ...fixed,
    ...drafts.map((draft) => {
      const matchRules = {
        ...(draft.ifHeaders ? { if_headers: draft.ifHeaders } : {}),
        ...(draft.ifQueries ? { if_queries: draft.ifQueries } : {}),
      };
      if (draft.type === "http") {
        return {
          type: "http" as const,
          base_url: draft.baseUrl.trim(),
          headers: { [draft.header.trim() || "Authorization"]: draft.secret.trim() },
          ...matchRules,
        } satisfies HttpInjection;
      }
      if (draft.type === "github") {
        return {
          type: "github" as const,
          ...(draft.baseUrl.trim() ? { base_url: draft.baseUrl.trim() } : {}),
          token: draft.secret.trim(),
          ...matchRules,
        } satisfies GithubInjection;
      }
      return {
        type: draft.type,
        ...(draft.baseUrl.trim() ? { base_url: draft.baseUrl.trim() } : {}),
        api_key: draft.secret.trim(),
        ...matchRules,
      } satisfies Exclude<SandboxInjection, HttpInjection | GithubInjection | InjectionById>;
    }),
  ];
}
