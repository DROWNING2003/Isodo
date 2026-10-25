import type { EnvironmentId } from "@t3tools/contracts";
import type { SandboxInfo } from "@t3tools/shared/sandbox";
import { createSandboxApi } from "@t3tools/sandbox-client";
import { useNavigate } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo, useRef } from "react";

import { sandboxIdForEnvironment } from "../../features/isodo/sandboxEnvironmentMap";
import { environmentCatalog } from "../../connection/catalog";
import { sandboxIdFromHostname } from "../../connection/sandboxPrimaryGate";
import { useSandboxCredentials } from "../../hooks/useSandboxCredentials";
import { setActiveEnvironmentId, useActiveEnvironmentId } from "../../state/entities";
import { useEnvironments } from "../../state/environments";
import { useAtomCommand } from "../../state/use-atom-command";

const SANDBOX_STATE_REFRESH_INTERVAL_MS = 30_000;

export interface SandboxEnvironmentReference {
  readonly environmentId: EnvironmentId;
  readonly displayUrl: string | null;
}

export function sandboxIdFromUrl(url: string): string | null {
  try {
    return sandboxIdFromHostname(new URL(url).hostname);
  } catch {
    return null;
  }
}

export function sandboxEnvironmentIdsToRemove(
  environments: ReadonlyArray<SandboxEnvironmentReference>,
  sandboxes: ReadonlyArray<Pick<SandboxInfo, "sandboxID" | "state">>,
): ReadonlyArray<EnvironmentId> {
  const stateBySandboxId = new Map(sandboxes.map((sandbox) => [sandbox.sandboxID, sandbox.state]));

  return environments.flatMap((environment) => {
    if (
      sandboxIdForEnvironment(environment.environmentId) !== null ||
      environment.displayUrl === null
    )
      return [];
    const sandboxId = sandboxIdFromUrl(environment.displayUrl);
    return sandboxId !== null && stateBySandboxId.get(sandboxId) !== "running"
      ? [environment.environmentId]
      : [];
  });
}

export function SandboxEnvironmentSync() {
  const credentials = useSandboxCredentials();
  const { environments } = useEnvironments();
  const activeEnvironmentId = useActiveEnvironmentId();
  const removeEnvironment = useAtomCommand(environmentCatalog.remove, {
    label: "sandbox environment sync",
    reportFailure: false,
    reportDefect: false,
  });
  const navigate = useNavigate();
  const requestInFlight = useRef(false);
  const api = useMemo(
    () =>
      createSandboxApi({
        apiKey: credentials.credentials.e2bApiKey,
        apiUrl: credentials.credentials.e2bApiUrl,
      }),
    [credentials.credentials.e2bApiKey, credentials.credentials.e2bApiUrl],
  );
  const sandboxEnvironments = useMemo<ReadonlyArray<SandboxEnvironmentReference>>(
    () =>
      environments
        .filter(
          (environment) =>
            environment.entry.target._tag === "BearerConnectionTarget" &&
            environment.displayUrl !== null,
        )
        .map(({ environmentId, displayUrl }) => ({ environmentId, displayUrl })),
    [environments],
  );

  const sync = useCallback(async () => {
    if (
      !credentials.isLoaded ||
      !credentials.hasRequired ||
      sandboxEnvironments.length === 0 ||
      requestInFlight.current
    ) {
      return;
    }

    requestInFlight.current = true;
    try {
      const sandboxes = await api.list();
      const pausedEnvironmentIds = sandboxEnvironmentIdsToRemove(sandboxEnvironments, sandboxes);
      if (pausedEnvironmentIds.length === 0) return;

      const removedEnvironmentIds: EnvironmentId[] = [];
      for (const environmentId of pausedEnvironmentIds) {
        const result = await removeEnvironment(environmentId);
        if (result._tag === "Success") {
          removedEnvironmentIds.push(environmentId);
        }
      }

      if (activeEnvironmentId !== null && removedEnvironmentIds.includes(activeEnvironmentId)) {
        setActiveEnvironmentId(null);
        void navigate({ to: "/sandboxes", replace: true });
      }
    } catch {
      // A transient list failure must not evict a usable environment.
    } finally {
      requestInFlight.current = false;
    }
  }, [
    activeEnvironmentId,
    api,
    credentials.hasRequired,
    credentials.isLoaded,
    navigate,
    removeEnvironment,
    sandboxEnvironments,
  ]);

  useEffect(() => {
    void sync();
    if (!credentials.isLoaded || !credentials.hasRequired || sandboxEnvironments.length === 0) {
      return;
    }

    const interval = window.setInterval(() => {
      void sync();
    }, SANDBOX_STATE_REFRESH_INTERVAL_MS);
    return () => window.clearInterval(interval);
  }, [credentials.hasRequired, credentials.isLoaded, sandboxEnvironments.length, sync]);

  return null;
}
