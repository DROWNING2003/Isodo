import { act, type ReactNode } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vite-plus/test";
const fixture = vi.hoisted(() => ({
  apply: vi.fn().mockResolvedValue(undefined),
  save: vi.fn().mockResolvedValue(undefined),
  rules: [{ kind: "persistent", persistent_id: "credential", key: "credential" }],
}));
vi.mock("./CloudPages", () => ({
  CloudPage: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));
vi.mock("./usePlatformSandbox", () => ({
  useLocalRuntimeSandbox: () => ({ record: { id: "sandbox", status: "running" } }),
  usePlatformSandbox: () => ({}),
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
  const save = renderer.root.findByProps({ "aria-label": "保存沙箱凭证配置" });
  expect(save.props.size).toBe("icon");
  expect(save.props.disabled).toBe(false);
  await act(async () =>
    renderer.root.findByProps({ "aria-label": "添加临时 Agent 配置" }).props.onClick(),
  );
  const submit = renderer.root
    .findAllByType("button")
    .find((button) => button.children.join("") === "提交临时 Agent")!;
  await act(async () => submit.props.onClick());
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
