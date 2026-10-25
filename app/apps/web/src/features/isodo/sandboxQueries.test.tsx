import { act } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";
vi.mock("./auth", () => ({
  useAuth: () => ({ status: "unauthenticated", user: null }),
  getAuthGeneration: () => 0,
}));
import { useLocalRuntimeSandbox } from "./usePlatformSandbox";
import {
  sandboxQueryClient,
  localSandboxesKey,
  updateLocalSandboxCache,
  platformSandboxRefetchInterval,
} from "./sandboxQueries";
let renderer: ReactTestRenderer;
let current: ReturnType<typeof useLocalRuntimeSandbox>;
const list = vi.fn();
function Harness() {
  current = useLocalRuntimeSandbox("sandbox-a");
  return null;
}
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("window", { isodoDesktop: { listLocalSandboxes: list } });
  list.mockReset().mockResolvedValue({ items: [{ id: "sandbox-a", name: "A", state: "running" }] });
});
afterEach(async () => {
  await act(async () => renderer?.unmount());
  sandboxQueryClient.clear();
  vi.unstubAllGlobals();
});
it("shows the cached sandbox immediately after leaving and re-entering a page", async () => {
  await act(async () => {
    renderer = create(<Harness />);
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
  expect(current.record?.name).toBe("A");
  await act(async () => renderer.unmount());
  list.mockImplementation(() => new Promise(() => {}));
  await act(async () => {
    renderer = create(<Harness />);
  });
  expect(current.record?.name).toBe("A");
  expect(list).toHaveBeenCalledTimes(1);
});
it("coalesces the sidebar and workspace status requests", async () => {
  list.mockImplementation(() => new Promise(() => {}));
  await act(async () => {
    renderer = create(
      <>
        <Harness />
        <Harness />
      </>,
    );
  });
  expect(list).toHaveBeenCalledTimes(1);
});
it("updates cached rows after deletion without waiting for another list request", () => {
  sandboxQueryClient.setQueryData(localSandboxesKey, [{ id: "sandbox-a" }, { id: "sandbox-b" }]);
  updateLocalSandboxCache((items) => items?.filter((item) => item.id !== "sandbox-a") ?? null);
  expect(sandboxQueryClient.getQueryData(localSandboxesKey)).toEqual([{ id: "sandbox-b" }]);
});

it.each([
  ["stopped", "running"],
  ["running", "stopped"],
  ["running", "unknown"],
  ["stopped", "unknown"],
  ["running", "starting"],
  ["running", "requested"],
  ["running", "preparing"],
  ["stopped", "stopping"],
  ["starting", "starting"],
])("polls cloud rows until %s converges from %s", (desired_state, observed_state) => {
  expect(platformSandboxRefetchInterval([{ desired_state, observed_state }])).toBe(2_000);
});

it.each([
  ["running", "running"],
  ["stopped", "stopped"],
  ["running", "failed"],
  ["running", "error"],
  ["stopped", "failed"],
  ["stopped", "error"],
])("stops polling settled or failed cloud rows: %s / %s", (desired_state, observed_state) => {
  expect(platformSandboxRefetchInterval([{ desired_state, observed_state }])).toBe(false);
});

it("continues polling other transitioning rows even when one sandbox failed", () => {
  expect(
    platformSandboxRefetchInterval([
      { desired_state: "running", observed_state: "failed" },
      { desired_state: "stopped", observed_state: "running" },
    ]),
  ).toBe(2_000);
});

it("does not poll when no cloud rows have loaded or the list is empty", () => {
  expect(platformSandboxRefetchInterval(undefined)).toBe(false);
  expect(platformSandboxRefetchInterval([])).toBe(false);
});

it("retains the last successful rows when a background refresh fails", async () => {
  await act(async () => {
    renderer = create(<Harness />);
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
  expect(current.record?.name).toBe("A");
  list.mockRejectedValue(new Error("runtime temporarily unavailable"));
  await act(async () => {
    await sandboxQueryClient.invalidateQueries({ queryKey: localSandboxesKey });
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
  expect(current.record?.name).toBe("A");
});
