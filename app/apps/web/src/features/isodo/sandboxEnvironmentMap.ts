import { useSyncExternalStore } from "react";
import type { EnvironmentId } from "@t3tools/contracts";

const STORAGE_KEY = "isodo.sandbox-environments";

type SandboxMap = Record<string, string>;

function read(): SandboxMap {
  if (typeof localStorage === "undefined") return {};
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "{}");
    return typeof parsed === "object" && parsed !== null ? (parsed as SandboxMap) : {};
  } catch {
    return {};
  }
}

/**
 * Remembers which sandbox produced a paired environment. The environment's
 * display URL is not a reliable identifier (it can be rewritten by the
 * transport), so the mapping is recorded when pairing succeeds.
 */
export function rememberSandboxEnvironment(environmentId: EnvironmentId, sandboxId: string): void {
  const map = read();
  map[String(environmentId)] = sandboxId;
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(map));
  } catch {
    // Storage is best effort; the UI still works without the mapping.
  }
}

export function sandboxIdForEnvironment(environmentId: EnvironmentId | null): string | null {
  if (environmentId === null) return null;
  return read()[String(environmentId)] ?? null;
}

/** Resolve only registered environments, so a stale mapping cannot reconnect by itself. */
export function environmentIdsForSandbox(
  sandboxId: string,
  registeredIds: ReadonlyArray<EnvironmentId>,
): ReadonlyArray<EnvironmentId> {
  const map = read();
  return registeredIds.filter((id) => map[id] === sandboxId);
}

export function environmentIdForSandbox(
  sandboxId: string,
  registeredIds: ReadonlyArray<EnvironmentId>,
): EnvironmentId | null {
  return environmentIdsForSandbox(sandboxId, registeredIds).at(-1) ?? null;
}

type WorkspaceTarget =
  | { to: "/workspace"; replace: true }
  | { to: "/draft/$draftId"; params: { draftId: string }; replace: true }
  | {
      to: "/$environmentId/$threadId";
      params: { environmentId: string; threadId: string };
      replace: true;
    };
const workspaceTargets = new Map<EnvironmentId, WorkspaceTarget>();

export function rememberSandboxWorkspace(
  environmentId: EnvironmentId,
  target: WorkspaceTarget,
): void {
  if (sandboxIdForEnvironment(environmentId) !== null) workspaceTargets.set(environmentId, target);
}

export function workspaceTargetForEnvironment(environmentId: EnvironmentId): WorkspaceTarget {
  return workspaceTargets.get(environmentId) ?? { to: "/workspace", replace: true };
}

// Preserve explicit disconnect intent across page refreshes and app restarts.
const DISCONNECTED_STORAGE_KEY = "isodo.disconnected-environments";
let disconnected = new Set<string>();
let disconnectStorageUnavailable = false;
function readDisconnected(): Set<string> {
  try {
    if (disconnectStorageUnavailable || typeof localStorage === "undefined")
      return new Set(disconnected);
    const value: unknown = JSON.parse(localStorage.getItem(DISCONNECTED_STORAGE_KEY) ?? "[]");
    return new Set(
      Array.isArray(value) ? value.filter((id): id is string => typeof id === "string") : [],
    );
  } catch {
    return new Set(disconnected);
  }
}
const connectionListeners = new Set<() => void>();
let connectionVersion = 0;
export function setSandboxEnvironmentDisconnected(
  environmentId: EnvironmentId,
  value: boolean,
): void {
  const next = readDisconnected();
  if (value) {
    next.add(environmentId);
    disconnected.add(environmentId);
  } else {
    next.delete(environmentId);
    disconnected.delete(environmentId);
  }
  disconnected = next;
  try {
    localStorage.setItem(DISCONNECTED_STORAGE_KEY, JSON.stringify([...next]));
  } catch {
    disconnectStorageUnavailable = true;
  }
  connectionVersion += 1;
  for (const listener of connectionListeners) listener();
}
export function isSandboxEnvironmentDisconnected(environmentId: EnvironmentId): boolean {
  return readDisconnected().has(environmentId);
}
export function useSandboxConnectionVersion(): number {
  return useSyncExternalStore(
    (listener) => {
      connectionListeners.add(listener);
      return () => {
        connectionListeners.delete(listener);
      };
    },
    () => connectionVersion,
  );
}
