import { sandboxClientFor } from "./sandboxTransports";
import { bootstrapSandboxPreview } from "../../api/preview";
import { useMemo } from "react";
import { useLocalSandboxes, usePlatformSandboxes } from "./sandboxQueries";

import type { CloudSandbox } from "@t3tools/sandbox-client/cloud";
import { type Sandbox as PlatformSandbox } from "../../api/public.generated";

/**
 * Platform sandboxes live in the cloud control plane, not in the local mock
 * cloud store, so a route reached with one of their IDs has to resolve them
 * from the API instead.
 */
export interface PlatformSandboxRecord {
  readonly id: string;
  readonly name: string;
  readonly status: "running" | "starting" | "failed" | "stopped";
  readonly description: string;
  readonly resources?: {
    readonly vcpu?: number;
    readonly memory_bytes?: number;
    readonly disk_bytes?: number;
  };
}

export function toPlatformRecord(sandbox: PlatformSandbox): PlatformSandboxRecord {
  return {
    id: sandbox.id,
    name: sandbox.name,
    status:
      sandbox.observed_state === "running"
        ? "running"
        : ["requested", "preparing", "starting"].includes(sandbox.observed_state)
          ? "starting"
          : ["failed", "error"].includes(sandbox.observed_state)
            ? "failed"
            : "stopped",
    description: sandbox.template_id ? "模板沙箱" : "Agent 工作台",
  };
}

/**
 * Presents a platform sandbox through the same view model the workspace already
 * renders for local sandboxes. Resources are not reported per sandbox by the
 * control plane yet, so the plan defaults are shown instead of invented values.
 */
export function platformSandboxView(record: PlatformSandboxRecord): CloudSandbox {
  return {
    id: record.id,
    name: record.name,
    template: "dev",
    status: record.status === "running" ? "running" : "paused",
    cpu: 2,
    memoryGB: 4,
    diskGB: 20,
    connectionIds: [],
    files: [],
  };
}

export function usePlatformSandbox(sandboxId: string): {
  record: PlatformSandboxRecord | null;
  loading: boolean;
} {
  const query = usePlatformSandboxes(sandboxId !== "");
  const record = useMemo(() => {
    const match = sandboxId ? query.data?.find((item) => item.id === sandboxId) : null;
    return match ? toPlatformRecord(match) : null;
  }, [sandboxId, query.data]);
  return { record, loading: sandboxId !== "" && query.isPending && query.fetchStatus !== "idle" };
}

/**
 * Resolves the short-lived pairing URL for a sandbox. Local sandboxes are
 * served by the desktop runtime; everything else asks the control plane.
 * The URL is opaque: callers must not rebuild or parse it.
 */
export async function resolvePairingUrl(
  sandboxId: string,
  options: { readonly local: boolean },
): Promise<string> {
  const link = await sandboxClientFor(options.local ? "local" : "cloud").createAppLink(sandboxId);
  await bootstrapSandboxPreview(link.pairing_url);
  return link.pairing_url;
}

/** One sandbox owned by the desktop runtime, if it owns this id. */
export function useLocalRuntimeSandbox(sandboxId: string): {
  record: PlatformSandboxRecord | null;
  loading: boolean;
} {
  const query = useLocalSandboxes(sandboxId !== "");
  const record = useMemo<PlatformSandboxRecord | null>(() => {
    const match = sandboxId ? query.data?.find((item) => item.id === sandboxId) : null;
    return match
      ? {
          id: match.id,
          name: match.name || match.id.slice(0, 8),
          status:
            match.state === "running"
              ? "running"
              : ["requested", "preparing", "starting"].includes(match.state ?? "")
                ? "starting"
                : match.state === "error"
                  ? "failed"
                  : "stopped",
          description: "本地沙箱",
          ...(match.resources ? { resources: match.resources } : {}),
        }
      : null;
  }, [sandboxId, query.data]);
  return { record, loading: sandboxId !== "" && query.isPending && query.fetchStatus !== "idle" };
}
