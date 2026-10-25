export interface SandboxInfo {
  sandboxID: string;
  templateID: string;
  alias?: string | null;
  domain?: string | null;
  state: "running" | "paused";
  startedAt: string;
  endAt: string;
  cpuCount: number;
  memoryMB: number;
  diskSizeMB: number;
  envdVersion?: string;
  envdAccessToken?: string;
  trafficAccessToken?: string | null;
  metadata?: Record<string, string>;
}

import type { SandboxCreateInput as SDKCreateInput } from "@t3tools/sandbox-client";

export type SandboxCreateInput = SDKCreateInput;

export interface SandboxCredentials {
  e2bApiKey: string;
  e2bApiUrl: string;
  openaiApiKey: string;
  openaiBaseUrl: string;
  templateID: string;
}

export const DEFAULT_TEMPLATE_ID = "usrrhp4zsns5yyithi8a";
export const DEFAULT_OPENAI_BASE_URL = "https://api.fenno.ai";
export const DEFAULT_TIMEOUT_HOURS = 3;
export const DEFAULT_T3_PORT = 8080;

export function mergeSandboxCredentials(
  current: Partial<SandboxCredentials>,
  updates: Pick<SandboxCredentials, "e2bApiKey" | "e2bApiUrl" | "templateID"> &
    Partial<Pick<SandboxCredentials, "openaiApiKey" | "openaiBaseUrl">>,
): SandboxCredentials {
  return {
    ...updates,
    openaiApiKey: updates.openaiApiKey ?? current.openaiApiKey ?? "",
    openaiBaseUrl:
      updates.openaiBaseUrl !== undefined
        ? updates.openaiBaseUrl || DEFAULT_OPENAI_BASE_URL
        : current.openaiBaseUrl || DEFAULT_OPENAI_BASE_URL,
  };
}

export function isSandboxConnecting(
  sandboxID: string,
  connectingSandboxID: string | null,
): boolean {
  return sandboxID === connectingSandboxID;
}

export function sandboxUrl(
  sandboxID: string,
  domain: string | null | undefined,
  port = DEFAULT_T3_PORT,
): string | null {
  if (!/^[a-z0-9-]+$/i.test(sandboxID) || !Number.isInteger(port) || port < 1 || port > 65_535) {
    return null;
  }

  const resolvedDomain = domain
    ?.trim()
    .replace(/^https?:\/\//, "")
    .replace(/\/+$/, "");
  if (
    !resolvedDomain ||
    !/^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(
      resolvedDomain,
    )
  ) {
    return null;
  }

  return `https://${port}-${sandboxID}.${resolvedDomain}`;
}
