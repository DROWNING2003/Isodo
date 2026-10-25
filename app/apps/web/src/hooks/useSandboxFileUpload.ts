import type { EnvironmentId } from "@t3tools/contracts";
import type { SandboxConnectionInfo, SandboxInfo } from "@t3tools/shared/sandbox";
import { useCallback, useEffect, useMemo, useState } from "react";

import { useEnvironmentHttpBaseUrl } from "../state/environments";
import { useSandboxApi } from "./useSandboxApi";
import { useSandboxCredentials } from "./useSandboxCredentials";
import { sandboxUploadPath } from "../components/sandbox/sandboxUpload";

const SANDBOX_CONNECTION_HOST_PATTERN = /^\d+-([a-z0-9-]+)\.(.+)$/i;

export interface SandboxConnectionTarget {
  readonly sandboxID: string;
}

export function mergeConnectedSandboxInfo(
  previous: SandboxInfo,
  connected: SandboxConnectionInfo,
): SandboxInfo {
  return {
    ...previous,
    ...connected,
    // The connect endpoint resumes paused sandboxes, but its Sandbox response
    // does not include the state field from SandboxDetail.
    state: "running",
  };
}

export function isSandboxUploadAllowed(sandbox: Pick<SandboxInfo, "state"> | null): boolean {
  return sandbox?.state === "running";
}

export function parseSandboxConnectionTarget(
  httpBaseUrl: string | null | undefined,
): SandboxConnectionTarget | null {
  if (!httpBaseUrl) return null;
  try {
    const hostname = new URL(httpBaseUrl).hostname.replace(/\.$/, "");
    const match = SANDBOX_CONNECTION_HOST_PATTERN.exec(hostname);
    if (!match?.[1] || !match[2]) return null;
    return { sandboxID: match[1] };
  } catch {
    return null;
  }
}

interface SandboxFileUploadState {
  readonly sandbox: SandboxInfo | null;
  readonly canOpen: boolean;
  readonly isLoading: boolean;
  readonly error: string | null;
  readonly upload: (files: readonly File[], directory: string) => Promise<void>;
}

export function useSandboxFileUpload(environmentId: EnvironmentId): SandboxFileUploadState {
  const credentials = useSandboxCredentials();
  const { api, uploadFile } = useSandboxApi(credentials.credentials);
  const httpBaseUrl = useEnvironmentHttpBaseUrl(environmentId);
  const target = useMemo(() => parseSandboxConnectionTarget(httpBaseUrl), [httpBaseUrl]);
  const [sandbox, setSandbox] = useState<SandboxInfo | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setSandbox(null);
    setError(null);

    if (!credentials.isLoaded || !credentials.hasRequired || target === null) {
      setIsLoading(false);
      return () => {
        cancelled = true;
      };
    }

    setIsLoading(true);
    void api
      .list()
      .then((sandboxes) => {
        if (cancelled) return;
        const listed = sandboxes.find((candidate) => candidate.sandboxID === target.sandboxID);
        if (!listed) {
          setError("当前工作区对应的沙箱不在可用列表中。");
          return;
        }
        setSandbox(listed);
      })
      .catch((cause) => {
        if (!cancelled) {
          setError(cause instanceof Error ? cause.message : "无法读取沙箱信息。");
        }
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [api, credentials.hasRequired, credentials.isLoaded, target]);

  const upload = useCallback(
    async (files: readonly File[], directory: string): Promise<void> => {
      if (sandbox === null) {
        throw new Error(error ?? "沙箱信息尚未准备好，请稍后重试。");
      }
      if (target === null) {
        throw new Error("当前工作区不是可上传的沙箱环境。");
      }

      // The v2 list response does not include envd credentials. Resolve the
      // connection details only when the user submits an upload, so listing
      // files never refreshes a sandbox or creates a background request loop.
      let uploadSandbox = sandbox;
      if (!sandbox.envdAccessToken?.trim() || !isSandboxUploadAllowed(sandbox)) {
        const connected = await api.connect(sandbox.sandboxID, 3600);
        uploadSandbox = mergeConnectedSandboxInfo(sandbox, connected);
        setSandbox(uploadSandbox);
      }
      if (!isSandboxUploadAllowed(uploadSandbox)) {
        throw new Error("沙箱已暂停，请先恢复后再上传。");
      }
      for (const file of files) {
        await uploadFile(uploadSandbox, sandboxUploadPath(directory, file.name), file, file.name);
      }
    },
    [api, error, sandbox, target, uploadFile],
  );

  return {
    sandbox,
    canOpen:
      target !== null &&
      credentials.hasRequired &&
      (sandbox === null || isSandboxUploadAllowed(sandbox)),
    isLoading,
    error,
    upload,
  };
}
