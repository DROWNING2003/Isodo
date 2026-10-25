import { Navigate, useLocation } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useComposerDraftStore, DraftId } from "../../composerDraftStore";
import { useEnvironments } from "../../state/environments";
import { sandboxIdForEnvironment, isSandboxEnvironmentDisconnected } from "./sandboxEnvironmentMap";
import { useLocalSandboxes, usePlatformSandboxes } from "./sandboxQueries";
import { EnvironmentId } from "@t3tools/contracts";

export function workspaceAccess(input: {
  ready: boolean;
  sandboxId: string | null;
  disconnected: boolean;
  loading: boolean;
  failed: boolean;
  state: string | undefined;
}): "wait" | "allow" | "list" {
  if (!input.ready) return "wait";
  if (!input.sandboxId || input.disconnected) return "list";
  if (input.loading) return "wait";
  if (
    input.failed ||
    !input.state ||
    ["stopped", "paused", "deleted", "failed"].includes(input.state)
  )
    return "list";
  return "allow";
}
export function IsodoWorkspaceGate({ children }: { children: ReactNode }) {
  const path = useLocation({ select: (location) => location.pathname });
  const segments = path.split("/").filter(Boolean);
  const isDraft = segments[0] === "draft";
  const { environments, isReady } = useEnvironments();
  const draft = useComposerDraftStore((store) =>
    isDraft && segments[1] ? store.getDraftSession(DraftId.make(segments[1])) : null,
  );
  const taskEnvironment = isDraft
    ? draft?.environmentId
    : environments.find((item) => item.environmentId === segments[0])?.environmentId;
  const taskRoute =
    isDraft ||
    (segments.length === 2 &&
      ![
        "sandboxes",
        "developer",
        "settings",
        "sandbox-connections",
        "connect",
        "projects",
      ].includes(segments[0] ?? ""));
  const sandboxId = taskEnvironment ? sandboxIdForEnvironment(taskEnvironment) : null;
  const local = useLocalSandboxes(taskRoute && Boolean(sandboxId?.startsWith("local-")));
  const remote = usePlatformSandboxes(
    taskRoute && Boolean(sandboxId && !sandboxId.startsWith("local-")),
  );
  if (!taskRoute) return children;
  const isLocal = sandboxId?.startsWith("local-");
  const query = isLocal ? local : remote;
  const state = isLocal
    ? local.data?.find((item) => item.id === sandboxId)?.state
    : remote.data?.find((item) => item.id === sandboxId)?.observed_state;
  const access = workspaceAccess({
    ready: isReady,
    sandboxId,
    disconnected: taskEnvironment
      ? isSandboxEnvironmentDisconnected(EnvironmentId.make(taskEnvironment))
      : false,
    loading: query.isPending,
    failed: query.isError,
    state,
  });
  if (access === "wait")
    return (
      <div role="status" className="p-6 text-sm text-muted-foreground">
        正在确认沙箱状态…
      </div>
    );
  return access === "list" ? <Navigate to="/sandboxes" replace /> : children;
}
