import { createSandboxApi } from "@t3tools/sandbox-client";
import type { SandboxInfo } from "@t3tools/shared/sandbox";

import { sandboxCredentialStore } from "../lib/sandboxCredentialStore";

const SANDBOX_HOSTNAME_PATTERN = /^\d+-([a-z0-9-]+)\./i;

export function sandboxIdFromHostname(hostname: string): string | null {
  return SANDBOX_HOSTNAME_PATTERN.exec(hostname.trim())?.[1] ?? null;
}

export function shouldProbeSandboxEnvironment(
  sandboxId: string | null,
  sandboxes: ReadonlyArray<Pick<SandboxInfo, "sandboxID" | "state">>,
): boolean {
  if (sandboxId === null) {
    return true;
  }

  const current = sandboxes.find((sandbox) => sandbox.sandboxID === sandboxId);
  return current?.state === "running";
}

export async function shouldProbeCurrentSandboxEnvironment(): Promise<boolean> {
  const sandboxId = sandboxIdFromHostname(window.location.hostname);
  if (sandboxId === null) {
    return true;
  }

  const credentials = await sandboxCredentialStore.get();
  if (credentials === null || credentials.e2bApiKey.trim() === "") {
    return true;
  }

  const api = createSandboxApi({
    apiKey: credentials.e2bApiKey,
    apiUrl: credentials.e2bApiUrl,
  });
  const sandboxes = await api.list();
  return shouldProbeSandboxEnvironment(sandboxId, sandboxes);
}
