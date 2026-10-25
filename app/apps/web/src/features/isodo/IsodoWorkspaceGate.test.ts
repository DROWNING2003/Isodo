import { expect, it } from "vite-plus/test";
import { workspaceAccess } from "./IsodoWorkspaceGate";
const running = {
  ready: true,
  sandboxId: "local-test",
  disconnected: false,
  loading: false,
  failed: false,
  state: "running",
};
it("waits for sandbox and catalog hydration before restoring a task", () => {
  expect(workspaceAccess({ ...running, ready: false })).toBe("wait");
  expect(workspaceAccess({ ...running, loading: true })).toBe("wait");
  expect(workspaceAccess(running)).toBe("allow");
});
it("returns stale, stopped or explicitly disconnected tasks to the sandbox list", () => {
  for (const change of [
    { sandboxId: null },
    { disconnected: true },
    { state: "stopped" },
    { state: undefined },
    { failed: true },
  ])
    expect(workspaceAccess({ ...running, ...change })).toBe("list");
});
