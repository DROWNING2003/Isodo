import { afterEach, expect, it, vi } from "vite-plus/test";
import { createManagedQuit, pauseManagedSandboxes } from "./shutdown.js";
afterEach(() => vi.useRealTimers());
it("only stops owned active sandboxes and waits for observed stopped", async () => {
  vi.useFakeTimers();
  let reads = 0;
  const request = vi.fn(async (path: string, _init?: RequestInit) => {
    if (path === "/v1/sandboxes") {
      reads++;
      return {
        items: [
          { id: "local-a", name: "A", observed_state: reads > 2 ? "stopped" : "running" },
          { id: "local-b", observed_state: "stopped" },
        ],
      };
    }
    return {};
  });
  const result = pauseManagedSandboxes(request);
  await vi.runAllTimersAsync();
  await result;
  expect(
    request.mock.calls.filter(([path]) => path.endsWith("/stop")).map(([path]) => path),
  ).toEqual(["/v1/sandboxes/local-a/stop"]);
  expect(reads).toBe(3);
});
it("does not report success if an owned sandbox disappears before stopping is confirmed", async () => {
  vi.useFakeTimers();
  let reads = 0;
  const request = vi.fn(async (path: string) =>
    path === "/v1/sandboxes"
      ? { items: ++reads === 1 ? [{ id: "local-a", observed_state: "running" }] : [] }
      : {},
  );
  const result = expect(pauseManagedSandboxes(request, 20)).rejects.toThrow("等待暂停超时");
  await vi.runAllTimersAsync();
  await result;
});
it("finishes without stop requests when there are no owned sandboxes", async () => {
  const request = vi.fn().mockResolvedValue({ items: [] });
  await pauseManagedSandboxes(request);
  expect(request).toHaveBeenCalledTimes(1);
});
it("reports failed stops while still attempting the other owned sandboxes", async () => {
  const request = vi.fn(async (path: string) => {
    if (path === "/v1/sandboxes")
      return {
        items: [
          { id: "a", name: "Failed VM", observed_state: "running" },
          { id: "b", observed_state: "running" },
        ],
      };
    if (path.includes("/a/")) throw new Error("offline");
    return {};
  });
  await expect(pauseManagedSandboxes(request)).rejects.toThrow("Failed VM");
  expect(request).toHaveBeenCalledWith(
    "/v1/sandboxes/b/stop",
    expect.objectContaining({ method: "POST" }),
  );
});
it("prevents duplicate quits and quits only after pause confirmation", async () => {
  const order: string[] = [];
  let release!: () => void;
  const pause = vi.fn(
    () =>
      new Promise<void>((resolve) => {
        release = resolve;
        order.push("pause");
      }),
  );
  const quit = createManagedQuit({
    pause,
    quit: () => {
      order.push("quit");
    },
    onFailure: async () => "cancel",
  });
  const event = { preventDefault: vi.fn() };
  const first = quit(event);
  expect(quit(event)).toBe(first);
  expect(order).toEqual(["pause"]);
  release();
  await first;
  expect(order).toEqual(["pause", "quit"]);
  const finalEvent = { preventDefault: vi.fn() };
  expect(quit(finalEvent)).toBeUndefined();
  expect(finalEvent.preventDefault).not.toHaveBeenCalled();
});
it("cancels exit on a failed pause and allows retrying later", async () => {
  const pause = vi.fn().mockRejectedValueOnce(new Error("failed")).mockResolvedValue(undefined);
  const exit = vi.fn();
  const quit = createManagedQuit({ pause, quit: exit, onFailure: async () => "cancel" });
  await quit({ preventDefault: vi.fn() });
  expect(exit).not.toHaveBeenCalled();
  await quit({ preventDefault: vi.fn() });
  expect(exit).toHaveBeenCalledOnce();
});
it("only skips the failed pause after an explicit force-exit choice", async () => {
  const exit = vi.fn();
  const choice = vi.fn(async () => "force" as const);
  const quit = createManagedQuit({
    pause: async () => {
      throw new Error("unreachable");
    },
    quit: exit,
    onFailure: choice,
  });
  await quit({ preventDefault: vi.fn() });
  expect(choice).toHaveBeenCalledOnce();
  expect(exit).toHaveBeenCalledOnce();
});

it("rechecks pause if window closing was canceled after quit was allowed", async () => {
  const pause = vi.fn().mockResolvedValue(undefined);
  const quit = createManagedQuit({ pause, quit: vi.fn(), onFailure: async () => "cancel" });
  await quit({ preventDefault: vi.fn() });
  const accepted = { preventDefault: vi.fn() };
  quit(accepted);
  expect(accepted.preventDefault).not.toHaveBeenCalled();
  await quit({ preventDefault: vi.fn() });
  expect(pause).toHaveBeenCalledTimes(2);
});

it("also pauses owned sandboxes created while shutdown is waiting", async () => {
  vi.useFakeTimers();
  let reads = 0;
  const request = vi.fn(async (path: string, _init?: RequestInit) => {
    if (path !== "/v1/sandboxes") return {};
    reads++;
    return {
      items:
        reads === 1
          ? [{ id: "a", observed_state: "running" }]
          : [
              { id: "a", observed_state: "stopped" },
              { id: "b", observed_state: reads === 2 ? "running" : "stopped" },
            ],
    };
  });
  const result = pauseManagedSandboxes(request);
  await vi.runAllTimersAsync();
  await result;
  expect(request).toHaveBeenCalledWith(
    "/v1/sandboxes/b/stop",
    expect.objectContaining({ method: "POST" }),
  );
});
