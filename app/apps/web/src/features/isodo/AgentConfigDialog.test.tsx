import { act, type ReactNode } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";
vi.mock("../../components/ui/dialog", () => {
  return Object.fromEntries(
    [
      "Dialog",
      "DialogDescription",
      "DialogFooter",
      "DialogHeader",
      "DialogPanel",
      "DialogPopup",
      "DialogTitle",
    ].map((name) => [
      name,
      ({ children }: { children?: ReactNode }) => <div data-test-slot={name}>{children}</div>,
    ]),
  );
});
vi.mock("../../components/ui/button", () => ({
  Button: (props: React.ComponentProps<"button">) => <button {...props} />,
}));
vi.mock("../../components/ui/input", () => ({
  Input: (props: React.ComponentProps<"input">) => <input {...props} />,
}));
vi.mock("../../components/ui/textarea", () => ({
  Textarea: (props: React.ComponentProps<"textarea">) => <textarea {...props} />,
}));
vi.mock("./injectionRules", () => ({ fetchInjectionMaterials: vi.fn() }));
import type { InjectionRule } from "../../api/public.generated";
import { AgentConfigDialog } from "./AgentConfigDialog";
import { fetchInjectionMaterials } from "./injectionRules";
let renderer: ReactTestRenderer;
const saved = vi.fn();
const createConnection = vi.fn();
const close = vi.fn();
const fetchModels = vi.fn();
const rule: InjectionRule = {
  id: "relay-1",
  name: "Relay",
  type: "openai",
  base_url: "https://relay.example/v1",
  secret_hint: "••••test",
  secret_set: true,
  created_at: "",
  updated_at: "",
};
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchModels);
  saved.mockReset();
  close.mockReset();
  createConnection.mockReset().mockResolvedValue(rule);
  fetchModels
    .mockReset()
    .mockResolvedValue({ ok: true, json: async () => ({ data: [{ id: "relay-model" }] }) });
});
afterEach(async () => {
  await act(async () => renderer?.unmount());
  vi.unstubAllGlobals();
});
async function render(connections: InjectionRule[] = []) {
  await act(async () => {
    renderer = create(
      <AgentConfigDialog
        open
        onOpenChange={close}
        connections={connections}
        onSave={saved}
        onCreateConnection={createConnection}
      />,
    );
  });
}
async function fill(id: string, value: string) {
  await act(async () => renderer.root.findByProps({ id }).props.onChange({ target: { value } }));
}
async function click(label: string) {
  const button = renderer.root
    .findAllByType("button")
    .find((node) => node.children.join("") === label);
  expect(button, label).toBeDefined();
  await act(async () => button!.props.onClick());
}
it("queries the unsaved endpoint and key, then creates the connection and saves its reference", async () => {
  await render();
  await fill("agent-config-name", "My Codex");
  await fill("agent-endpoint", "https://relay.example/v1/");
  await fill("agent-api-key", "draft-secret");
  await click("获取模型");
  const [url, options] = fetchModels.mock.calls[0]!;
  expect(url).toBe("https://relay.example/v1/models");
  expect(new Headers(options.headers).get("Authorization")).toBe("Bearer draft-secret");
  expect(fetchInjectionMaterials).not.toHaveBeenCalled();
  expect(createConnection).not.toHaveBeenCalled();
  await act(async () =>
    renderer.root.findByProps({ type: "checkbox" }).props.onChange({ target: { checked: true } }),
  );
  await click("保存 Agent 配置");
  expect(createConnection).toHaveBeenCalledWith(
    expect.objectContaining({
      base_url: "https://relay.example/v1",
      secret: "draft-secret",
      type: "openai",
    }),
  );
  expect(saved).toHaveBeenCalledWith(
    expect.objectContaining({ connectionId: "relay-1", defaultModel: "relay-model" }),
  );
  expect(JSON.stringify(saved.mock.calls)).not.toContain("draft-secret");
});
it("preserves inputs and supports manual models and retry after errors", async () => {
  fetchModels.mockRejectedValue(new Error("offline"));
  createConnection.mockRejectedValue(new Error("保存连接失败"));
  await render();
  await fill("agent-config-name", "Retry");
  await fill("agent-endpoint", "https://relay.example/v1");
  await fill("agent-api-key", "draft-secret");
  await click("获取模型");
  await fill("agent-manual-model", "manual-model");
  await click("添加");
  await click("保存 Agent 配置");
  expect(saved).not.toHaveBeenCalled();
  expect(close).not.toHaveBeenCalled();
  expect(renderer.root.findByProps({ id: "agent-endpoint" }).props.value).toBe(
    "https://relay.example/v1",
  );
  expect(JSON.stringify(renderer.toJSON())).toContain("保存连接失败");
  createConnection.mockResolvedValue(rule);
  await click("保存 Agent 配置");
  expect(saved).toHaveBeenCalledWith(expect.objectContaining({ defaultModel: "manual-model" }));
});

it("queries Anthropic with its protocol URL and authentication", async () => {
  await render();
  await fill("agent-kind", "claude");
  await fill("agent-endpoint", "https://relay.example/anthropic/");
  await fill("agent-api-key", "anthropic-key");
  await click("获取模型");
  const [url, options] = fetchModels.mock.calls[0]!;
  expect(url).toBe("https://relay.example/anthropic/v1/models?limit=1000");
  const headers = new Headers(options.headers);
  expect(headers.get("x-api-key")).toBe("anthropic-key");
  expect(headers.has("Authorization")).toBe(false);
});

it("can fill from a saved credential without creating a duplicate", async () => {
  await render([rule]);
  await fill("agent-config-name", "Reuse");
  await click("从已保存凭证填入");
  await fill("agent-connection", rule.id);
  await fill("agent-manual-model", "relay-model");
  await click("添加");
  await click("保存 Agent 配置");
  expect(createConnection).not.toHaveBeenCalled();
  expect(saved).toHaveBeenCalledWith(expect.objectContaining({ connectionId: rule.id }));
});

it("retries local configuration saving without duplicating the credential or exposing the key", async () => {
  saved.mockImplementationOnce(() => {
    throw new Error("storage full");
  });
  await render();
  await fill("agent-config-name", "Retry local save");
  await fill("agent-endpoint", "https://relay.example/v1");
  await fill("agent-api-key", "draft-secret");
  await click("高级设置");
  await fill("agent-config-content", '{"customAuth":"draft-secret"}');
  await fill("agent-manual-model", "relay-model");
  await click("添加");
  await click("保存 Agent 配置");
  expect(close).not.toHaveBeenCalled();
  await click("保存 Agent 配置");
  expect(createConnection).toHaveBeenCalledTimes(1);
  expect(saved).toHaveBeenCalledTimes(2);
  expect(JSON.stringify(saved.mock.calls)).not.toContain("draft-secret");
  expect(close).toHaveBeenCalledWith(false);
});

it("keeps save errors beside the footer buttons, outside the scrolling fields", async () => {
  await render();
  await click("保存 Agent 配置");
  const footer = renderer.root.findByProps({ "data-test-slot": "DialogFooter" });
  expect(footer.findByProps({ role: "alert" }).children.join("")).toContain("请填写配置名称");
});
