import { useCallback, useMemo } from "react";
import type {
  SandboxConnectionInfo,
  CodexProviderConfig,
  SandboxSkill,
} from "@t3tools/sandbox-client";
import {
  createSandboxApi,
  startT3Server as sdkStartT3Server,
  T3_SERVER_BUNDLE_FILE_NAME,
  getT3PairingUrl,
  createEnvdProcessRequest,
  buildT3StartCommand,
  buildT3PairingCommand,
  DEFAULT_T3_SERVER_COMMAND,
  type SandboxApi,
} from "@t3tools/sandbox-client";

export { envdHeaders, createEnvdProcessRequest } from "@t3tools/sandbox-client";
export {
  buildCreateBody as buildSandboxCreateBody,
  resolveApiUrl as resolveSandboxApiUrl,
  startT3Server,
  waitForT3Server,
} from "@t3tools/sandbox-client";
export {
  buildT3PairingCommand,
  buildT3StartCommand,
  readPairingUrlFromJson,
  envdFileUrl,
} from "@t3tools/sandbox-client";

export const SANDBOX_T3_SERVER_COMMAND = DEFAULT_T3_SERVER_COMMAND;

// Legacy test helpers
export function createT3StartRequest(
  providers: ReadonlyArray<import("@t3tools/sandbox-client").CodexProviderConfig>,
  skills?: ReadonlyArray<import("@t3tools/sandbox-client").SandboxSkill>,
): ArrayBuffer {
  return createEnvdProcessRequest(
    buildT3StartCommand(providers, skills ?? [], SANDBOX_T3_SERVER_COMMAND),
  );
}

export function createT3PairingRequest(serverUrl: string): ArrayBuffer {
  return createEnvdProcessRequest(buildT3PairingCommand(serverUrl));
}

export async function parseSandboxApiResponse<T>(response: Response): Promise<T> {
  const body = await response.text();
  return body.trim() ? (JSON.parse(body) as T) : (undefined as T);
}
export type { SandboxApi };

export const DEFAULT_SANDBOX_API_URL = "https://cn-yangzhou-1-sandbox.qiniuapi.com";

interface UseSandboxApiOptions {
  readonly apiKey: string;
  readonly apiUrl?: string;
  readonly serverBundleUrl: string;
}

export const MOBILE_T3_SERVER_BUNDLE_URL =
  process.env.EXPO_PUBLIC_T3_SERVER_BUNDLE_URL?.trim() ?? "";

export async function downloadMobileT3ServerBundle(url: string): Promise<Blob> {
  const normalizedUrl = url.trim();
  if (!normalizedUrl) {
    throw new Error("A T3 server bundle URL is required.");
  }

  const { File, Paths } = await import("expo-file-system");
  const destination = new File(Paths.cache, T3_SERVER_BUNDLE_FILE_NAME);
  const downloaded = await File.downloadFileAsync(normalizedUrl, destination, {
    idempotent: true,
  });

  return downloaded;
}

export function useSandboxApi(options: UseSandboxApiOptions) {
  const api = useMemo(
    () => createSandboxApi({ apiKey: options.apiKey, apiUrl: options.apiUrl }),
    [options.apiKey, options.apiUrl],
  );

  const startSandboxT3Server = useCallback(
    async (
      sandbox: SandboxConnectionInfo,
      codexProviders?: ReadonlyArray<CodexProviderConfig>,
      skills?: ReadonlyArray<SandboxSkill>,
    ): Promise<void> => {
      const serverBundle = await downloadMobileT3ServerBundle(options.serverBundleUrl);
      await sdkStartT3Server({
        sandboxID: sandbox.sandboxID,
        domain: sandbox.domain,
        codexProviders,
        skills,
        serverBundle,
        serverCommand: SANDBOX_T3_SERVER_COMMAND,
        envdAccessToken: sandbox.envdAccessToken,
        trafficAccessToken: sandbox.trafficAccessToken,
      });
    },
    [options.serverBundleUrl],
  );

  const getPairingUrl = useCallback(
    async (
      sandboxID: string,
      domain: string,
      access?: Pick<SandboxConnectionInfo, "envdAccessToken" | "trafficAccessToken">,
    ): Promise<string> => {
      return getT3PairingUrl(sandboxID, domain, access);
    },
    [],
  );

  return {
    api,
    createSandbox: api.create,
    listSandboxes: api.list,
    deleteSandbox: api.delete,
    pauseSandbox: api.pause,
    resumeSandbox: api.resume,
    refreshSandbox: api.refresh,
    connectSandbox: api.connect,
    startSandboxT3Server,
    getPairingUrl,
  } as const;
}
