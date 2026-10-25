import { afterEach, expect, it, vi } from "vite-plus/test";
import { EnvironmentId } from "@t3tools/contracts";
afterEach(() => vi.unstubAllGlobals());
it("retains disconnect intent after a module reload while keeping the sandbox pairing", async () => {
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
  });
  const id = EnvironmentId.make("env-persisted");
  const before = await import("./sandboxEnvironmentMap");
  before.rememberSandboxEnvironment(id, "sandbox");
  before.setSandboxEnvironmentDisconnected(id, true);
  vi.resetModules();
  const after = await import("./sandboxEnvironmentMap");
  expect(after.isSandboxEnvironmentDisconnected(id)).toBe(true);
  expect(after.sandboxIdForEnvironment(id)).toBe("sandbox");
  after.setSandboxEnvironmentDisconnected(id, false);
  expect(after.isSandboxEnvironmentDisconnected(id)).toBe(false);
});
