interface Sandbox {
  id: string;
  name?: string;
  observed_state?: string;
}
type Request = (path: string, init?: RequestInit) => Promise<unknown>;

/** Only the desktop Node's ownership index is used; never enumerate all Lima VMs. */
export async function pauseManagedSandboxes(request: Request, timeoutMs = 90_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  const read = async () => {
    const result = (await request("/v1/sandboxes", {
      signal: AbortSignal.timeout(Math.max(1, Math.min(10_000, deadline - Date.now()))),
    })) as { items?: Sandbox[] };
    if (!Array.isArray(result.items)) throw new Error("无法读取本地沙箱状态。");
    return result.items;
  };
  const active = (await read()).filter(
    (item) => item.observed_state !== "stopped" && item.observed_state !== "paused",
  );
  const stopBatch = async (targets: Sandbox[]) => {
    const outcomes = await Promise.allSettled(
      targets.map(async (item) => {
        await request(`/v1/sandboxes/${encodeURIComponent(item.id)}/stop`, {
          method: "POST",
          signal: AbortSignal.timeout(Math.max(1, Math.min(15_000, deadline - Date.now()))),
        });
      }),
    );
    const failures = targets.filter((_item, index) => outcomes[index]?.status === "rejected");
    if (failures.length)
      throw new Error(
        `以下沙箱未能暂停：${failures.map((item) => item.name || item.id).join("、")}`,
      );
  };
  await stopBatch(active);
  while (active.length) {
    const items = await read();
    const added = items.filter(
      (item) =>
        item.observed_state !== "stopped" &&
        item.observed_state !== "paused" &&
        !active.some((target) => target.id === item.id),
    );
    if (added.length) {
      active.push(...added);
      await stopBatch(added);
    }
    const remaining = active.filter((target) => {
      const item = items.find((candidate) => candidate.id === target.id);
      // Missing ownership is not proof that the VM stopped.
      return !item || (item.observed_state !== "stopped" && item.observed_state !== "paused");
    });
    if (!remaining.length) return;
    if (Date.now() >= deadline)
      throw new Error(`等待暂停超时：${remaining.map((item) => item.name || item.id).join("、")}`);
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
}

export function createManagedQuit(dependencies: {
  pause: () => Promise<void>;
  quit: () => void;
  onFailure: (error: unknown) => Promise<"retry" | "cancel" | "force">;
}) {
  let allowed = false;
  let pending: Promise<void> | null = null;
  return (event: { preventDefault: () => void }): Promise<void> | undefined => {
    if (allowed) {
      allowed = false;
      return;
    }
    event.preventDefault();
    if (pending) return pending;
    pending = (async () => {
      for (;;) {
        try {
          await dependencies.pause();
        } catch (error) {
          const action = await dependencies.onFailure(error);
          if (action === "retry") continue;
          if (action === "cancel") return;
        }
        allowed = true;
        dependencies.quit();
        return;
      }
    })().finally(() => {
      pending = null;
    });
    return pending;
  };
}
