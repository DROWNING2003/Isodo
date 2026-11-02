import { act, type ReactNode } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";
const fixture = vi.hoisted(() => ({
  local: true,
  trace: [] as string[],
  revoke: vi.fn(async () => 1),
  add: vi.fn(async () => {}),
  refresh: vi.fn(async () => {}),
  reset: vi.fn(),
  apply: vi.fn().mockResolvedValue(undefined),
  save: vi.fn().mockResolvedValue(undefined),
  rules: [{ kind: "persistent", persistent_id: "credential", key: "credential" }],
}));
vi.mock("./MCPMarketPage", () => ({
  SandboxMCPBindings: () => <div>MCP</div>,
  useSandboxMCPSelection: () => ({
    revokeRemoved: fixture.revoke,
    applyAdded: fixture.add,
    refetch: fixture.refresh,
    resetDraft: fixture.reset,
    loading: false,
    failed: false,
  }),
}));
vi.mock("./skillMarket", () => ({
  useSandboxSkillSelection: () => ({
    loading: false,
    failed: false,
    changed: false,
    apply: async () => {
      fixture.trace.push("skills");
    },
  }),
}));
vi.mock("./SkillMarketPage", () => ({ SandboxSkillSelection: () => <div>Skill</div> }));
vi.mock("./mcpMarket", () => ({ restoreBoundMCP: vi.fn(async () => {}) }));
vi.mock("./CloudPages", () => ({
  CloudPage: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));
vi.mock("./usePlatformSandbox", () => ({
  useLocalRuntimeSandbox: () => ({
    record: fixture.local ? { id: "sandbox", status: "running" } : undefined,
  }),
  usePlatformSandbox: () => ({
    record: fixture.local ? undefined : { id: "sandbox", status: "running" },
  }),
}));
vi.mock("./credentialDocuments", () => ({
  useServerAgentConfigs: () => ({ data: [], isSuccess: true }),
  useServerSandboxCredentials: () => ({ data: {}, isSuccess: true }),
}));
vi.mock("./injectionRules", () => ({
  useInjectionRules: () => ({
    data: [{ id: "credential", name: "Relay", type: "openai", base_url: "https://relay.example" }],
    isLoading: false,
  }),
  useSandboxInjections: () => ({ data: { rules: fixture.rules } }),
  useSandboxInjectionWriter: () => vi.fn(),
  ruleSecretPreview: vi.fn(),
  ruleTarget: vi.fn(),
}));
vi.mock("./applySandboxCredentials", () => ({
  applySandboxCredentials: fixture.apply,
  saveSandboxCredentialSelection: fixture.save,
}));
vi.mock("./TemporaryAgentDialog", () => ({
  TemporaryAgentDialog: ({ onSave }: { onSave: (value: unknown) => Promise<void> }) => (
    <button
      onClick={() =>
        onSave({
          config: {
            id: "temporary-agent",
            name: "Temporary Codex",
            agent: "codex",
            defaultModel: "relay-model",
          },
          connection: {},
          rule: { key: "temporary-key" },
        })
      }
    >
      提交临时 Agent
    </button>
  ),
}));
vi.mock("../../components/ui/button", () => ({
  Button: ({ children, ...props }: React.ComponentProps<"button">) => (
    <button {...props}>{children}</button>
  ),
}));
vi.mock("../../components/ui/dialog", () => {
  const Slot = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    Dialog: () => null,
    DialogDescription: Slot,
    DialogHeader: Slot,
    DialogPanel: Slot,
    DialogPopup: Slot,
    DialogTitle: Slot,
  };
});
import { SandboxConnectionsPage } from "./SandboxConnectionsPage";
let renderer: ReactTestRenderer;
beforeEach(() => {
  fixture.local = true;
  fixture.trace = [];
  vi.clearAllMocks();
  fixture.revoke.mockImplementation(async () => {
    fixture.trace.push("revoke");
    return 1;
  });
  fixture.apply.mockImplementation(async () => {
    fixture.trace.push("credentials");
  });
  fixture.add.mockImplementation(async () => {
    fixture.trace.push("bind");
  });
  fixture.save.mockImplementation(async () => {
    fixture.trace.push("preferences");
  });
});
afterEach(async () => {
  await act(async () => renderer?.unmount());
  vi.unstubAllGlobals();
});
it("offers an icon save action and saves temporary Agent only in the current sandbox document", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("window", { isodoDesktop: {} });
  await act(async () => {
    renderer = create(<SandboxConnectionsPage sandboxId="sandbox" />);
  });
  const checkbox = renderer.root.findByType("input");
  expect(checkbox.props.disabled).toBe(false);
  expect(checkbox.props.checked).toBe(true);
  await act(async () => checkbox.props.onChange({ target: { checked: false } }));
  expect(renderer.root.findByType("input").props.checked).toBe(false);
  const save = renderer.root.findByProps({ "aria-label": "保存沙箱安全连接" });
  expect(save.props.size).toBe("icon");
  expect(save.props.disabled).toBe(false);
  await act(async () =>
    renderer.root.findByProps({ "aria-label": "添加临时 Agent 配置" }).props.onClick(),
  );
  const submit = renderer.root
    .findAllByType("button")
    .find((button) => button.children.join("") === "提交临时 Agent")!;
  await act(async () => submit.props.onClick());
  expect(fixture.apply).not.toHaveBeenCalled();
  await act(async () => save.props.onClick());
  expect(fixture.apply.mock.calls[0]?.[0]).toMatchObject({
    sandboxId: "sandbox",
    temporaryAgents: [{ config: { id: "temporary-agent" } }],
  });
  expect(fixture.save.mock.calls[0]).toMatchObject([
    "sandbox",
    { agentIds: [], credentialIds: [] },
    [],
    { temporaryAgents: [{ config: { id: "temporary-agent" } }] },
  ]);
});

it("uses the page save for MCP and credentials, revoking before adding access", async () => {
  fixture.local = false;
  vi.stubGlobal("window", {});
  await act(async () => {
    renderer = create(<SandboxConnectionsPage sandboxId="sandbox" />);
  });
  const save = renderer.root.findByProps({ "aria-label": "保存沙箱安全连接" });
  await act(async () => save.props.onClick());
  expect(fixture.trace).toEqual(["revoke", "credentials", "bind", "skills", "preferences"]);
  expect(fixture.refresh).toHaveBeenCalledOnce();
  expect(fixture.reset).toHaveBeenCalledOnce();
});
it("retains revocation and reports partial success when credential application fails", async () => {
  fixture.local = false;
  vi.stubGlobal("window", {});
  fixture.apply.mockRejectedValueOnce(new Error("Guest offline"));
  await act(async () => {
    renderer = create(<SandboxConnectionsPage sandboxId="sandbox" />);
  });
  await act(async () =>
    renderer.root.findByProps({ "aria-label": "保存沙箱安全连接" }).props.onClick(),
  );
  expect(fixture.revoke).toHaveBeenCalledOnce();
  expect(fixture.add).not.toHaveBeenCalled();
  expect(fixture.reset).not.toHaveBeenCalled();
  expect(JSON.stringify(renderer.toJSON())).toContain(
    "MCP 撤销已完成；后续保存失败：Guest offline",
  );
});
