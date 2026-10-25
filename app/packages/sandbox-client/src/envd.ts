import { ENVD_PORT, sandboxUrl } from "@t3tools/shared/sandbox";
import type { EnvdAccess } from "./types";

export const ENVD_HEALTH_CHECK_TIMEOUT_MS = 5_000;
export const ENVD_HEALTH_CHECK_RETRY_DELAY_MS = 3_000;
export const ENVD_PROCESS_START_TIMEOUT_MS = 60_000;
export const ENVD_FILE_RETRY_DELAY_MS = 1_000;

export function envdHeaders(access: EnvdAccess): Record<string, string> {
  return {
    Authorization: "Basic dXNlcjo=",
    "Connect-Protocol-Version": "1",
    "Content-Type": "application/connect+json",
    "X-Sandbox-ID": access.sandboxID,
    ...(access.envdAccessToken ? { "X-Access-Token": access.envdAccessToken } : {}),
    ...(access.trafficAccessToken ? { "e2b-traffic-access-token": access.trafficAccessToken } : {}),
  };
}

export function createEnvdProcessRequest(command: string): ArrayBuffer {
  const payload = new TextEncoder().encode(
    JSON.stringify({
      process: { cmd: "/bin/bash", args: ["-lc", command] },
      stdin: false,
    }),
  );
  const request = new ArrayBuffer(5 + payload.length);
  new DataView(request).setUint32(1, payload.length);
  new Uint8Array(request, 5).set(payload);
  return request;
}

export function envdFileUrl(sandboxID: string, domain: string, path: string): string {
  const envdUrl = sandboxUrl(sandboxID, domain, ENVD_PORT);
  if (!envdUrl) throw new Error("The sandbox envd URL could not be resolved.");
  const url = new URL(`${envdUrl}/files`);
  url.searchParams.set("path", path);
  url.searchParams.set("username", "user");
  return url.toString();
}

export async function envdHealthCheck(
  envdBase: string,
  headers: Record<string, string>,
  options?: {
    readonly fetchImpl?: typeof fetch;
    readonly retryCount?: number;
    readonly retryDelayMs?: number;
    readonly wait?: (ms: number) => Promise<void>;
  },
): Promise<boolean> {
  const fetchImpl = options?.fetchImpl ?? fetch;
  const retries = options?.retryCount ?? 30;
  const delay = options?.retryDelayMs ?? ENVD_HEALTH_CHECK_RETRY_DELAY_MS;
  const wait = options?.wait ?? ((ms: number) => new Promise((r) => setTimeout(r, ms)));
  for (let i = 0; i < retries; i++) {
    try {
      const res = await fetchImpl(`${envdBase}/health`, {
        method: "GET",
        headers,
        signal: AbortSignal.timeout(ENVD_HEALTH_CHECK_TIMEOUT_MS),
      });
      if (res.ok) return true;
    } catch {}
    if (i + 1 < retries) await wait(delay);
  }
  return false;
}

export async function envdProcessStart(
  envdBase: string,
  headers: Record<string, string>,
  command: string,
  options?: { readonly fetchImpl?: typeof fetch; readonly timeoutMs?: number },
): Promise<Response> {
  const fetchImpl = options?.fetchImpl ?? fetch;
  return fetchImpl(`${envdBase}/process.Process/Start`, {
    method: "POST",
    headers,
    body: createEnvdProcessRequest(command),
    signal: AbortSignal.timeout(options?.timeoutMs ?? ENVD_PROCESS_START_TIMEOUT_MS),
  });
}

export async function envdFileRead(
  fileUrl: string,
  headers: Record<string, string>,
  options?: {
    readonly fetchImpl?: typeof fetch;
    readonly retryCount?: number;
    readonly retryDelayMs?: number;
    readonly wait?: (ms: number) => Promise<void>;
  },
): Promise<Response | null> {
  const fetchImpl = options?.fetchImpl ?? fetch;
  const retries = options?.retryCount ?? 30;
  const delay = options?.retryDelayMs ?? ENVD_FILE_RETRY_DELAY_MS;
  const wait = options?.wait ?? ((ms: number) => new Promise((r) => setTimeout(r, ms)));
  for (let i = 0; i < retries; i++) {
    try {
      const res = await fetchImpl(fileUrl, {
        headers,
        signal: AbortSignal.timeout(3000),
      });
      if (res.ok) return res;
    } catch {}
    if (i + 1 < retries) await wait(delay);
  }
  return null;
}
