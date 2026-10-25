vi.mock("./sandboxQueries", () => ({ updateLocalSandboxCache: vi.fn() }));
import { act, type ReactNode } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vite-plus/test";

const fixture = vi.hoisted(() => ({
  savedId: null as string | null,
  loading: false,
  resolve: vi.fn(async () => "https://runtime.test/pair"),
  connectionCommand: Symbol("set-connected"),
  resume: vi.fn(async () => ({ _tag: "Success" })),
  navigate: vi.fn(),
  remember: vi.fn(),
  activate: vi.fn(),
  connect: vi.fn(async () => ({ _tag: "Success", value: "env-local" })),
  record: { id: "sbx-local", name: "Local sandbox", status: "running" },
}));
vi.mock("@tanstack/react-router", () => ({
  Link: () => null,
  useNavigate: () => fixture.navigate,
}));
vi.mock("../../components/ui/button", () => ({ Button: () => null }));
vi.mock("../../components/ui/sidebar", () => ({
  SidebarInset: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SidebarTrigger: () => null,
}));
vi.mock("./sandboxConnectionCommands", () => ({
  setSandboxEnvironmentConnected: fixture.connectionCommand,
}));
vi.mock("../../connection/onboarding", () => ({ connectPairing: Symbol("connect") }));
vi.mock("../../state/use-atom-command", () => ({
  useAtomCommand: (command: unknown) =>
    command === fixture.connectionCommand ? fixture.resume : fixture.connect,
}));
vi.mock("../../state/entities", () => ({
  setActiveEnvironmentId: fixture.activate,
  useAllEnvironmentShellsBootstrapped: () => false,
}));
vi.mock("../../state/environments", () => ({
  useEnvironments: () => ({
    isReady: true,
    environments: fixture.savedId ? [{ environmentId: fixture.savedId }] : [],
  }),
}));
vi.mock("./sandboxEnvironmentMap", () => ({
  rememberSandboxEnvironment: fixture.remember,
  setSandboxEnvironmentDisconnected: vi.fn(),
  environmentIdForSandbox: () => fixture.savedId,
  workspaceTargetForEnvironment: () => ({ to: "/workspace", replace: true }),
}));
vi.mock("./cloud", () => ({ useCloudState: () => ({ sandboxes: [] }), cloud: {} }));
vi.mock("./usePlatformSandbox", () => ({
  useLocalRuntimeSandbox: () => ({
    record: fixture.loading ? null : fixture.record,
    loading: fixture.loading,
  }),
  usePlatformSandbox: () => ({ record: null, loading: false }),
  platformSandboxView: (record: unknown) => record,
  resolvePairingUrl: fixture.resolve,
}));
import { CloudWorkspace } from "./CloudWorkspace";
let renderer: ReactTestRenderer;
afterEach(async () => {
  await act(async () => renderer?.unmount());
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it("enters the workbench after pairing without waiting on a stale catalog or reloading away the active environment", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("window", { location: { replace: vi.fn() } });
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
  await act(async () => {
    renderer = create(<CloudWorkspace sandboxId="sbx-local" />);
  });
  expect(fixture.remember).toHaveBeenCalledWith("env-local", "sbx-local");
  expect(fixture.activate).toHaveBeenCalledWith("env-local");
  expect(fixture.navigate).toHaveBeenCalledWith({ to: "/workspace", replace: true });
});

it("reuses a paired sandbox after returning to the list without requesting another pairing", async () => {
  fixture.connect.mockClear();
  fixture.savedId = "env-local";
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  await act(async () => {
    renderer = create(<CloudWorkspace sandboxId="sbx-local" />);
  });
  expect(fixture.activate).toHaveBeenLastCalledWith("env-local");
  expect(fixture.navigate).toHaveBeenLastCalledWith({ to: "/workspace", replace: true });
  expect(fixture.connect).not.toHaveBeenCalled();
  await act(async () => renderer.unmount());
  await act(async () => {
    renderer = create(<CloudWorkspace sandboxId="sbx-local" />);
  });
  expect(fixture.connect).not.toHaveBeenCalled();
  fixture.savedId = null;
});

it("does not pull the user back into a workspace when background pairing finishes after returning to the list", async () => {
  let finish!: (result: { _tag: string; value: string }) => void;
  fixture.connect.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  fixture.navigate.mockClear();
  fixture.activate.mockClear();
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  await act(async () => {
    renderer = create(<CloudWorkspace sandboxId="sbx-local" />);
  });
  await act(async () => renderer.unmount());
  await act(async () => finish({ _tag: "Success", value: "env-local" }));
  expect(fixture.remember).toHaveBeenLastCalledWith("env-local", "sbx-local");
  expect(fixture.navigate).not.toHaveBeenCalled();
  expect(fixture.activate).not.toHaveBeenCalled();
});

it("repairs the local runtime before resuming an existing pairing", async () => {
  fixture.savedId = "env-local";
  fixture.resolve.mockClear();
  fixture.resume.mockClear();
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  await act(async () => {
    renderer = create(<CloudWorkspace sandboxId="sbx-local" />);
  });
  expect(fixture.resolve).toHaveBeenCalledWith("sbx-local", { local: true });
  expect(fixture.resolve.mock.invocationCallOrder[0]).toBeLessThan(
    fixture.resume.mock.invocationCallOrder[0]!,
  );
  fixture.savedId = null;
});

it("waits for sandbox ownership before resuming a saved pairing", async () => {
  fixture.savedId = "env-local";
  fixture.loading = true;
  fixture.resolve.mockClear();
  fixture.resume.mockClear();
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  await act(async () => {
    renderer = create(<CloudWorkspace sandboxId="sbx-local" />);
  });
  expect(fixture.resume).not.toHaveBeenCalled();
  fixture.loading = false;
  await act(async () => renderer.update(<CloudWorkspace sandboxId="sbx-local" />));
  expect(fixture.resolve).toHaveBeenCalledWith("sbx-local", { local: true });
  expect(fixture.resume).toHaveBeenCalledTimes(1);
  fixture.savedId = null;
});

it("keeps a paused paired sandbox on its start screen instead of attempting a failing connection", async () => {
  fixture.savedId = "env-local";
  fixture.record.status = "stopped";
  fixture.resolve.mockClear();
  fixture.resume.mockClear();
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  await act(async () => {
    renderer = create(<CloudWorkspace sandboxId="sbx-local" />);
  });
  expect(fixture.resolve).not.toHaveBeenCalled();
  expect(fixture.resume).not.toHaveBeenCalled();
  fixture.record.status = "running";
  fixture.savedId = null;
});
