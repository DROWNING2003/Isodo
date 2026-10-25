import { afterEach, expect, it, vi } from "vite-plus/test";
const fixture = vi.hoisted(() => ({ get: vi.fn(), stop: vi.fn(), select: vi.fn() }));
vi.mock("./sandboxTransports", () => ({ sandboxClientFor: fixture.select }));
import { pauseSandbox } from "./pauseSandbox";
const snapshot = (status: "running" | "stopped", revision = "1", actionStatus: "running" | "succeeded" = "succeeded") => ({
  id: "local-a", name: "A", desired_state: status, observed_state: status,
  revision, last_action: { revision, type: "stop", status: actionStatus, updated_at: "2026-10-25T00:00:00Z" },
});
afterEach(() => vi.resetAllMocks());
it.each(["local-a", "cloud-a"])("uses the owning transport and waits for the exact stop action: %s", async (id) => {
  fixture.select.mockReturnValue({ get: fixture.get, stop: fixture.stop });
  fixture.get.mockResolvedValueOnce(snapshot("running")).mockResolvedValueOnce(snapshot("stopped", "2"));
  fixture.stop.mockResolvedValue({ ...snapshot("stopped", "2", "running"), observed_state: "running" });
  await pauseSandbox(id);
  expect(fixture.select).toHaveBeenCalledWith(id.startsWith("local-") ? "local" : "cloud");
  expect(fixture.stop).toHaveBeenCalledExactlyOnceWith(id);
  expect(fixture.get).toHaveBeenCalledTimes(2);
});
it("does not stop an already stopped sandbox", async () => {
  fixture.select.mockReturnValue({ get: fixture.get, stop: fixture.stop });
  fixture.get.mockResolvedValue(snapshot("stopped"));
  await pauseSandbox("local-a");
  expect(fixture.stop).not.toHaveBeenCalled();
});
it("does not mistake a newer running revision for stop success", async () => {
  fixture.select.mockReturnValue({ get: fixture.get, stop: fixture.stop });
  fixture.get.mockResolvedValueOnce(snapshot("running")).mockResolvedValueOnce(snapshot("running", "3"));
  fixture.stop.mockResolvedValue(snapshot("stopped", "2", "running"));
  await expect(pauseSandbox("local-a")).rejects.toMatchObject({ code: "ACTION_SUPERSEDED" });
});
