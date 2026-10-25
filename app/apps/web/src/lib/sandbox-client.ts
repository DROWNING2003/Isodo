import {
  DEFAULT_T3_SERVER_BUNDLE_URL,
  fetchT3ServerBundle,
  type SandboxInfo,
  getT3PairingUrl,
  startT3Server,
} from "@t3tools/sandbox-client";

export type { T3StartOptions as StartT3ServerOptions } from "@t3tools/sandbox-client";
export { startT3Server as startSandboxT3Server };

export const SANDBOX_T3_SERVER_BUNDLE_URL = DEFAULT_T3_SERVER_BUNDLE_URL;

export async function fetchSandboxT3ServerBundle(fetchImpl: typeof fetch = fetch): Promise<Blob> {
  return fetchT3ServerBundle(SANDBOX_T3_SERVER_BUNDLE_URL, fetchImpl);
}

export async function getSandboxPairingUrl(
  sandboxID: string,
  domain: string,
  access?: Pick<SandboxInfo, "envdAccessToken" | "trafficAccessToken">,
): Promise<string> {
  return getT3PairingUrl(sandboxID, domain, access);
}
