import { act, type ReactNode } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";
const fixture = vi.hoisted(() => ({
  apply: vi.fn(),
  create: vi.fn(),
  navigate: vi.fn(),
  pairing: vi.fn(),
}));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => fixture.navigate }));
vi.mock("../../env", () => ({ isElectron: true }));
vi.mock("./auth", () => ({ useAuth: () => ({ status: "authenticated" }) }));
vi.mock("../../state/use-atom-command", () => ({ useAtomCommand: () => fixture.pairing }));
vi.mock("../../connection/onboarding", () => ({ connectPairing: Symbol("pairing") }));
vi.mock("../../state/entities", () => ({ setActiveEnvironmentId: vi.fn() }));
vi.mock("./sandboxEnvironmentMap", () => ({ rememberSandboxEnvironment: vi.fn() }));
vi.mock("./sandboxQueries", () => ({
  invalidateSandboxLists: vi.fn(),
  updateLocalSandboxCache: vi.fn(),
}));

vi.mock("./applySandboxCredentials", () => ({
  applySandboxCredentials: fixture.apply,
  saveSandboxCredentialSelection: vi.fn(),
}));
vi.mock("./injectionRules", () => ({
  useInjectionRules: () => ({
    data: [{ id: "credential", name: "API" }],
    isLoading: false,
    isError: false,
  }),
}));
vi.mock("./credentialDocuments", () => ({
  useServerAgentConfigs: () => ({
    data: [
      {
        id: "agent",
        name: "My Codex",
        agent: "codex",
        connectionId: "credential",
        defaultModel: "my-model",
        modelNames: ["my-model"],
      },
    ],
    isSuccess: true,
  }),
}));
vi.mock("./agentConfigs", () => ({
  readAgentConfigs: () => [
    {
      id: "agent",
      name: "My Codex",
      agent: "codex",
      connectionId: "credential",
      defaultModel: "my-model",
    },
  ],
  AGENT_LABELS: { codex: "Codex" },
}));
vi.mock("../../components/ui/button", () => ({
  Button: (props: React.ComponentProps<"button">) => <button {...props} />,
}));
vi.mock("../../components/ui/input", () => ({
  Input: (props: React.ComponentProps<"input">) => <input {...props} />,
}));
vi.mock("../../components/ui/dialog", () => {
  const Slot = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return Object.fromEntries(
    [
      "Dialog",
      "DialogDescription",
      "DialogFooter",
      "DialogHeader",
      "DialogPanel",
      "DialogPopup",
      "DialogTitle",
    ].map((name) => [name, Slot]),
  );
});
vi.mock("../../components/ui/select", () => {
  const Slot = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return Object.fromEntries(
    ["Select", "SelectItem", "SelectPopup", "SelectTrigger", "SelectValue"].map((name) => [
      name,
      Slot,
    ]),
  );
});
import { CreateSandboxDialog } from "./CreateSandboxDialog";
let renderer: ReactTestRenderer;
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  fixture.create.mockReset().mockResolvedValue({
    sandbox_id: "local-sandbox",
    operation: { operation_id: "op", state: 3 },
  });
  fixture.apply.mockReset().mockResolvedValue(undefined);
  fixture.navigate.mockReset();
  fixture.pairing.mockReset().mockResolvedValue({ _tag: "Success", value: "environment" });
  vi.stubGlobal("window", {
    isodoDesktop: {
      listLocalTemplates: async () => ({ items: [{ id: "base", ready: true }] }),
      createLocalSandbox: fixture.create,
      getLocalSandboxApp: async () => ({ url: "http://localhost/pair" }),
      setSandboxInjections: vi.fn(),
    },
  });
});
afterEach(async () => {
  await act(async () => renderer?.unmount());
  vi.unstubAllGlobals();
});
async function click(text: string) {
  const button = renderer.root
    .findAllByType("button")
    .find((node) => node.children.join("") === text)!;
  expect(button).toBeDefined();
  await act(async () => button.props.onClick());
}
it("offers collapsed options and applies selected credentials before pairing", async () => {
  await act(async () => {
    renderer = create(<CreateSandboxDialog />);
  });
  expect(renderer.root.findByType("details").props.open).toBeUndefined();
  await act(async () =>
    renderer.root.findByProps({ type: "checkbox" }).props.onChange({ target: { checked: true } }),
  );
  await click("创建本地沙箱");
  expect(fixture.apply).toHaveBeenCalledWith(
    expect.objectContaining({
      sandboxId: "local-sandbox",
      selection: { agentIds: ["agent"], credentialIds: [] },
    }),
  );
  expect(fixture.apply.mock.invocationCallOrder[0]).toBeLessThan(
    fixture.pairing.mock.invocationCallOrder[0]!,
  );
});
it("retries failed credential application without creating a second sandbox", async () => {
  fixture.apply.mockRejectedValueOnce(new Error("write failed"));
  await act(async () => {
    renderer = create(<CreateSandboxDialog />);
  });
  await act(async () =>
    renderer.root.findByProps({ type: "checkbox" }).props.onChange({ target: { checked: true } }),
  );
  await click("创建本地沙箱");
  expect(JSON.stringify(renderer.toJSON())).toContain("沙箱已创建");
  await click("重试配置并连接");
  expect(fixture.create).toHaveBeenCalledTimes(1);
  expect(fixture.apply).toHaveBeenCalledTimes(2);
});
it("creates normally when no credentials were selected", async () => {
  await act(async () => {
    renderer = create(<CreateSandboxDialog />);
  });
  await click("创建本地沙箱");
  expect(fixture.apply).not.toHaveBeenCalled();
  expect(fixture.pairing).toHaveBeenCalledTimes(1);
});
