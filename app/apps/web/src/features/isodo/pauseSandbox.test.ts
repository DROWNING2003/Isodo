import { afterEach, expect, it, vi } from "vite-plus/test";
vi.mock("../../api/public.generated", () => ({ getSandbox: vi.fn(), stopSandbox: vi.fn() }));
import { getSandbox, stopSandbox } from "../../api/public.generated";
import { pauseSandbox } from "./pauseSandbox";
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
it("waits for the VM to stop without affecting another sandbox", async () => {
  vi.useFakeTimers();
  const stop = vi.fn().mockResolvedValue({});
  const list = vi
    .fn()
    .mockResolvedValueOnce({ items: [{ id: "local-a", state: "running" }] })
    .mockResolvedValueOnce({ items: [{ id: "local-a", state: "stopping" }] })
    .mockResolvedValue({
      items: [
        { id: "local-a", state: "stopped" },
        { id: "local-b", state: "running" },
      ],
    });
  vi.stubGlobal("window", { isodoDesktop: { listLocalSandboxes: list, stopLocalSandbox: stop } });
  const result = pauseSandbox("local-a");
  await vi.runAllTimersAsync();
  await result;
  expect(stop).toHaveBeenCalledExactlyOnceWith("local-a");
  expect(list).toHaveBeenCalledTimes(3);
});
it("does not issue another stop for an already paused sandbox", async () => {
  const stop = vi.fn();
  vi.stubGlobal("window", {
    isodoDesktop: {
      listLocalSandboxes: async () => ({ items: [{ id: "local-a", state: "stopped" }] }),
      stopLocalSandbox: stop,
    },
  });
  await pauseSandbox("local-a");
  expect(stop).not.toHaveBeenCalled();
});

it("pauses a cloud sandbox through its exact lifecycle endpoint", async () => {
  const sandbox = {
    id: "cloud-a",
    name: "Cloud",
    metadata: {},
    desired_state: "running",
    observed_state: "running",
    created_at: "",
    updated_at: "",
  };
  vi.stubGlobal("window", {});
  vi.mocked(getSandbox)
    .mockResolvedValueOnce(sandbox)
    .mockResolvedValueOnce({ ...sandbox, observed_state: "stopped" });
  vi.mocked(stopSandbox).mockResolvedValue({ ...sandbox, desired_state: "stopped" });
  await pauseSandbox("cloud-a");
  expect(getSandbox).toHaveBeenCalledWith("cloud-a");
  expect(stopSandbox).toHaveBeenCalledExactlyOnceWith("cloud-a");
});
