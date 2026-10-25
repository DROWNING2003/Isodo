import { beforeEach, expect, it, vi } from "vite-plus/test";
const fixture = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("../../api/transport", () => ({ isodoFetch: fixture.request }));
vi.mock("./auth", () => ({
  useAuth: () => ({ status: "authenticated" }),
  getAuthGeneration: () => 0,
}));
import { fetchServerAgentConfigs } from "./credentialDocuments";
const agent = {
  id: "agent-1",
  name: "Work",
  agent: "codex",
  connectionId: "rule-1",
  modelNames: ["relay-model"],
  defaultModel: "relay-model",
  filePath: "~/.codex/config.toml",
  content: "",
  updatedAt: "",
};
let storage: Map<string, string>;
beforeEach(() => {
  storage = new Map();
  fixture.request.mockReset();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    removeItem: (key: string) => storage.delete(key),
  });
  vi.stubGlobal("window", { dispatchEvent: vi.fn() });
});
it("loads configurations from the server with no local record", async () => {
  fixture.request.mockResolvedValue({ items: [{ id: agent.id, value: agent }] });
  expect(await fetchServerAgentConfigs()).toEqual([agent]);
  expect(fixture.request).toHaveBeenCalledWith("/v1/credential-documents/agents");
});
it("removes the migration copy only after the server confirms persistence", async () => {
  storage.set("isodo.agent-configs.v1", JSON.stringify([agent]));
  fixture.request.mockResolvedValueOnce({ items: [] }).mockRejectedValueOnce(new Error("offline"));
  await expect(fetchServerAgentConfigs()).rejects.toThrow("offline");
  expect(storage.has("isodo.agent-configs.v1")).toBe(true);
  fixture.request.mockResolvedValueOnce({ items: [] }).mockResolvedValueOnce({});
  expect(await fetchServerAgentConfigs()).toEqual([agent]);
  expect(storage.has("isodo.agent-configs.v1")).toBe(false);
});
it("does not replace an authoritative server configuration with stale local data", async () => {
  storage.set("isodo.agent-configs.v1", JSON.stringify([agent]));
  const current = { ...agent, defaultModel: "new-model" };
  fixture.request.mockResolvedValue({ items: [{ id: agent.id, value: current }] });
  expect(await fetchServerAgentConfigs()).toEqual([current]);
  expect(fixture.request).toHaveBeenCalledTimes(1);
});
