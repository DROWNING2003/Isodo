import { expect, it, vi, afterEach } from "vite-plus/test";
import { localSandboxClient, sandboxClientFor, remoteSandboxClient } from "./sandboxTransports";
afterEach(() => vi.unstubAllGlobals());
it("uses IPC for the same canonical list contract without exposing credentials", async () => {
  const request = vi.fn().mockResolvedValue({ items: [], total: 0, limit: 100, offset: 0 });
  vi.stubGlobal("window", { isodoDesktop: { sandboxRequest: request } });
  expect((await localSandboxClient.list()).items).toEqual([]);
  expect(request).toHaveBeenCalledWith(
    "/v1/sandboxes?limit=100&offset=0",
    "GET",
    undefined,
    undefined,
  );
  expect(sandboxClientFor("local")).toBe(localSandboxClient);
  expect(sandboxClientFor("cloud")).toBe(remoteSandboxClient);
});
it("does not issue IPC when the caller already cancelled", async () => {
  const request = vi.fn();
  vi.stubGlobal("window", { isodoDesktop: { sandboxRequest: request } });
  await expect(
    localSandboxClient.get("local-a", { signal: AbortSignal.abort() }),
  ).rejects.toThrow();
  expect(request).not.toHaveBeenCalled();
});

it("forwards action idempotency keys through IPC", async () => {
  const request = vi
    .fn()
    .mockResolvedValue({
      id: "local-a",
      name: "A",
      desired_state: "running",
      observed_state: "starting",
    });
  vi.stubGlobal("window", { isodoDesktop: { sandboxRequest: request } });
  await localSandboxClient.start("local-a", { idempotencyKey: "start-retry-1" });
  expect(request).toHaveBeenCalledWith(
    "/v1/sandboxes/local-a/start",
    "POST",
    undefined,
    "start-retry-1",
  );
});
