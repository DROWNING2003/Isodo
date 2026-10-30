const TRACE_PREFIX = "isodo.workspace-entry.";
let activeWorkspaceEntryTrace: WorkspaceEntryTrace | null = null;

export interface WorkspaceEntryTrace {
  readonly traceId: string;
  readonly sandboxId: string;
  readonly clickedAt: number;
}

function storageKey(sandboxId: string): string {
  return `${TRACE_PREFIX}${sandboxId}`;
}

function createTraceId(): string {
  return Array.from(globalThis.crypto.getRandomValues(new Uint8Array(16)), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
}

export function beginWorkspaceEntry(sandboxId: string): WorkspaceEntryTrace {
  const trace: WorkspaceEntryTrace = {
    traceId: createTraceId(),
    sandboxId,
    clickedAt: Date.now(),
  };
  try {
    globalThis.sessionStorage.setItem(storageKey(sandboxId), JSON.stringify(trace));
  } catch {
    // Diagnostics must never block workspace navigation.
  }
  activeWorkspaceEntryTrace = trace;
  return trace;
}

export function takeWorkspaceEntry(sandboxId: string): WorkspaceEntryTrace {
  try {
    const key = storageKey(sandboxId);
    const value = globalThis.sessionStorage.getItem(key);
    if (value) {
      const parsed = JSON.parse(value) as Partial<WorkspaceEntryTrace>;
      if (
        parsed.sandboxId === sandboxId &&
        typeof parsed.traceId === "string" &&
        typeof parsed.clickedAt === "number"
      ) {
        activeWorkspaceEntryTrace = parsed as WorkspaceEntryTrace;
        return activeWorkspaceEntryTrace;
      }
    }
  } catch {
    // Fall back to a new trace if storage is unavailable or malformed.
  }
  const trace = { traceId: createTraceId(), sandboxId, clickedAt: Date.now() };
  activeWorkspaceEntryTrace = trace;
  try {
    globalThis.sessionStorage.setItem(storageKey(sandboxId), JSON.stringify(trace));
  } catch {
    // Diagnostics must never block workspace navigation.
  }
  return trace;
}

export function getActiveWorkspaceEntryTrace(): WorkspaceEntryTrace | null {
  return activeWorkspaceEntryTrace;
}

export function clearActiveWorkspaceEntryTrace(traceId: string): void {
  if (activeWorkspaceEntryTrace?.traceId !== traceId) return;
  try {
    globalThis.sessionStorage.removeItem(storageKey(activeWorkspaceEntryTrace.sandboxId));
  } catch {
    // Ignore unavailable storage.
  }
  activeWorkspaceEntryTrace = null;
}

export function logWorkspaceEntry(
  trace: WorkspaceEntryTrace,
  stage: string,
  durationMs: number,
  details: Record<string, string | number | boolean> = {},
): void {
  console.info("[isodo-workspace-entry]", {
    trace_id: trace.traceId,
    sandbox_id: trace.sandboxId,
    stage,
    duration_ms: Math.max(0, Math.round(durationMs)),
    ...details,
  });
}

export async function measureWorkspaceEntry<T>(
  trace: WorkspaceEntryTrace,
  stage: string,
  operation: () => Promise<T>,
): Promise<T> {
  const startedAt = performance.now();
  try {
    const result = await operation();
    logWorkspaceEntry(trace, stage, performance.now() - startedAt, { outcome: "success" });
    return result;
  } catch (error) {
    logWorkspaceEntry(trace, stage, performance.now() - startedAt, { outcome: "failure" });
    throw error;
  }
}
