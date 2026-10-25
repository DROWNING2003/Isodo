import type { EnvironmentId } from "@t3tools/contracts";
import type { SandboxCredentials } from "@t3tools/contracts/sandbox";
import type { SandboxConnectionInfo, SandboxInfo } from "@t3tools/shared/sandbox";
import {
  createSandboxApi,
  detectCodexProviders,
  DEFAULT_SANDBOX_SKILLS,
  uploadSandboxFile as sdkUploadSandboxFile,
} from "@t3tools/sandbox-client";
import { useCallback, useMemo } from "react";
import {
  fetchSandboxT3ServerBundle,
  getSandboxPairingUrl,
  startSandboxT3Server,
} from "../lib/sandbox-client";
import { connectPairing } from "../connection/onboarding";
import { useAtomCommand } from "../state/use-atom-command";
import { getConfiguredSandboxInjections } from "../lib/sandboxInjections";
import type { SandboxConnectionStage } from "../lib/sandboxConnection";

export type SandboxConnectionProgress = (stage: SandboxConnectionStage) => void;

export function useSandboxApi(credentials: SandboxCredentials) {
  const api = useMemo(
    () =>
      createSandboxApi({
        apiKey: credentials.e2bApiKey,
        apiUrl: credentials.e2bApiUrl,
      }),
    [credentials.e2bApiKey, credentials.e2bApiUrl],
  );
  const connectPairingEnv = useAtomCommand(connectPairing, { reportFailure: false });

  const connect = useCallback(
    async (
      sandbox: SandboxConnectionInfo,
      onStage?: SandboxConnectionProgress,
    ): Promise<EnvironmentId> => {
      onStage?.("bundle");
      const allInjections = getConfiguredSandboxInjections(credentials);
      const providers = detectCodexProviders(
        allInjections.flatMap((i) => {
          const url = (i as { base_url?: string }).base_url;
          return url ? [{ base_url: url }] : [];
        }),
      );
      const serverBundle = await fetchSandboxT3ServerBundle();
      onStage?.("server");
      await startSandboxT3Server({
        sandboxID: sandbox.sandboxID,
        domain: sandbox.domain,
        codexProviders: providers,
        skills: DEFAULT_SANDBOX_SKILLS,
        serverBundle,
        ...(sandbox.envdAccessToken !== undefined
          ? { envdAccessToken: sandbox.envdAccessToken }
          : {}),
        ...(sandbox.trafficAccessToken !== undefined && sandbox.trafficAccessToken !== null
          ? { trafficAccessToken: sandbox.trafficAccessToken }
          : {}),
      });

      onStage?.("pairing");
      const pairingUrl = await getSandboxPairingUrl(sandbox.sandboxID, sandbox.domain, sandbox);

      onStage?.("codex");
      const result = await connectPairingEnv({ pairingUrl });
      if (result._tag !== "Success") {
        throw new Error("Failed to connect to sandbox");
      }
      return result.value as EnvironmentId;
    },
    [api, connectPairingEnv, credentials],
  );

  const uploadFile = useCallback(
    async (
      sandbox: SandboxInfo,
      destinationPath: string,
      file: Blob,
      fileName: string,
    ): Promise<void> => {
      await sdkUploadSandboxFile({
        sandboxID: sandbox.sandboxID,
        domain: sandbox.domain,
        path: destinationPath,
        file,
        fileName,
        ...(sandbox.envdAccessToken !== undefined
          ? { envdAccessToken: sandbox.envdAccessToken }
          : {}),
        ...(sandbox.trafficAccessToken !== undefined && sandbox.trafficAccessToken !== null
          ? { trafficAccessToken: sandbox.trafficAccessToken }
          : {}),
      });
    },
    [credentials],
  );

  return { api, connect, uploadFile };
}
