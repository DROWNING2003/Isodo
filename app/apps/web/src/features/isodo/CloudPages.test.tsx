const serverConfigs = vi.hoisted(() => ({
  removed: false,
  remove: vi.fn(async (_id: string) => {
    serverConfigs.removed = true;
  }),
}));
vi.mock("./credentialDocuments", () => ({
  useServerAgentConfigs: () => ({
    data: serverConfigs.removed
      ? []
      : JSON.parse(localStorage.getItem("isodo.agent-configs.v1") ?? "[]"),
    isSuccess: true,
  }),
  useAgentConfigMutations: () => ({ save: vi.fn(), remove: serverConfigs.remove }),
}));
import { act, type ReactNode } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vite-plus/test";
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn(), Link: () => null }));
vi.mock("./auth", () => ({
  useAuth: () => ({ status: "authenticated", user: { name: "test-user", provider: "test" } }),
  getAuthGeneration: () => 0,
  refreshAuth: vi.fn(),
  signIn: vi.fn(),
}));
vi.mock("./cloud", () => ({
  useCloudState: () => ({ sandboxes: [], tasks: [], snapshots: [] }),
  cloud: {},
}));
vi.mock("./useSandboxConnections", () => ({
  useSandboxConnections: () => ({ environmentForSandbox: () => null, error: null }),
}));
vi.mock("../../api/public.generated", () => ({
  listSandboxes: () => new Promise(() => {}),
  deleteSandbox: vi.fn(),
  startSandbox: vi.fn(),
  stopSandbox: vi.fn(),
}));
vi.mock("../../components/ui/sidebar", () => ({
  SidebarInset: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SidebarTrigger: () => null,
}));
vi.mock("../../components/ui/scroll-area", () => ({
  ScrollArea: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));
vi.mock("../../components/ui/card", () => {
  const Slot = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return { Card: Slot, CardHeader: Slot, CardTitle: Slot, CardPanel: Slot, CardFooter: Slot };
});
vi.mock("../../components/ui/button", () => ({
  Button: (props: React.ComponentProps<"button">) => <button {...props} />,
}));
vi.mock("../../components/ui/input", () => ({ Input: () => null }));
vi.mock("./injectionRules", () => ({
  useInjectionRules: () => ({ data: [], isLoading: false, isError: false }),
  useInjectionSummary: () => ({ data: {} }),
  useInjectionRuleMutations: () => ({ create: vi.fn(), update: vi.fn(), remove: vi.fn() }),
}));
vi.mock("../../components/ui/dialog", () => {
  const Slot = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    Dialog: ({ open, children }: { open: boolean; children?: ReactNode }) =>
      open ? <div>{children}</div> : null,
    DialogPopup: Slot,
    DialogHeader: Slot,
    DialogTitle: Slot,
    DialogDescription: Slot,
    DialogPanel: Slot,
  };
});
import { CloudOverview, CloudCredentials } from "./CloudPages";
import { sandboxQueryClient, localSandboxesKey } from "./sandboxQueries";
let renderer: ReactTestRenderer;
afterEach(async () => {
  await act(async () => renderer?.unmount());
  sandboxQueryClient.clear();
  vi.unstubAllGlobals();
});
it("renders cached local cards while the independent cloud request is still pending", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("window", { isodoDesktop: { listLocalSandboxes: vi.fn() } });
  sandboxQueryClient.setQueryData(localSandboxesKey, [
    { id: "sandbox-a", name: "Cached local sandbox", state: "running" },
  ]);
  await act(async () => {
    renderer = create(<CloudOverview />);
  });
  const output = JSON.stringify(renderer.toJSON());
  expect(output).not.toContain("个运行中");
  expect(output).not.toContain("isodo-overview-summary");
  expect(output).toContain("Cached local sandbox");
  expect(output).not.toContain("没有符合条件的沙箱");
  await act(async () => renderer.unmount());
  await act(async () => {
    renderer = create(<CloudOverview />);
  });
  expect(JSON.stringify(renderer.toJSON())).toContain("Cached local sandbox");
});

it("uses icon add buttons and confirms and persists Agent config deletion", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("window", {});
  const config = {
    id: "config-1",
    name: "Test Codex",
    agent: "codex",
    connectionId: "conn-1",
    modelNames: ["model"],
    defaultModel: "model",
    filePath: "~/.codex/config.toml",
    content: "",
    updatedAt: "",
  };
  const storage = { getItem: () => JSON.stringify([config]), setItem: vi.fn() };
  vi.stubGlobal("localStorage", storage);
  await act(async () => {
    renderer = create(<CloudCredentials />);
  });
  const add = renderer.root.findByProps({ "aria-label": "添加 Agent 配置" });
  expect(add.props.size).toBe("icon");
  expect(renderer.root.findByProps({ "aria-label": "添加 HTTP 凭证" }).props.size).toBe("icon");
  await act(async () =>
    renderer.root.findByProps({ "aria-label": "删除 Test Codex" }).props.onClick(),
  );
  expect(storage.setItem).not.toHaveBeenCalled();
  const cancel = renderer.root
    .findAllByType("button")
    .find((node) => node.children.join("") === "取消")!;
  await act(async () => cancel.props.onClick());
  expect(storage.setItem).not.toHaveBeenCalled();
  await act(async () =>
    renderer.root.findByProps({ "aria-label": "删除 Test Codex" }).props.onClick(),
  );
  const confirm = renderer.root
    .findAllByType("button")
    .find((node) => node.children.join("") === "删除")!;
  await act(async () => confirm.props.onClick());
  expect(serverConfigs.remove).toHaveBeenCalledWith("config-1");
  expect(storage.setItem).not.toHaveBeenCalled();
  expect(JSON.stringify(renderer.toJSON())).toContain("还没有 Agent 配置");
  expect(renderer.root.findAllByProps({ "aria-label": "删除 Test Codex" })).toHaveLength(0);
});
