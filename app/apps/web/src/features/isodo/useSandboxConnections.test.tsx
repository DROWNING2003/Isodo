vi.mock("./pauseSandbox", () => ({ pauseSandbox: fixture.pause }));
vi.mock("./sandboxQueries", () => ({ invalidateSandboxLists: vi.fn() }));
import { act } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { EnvironmentId } from "@t3tools/contracts";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";

const fixture = vi.hoisted(() => ({
  setConnected: vi.fn(),
  pause: vi.fn(),
  activate: vi.fn(),
  phase: "connected",
}));
const envA = EnvironmentId.make("env-a");
const envB = EnvironmentId.make("env-b");
vi.mock("./sandboxConnectionCommands", () => ({
  setSandboxEnvironmentConnected: Symbol("set-connected"),
}));
vi.mock("../../state/use-atom-command", () => ({ useAtomCommand: () => fixture.setConnected }));
vi.mock("../../state/entities", () => ({
  useActiveEnvironmentId: () => envA,
  setActiveEnvironmentId: fixture.activate,
}));
vi.mock("../../state/environments", () => ({
  useEnvironments: () => ({
    environments: [
      { environmentId: envA, connection: { phase: fixture.phase } },
      { environmentId: envB, connection: { phase: fixture.phase } },
    ],
  }),
}));
import { useSandboxConnections } from "./useSandboxConnections";
import {
  setSandboxEnvironmentDisconnected,
  rememberSandboxEnvironment,
  sandboxIdForEnvironment,
  rememberSandboxWorkspace,
  workspaceTargetForEnvironment,
} from "./sandboxEnvironmentMap";
let renderer: ReactTestRenderer;
let connections: ReturnType<typeof useSandboxConnections>;
function Harness() {
  connections = useSandboxConnections();
  return null;
}
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
  });
  setSandboxEnvironmentDisconnected(envA, false);
  setSandboxEnvironmentDisconnected(envB, false);
  rememberSandboxEnvironment(envA, "sandbox-a");
  rememberSandboxEnvironment(envB, "sandbox-b");
  fixture.phase = "connected";
  fixture.pause.mockReset().mockResolvedValue(undefined);
  fixture.setConnected.mockReset().mockResolvedValue({ _tag: "Success" });
  fixture.activate.mockClear();
});
afterEach(async () => {
  await act(async () => renderer?.unmount());
  vi.unstubAllGlobals();
});
it("keeps both sandbox connections when the displaying page unmounts", async () => {
  await act(async () => {
    renderer = create(<Harness />);
  });
  expect(connections.environmentForSandbox("sandbox-a")).toBe(envA);
  expect(connections.environmentForSandbox("sandbox-b")).toBe(envB);
  await act(async () => renderer.unmount());
  expect(fixture.setConnected).not.toHaveBeenCalled();
  expect(sandboxIdForEnvironment(envA)).toBe("sandbox-a");
  expect(sandboxIdForEnvironment(envB)).toBe("sandbox-b");
});
it("disconnects only the selected transport while retaining its pairing and task", async () => {
  rememberSandboxWorkspace(envA, {
    to: "/draft/$draftId",
    params: { draftId: "draft-a" },
    replace: true,
  });
  rememberSandboxWorkspace(envB, {
    to: "/draft/$draftId",
    params: { draftId: "draft-b" },
    replace: true,
  });
  await act(async () => {
    renderer = create(<Harness />);
  });
  await act(async () => {
    expect(await connections.disconnect("sandbox-a")).toBe(true);
  });
  expect(fixture.setConnected).toHaveBeenCalledExactlyOnceWith({
    environmentId: envA,
    connected: false,
  });
  expect(sandboxIdForEnvironment(envA)).toBe("sandbox-a");
  expect(connections.environmentForSandbox("sandbox-a")).toBeNull();
  expect(connections.environmentForSandbox("sandbox-b")).toBe(envB);
  expect(workspaceTargetForEnvironment(envA)).toEqual({
    to: "/draft/$draftId",
    params: { draftId: "draft-a" },
    replace: true,
  });
  expect(sandboxIdForEnvironment(envB)).toBe("sandbox-b");
  expect(workspaceTargetForEnvironment(envB)).toEqual({
    to: "/draft/$draftId",
    params: { draftId: "draft-b" },
    replace: true,
  });
});
it("retains pairing when disconnect fails", async () => {
  fixture.setConnected.mockResolvedValue({ _tag: "Failure" });
  await act(async () => {
    renderer = create(<Harness />);
  });
  await act(async () => {
    expect(await connections.disconnect("sandbox-a")).toBe(false);
  });
  expect(sandboxIdForEnvironment(envA)).toBe("sandbox-a");
  expect(fixture.activate).not.toHaveBeenCalled();
  expect(connections.error).toContain("断开连接失败");
});

it("does not label a retrying pairing as connected and still allows stopping retries", async () => {
  fixture.phase = "reconnecting";
  await act(async () => {
    renderer = create(<Harness />);
  });
  expect(connections.environmentForSandbox("sandbox-a")).toBeNull();
  expect(connections.canDisconnectSandbox("sandbox-a")).toBe(true);
  await act(async () => {
    await connections.disconnect("sandbox-a");
  });
  expect(connections.canDisconnectSandbox("sandbox-a")).toBe(false);
  expect(sandboxIdForEnvironment(envA)).toBe("sandbox-a");
});

it("pauses the selected sandbox before disconnecting its transport", async () => {
  await act(async () => {
    renderer = create(<Harness />);
  });
  await act(async () => {
    expect(await connections.pauseAndDisconnect("sandbox-a")).toBe(true);
  });
  expect(fixture.pause).toHaveBeenCalledWith("sandbox-a");
  expect(fixture.pause.mock.invocationCallOrder[0]).toBeLessThan(
    fixture.setConnected.mock.invocationCallOrder[0]!,
  );
  expect(connections.environmentForSandbox("sandbox-b")).toBe(envB);
});
it("keeps the connection when pausing fails and reports the error", async () => {
  fixture.pause.mockRejectedValue(new Error("暂停失败"));
  await act(async () => {
    renderer = create(<Harness />);
  });
  await act(async () => {
    expect(await connections.pauseAndDisconnect("sandbox-a")).toBe(false);
  });
  expect(fixture.setConnected).not.toHaveBeenCalled();
  expect(connections.error).toBe("暂停失败");
});
