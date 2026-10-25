import type {
  SandboxConnectionInfo,
  SandboxCreateInput,
  SandboxInjection,
  SandboxInfo,
} from "@t3tools/shared/sandbox";
import { DEFAULT_SANDBOX_API_URL } from "@t3tools/shared/sandbox";
import type { SandboxClientOptions } from "./types";

export const SANDBOX_API_TIMEOUT_MS = 60_000;

export interface SandboxListOptions {
  readonly metadata?: Readonly<Record<string, string>>;
}

function buildMetadataQuery(metadata: Readonly<Record<string, string>>): string {
  return Object.entries(metadata)
    .map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`)
    .join("&");
}

export function resolveApiUrl(apiUrl?: string): string {
  return apiUrl?.trim().replace(/\/+$/, "") || DEFAULT_SANDBOX_API_URL;
}

export function buildCreateBody(input: SandboxCreateInput) {
  return {
    templateID: input.templateID,
    timeout: input.timeout,
    autoPause: input.autoPause ?? true,
    ...(input.envVars ? { envVars: input.envVars } : {}),
    ...(input.resources ? { resources: input.resources } : {}),
    ...(input.injections ? { injections: input.injections } : {}),
    ...(input.metadata ? { metadata: input.metadata } : {}),
    ...(input.network ? { network: input.network } : {}),
  };
}

async function parseResponse<T>(response: Response): Promise<T> {
  const body = await response.text();
  return body.trim() ? (JSON.parse(body) as T) : (undefined as T);
}

export function createSandboxApi(options: SandboxClientOptions) {
  const baseUrl = resolveApiUrl(options.apiUrl);
  const apiKey = options.apiKey;
  const fetchImpl = options.fetchImpl ?? fetch;

  const request = async <T>(path: string, init?: RequestInit): Promise<T> => {
    const res = await fetchImpl(`${baseUrl}${path}`, {
      ...init,
      signal: init?.signal ?? AbortSignal.timeout(SANDBOX_API_TIMEOUT_MS),
      headers: {
        "Content-Type": "application/json",
        "X-API-Key": apiKey,
        ...init?.headers,
      },
    });
    if (!res.ok) {
      throw new Error(`${res.status} ${await res.text().catch(() => res.statusText)}`);
    }
    return parseResponse<T>(res);
  };

  return {
    create: (input: SandboxCreateInput): Promise<SandboxInfo> =>
      request<SandboxInfo>("/sandboxes", {
        method: "POST",
        body: JSON.stringify(buildCreateBody(input)),
      }),

    list: (options?: SandboxListOptions): Promise<SandboxInfo[]> => {
      const metadata = options?.metadata;
      const query =
        metadata && Object.keys(metadata).length > 0
          ? `?metadata=${encodeURIComponent(buildMetadataQuery(metadata))}`
          : "";
      return request<SandboxInfo[]>(`/v2/sandboxes${query}`);
    },

    get: (sandboxID: string): Promise<SandboxInfo> =>
      request<SandboxInfo>(`/sandboxes/${encodeURIComponent(sandboxID)}`),

    delete: (sandboxID: string): Promise<void> =>
      request<void>(`/sandboxes/${encodeURIComponent(sandboxID)}`, { method: "DELETE" }),

    refresh: (sandboxID: string, durationSeconds: number): Promise<void> =>
      request<void>(`/sandboxes/${encodeURIComponent(sandboxID)}/refreshes`, {
        method: "POST",
        body: JSON.stringify({ duration: durationSeconds }),
      }),

    pause: (sandboxID: string): Promise<void> =>
      request<void>(`/sandboxes/${encodeURIComponent(sandboxID)}/pause`, { method: "POST" }),

    resume: (sandboxID: string, timeout: number): Promise<SandboxInfo> =>
      request<SandboxInfo>(`/sandboxes/${encodeURIComponent(sandboxID)}/resume`, {
        method: "POST",
        body: JSON.stringify({ timeout, autoPause: true }),
      }),

    connect: (sandboxID: string, timeout: number): Promise<SandboxConnectionInfo> =>
      request<SandboxConnectionInfo>(`/sandboxes/${encodeURIComponent(sandboxID)}/connect`, {
        method: "POST",
        body: JSON.stringify({ timeout }),
      }),

    getInjections: (sandboxID: string): Promise<SandboxInjection[]> =>
      request<{ injections: SandboxInjection[] }>(
        `/sandboxes/${encodeURIComponent(sandboxID)}/injections`,
      ).then((response) => response.injections),

    updateInjections: (
      sandboxID: string,
      injections: ReadonlyArray<SandboxInjection>,
    ): Promise<void> =>
      request<void>(`/sandboxes/${encodeURIComponent(sandboxID)}/injections`, {
        method: "PUT",
        body: JSON.stringify({ injections }),
      }),
  };
}

export type SandboxApi = ReturnType<typeof createSandboxApi>;
