import { act, useState, type ReactNode } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";

const fixture = vi.hoisted(() => ({
  pathname: "/draft/draft-a",
  activeEnvironmentId: "env-other",
  draftEnvironmentId: "env-local",
  environment: vi.fn(),
  local: true,
  threadTitle: "hi",
  newThread: vi.fn(async () => ({ draftId: "next-draft" })),
  openPanel: vi.fn(),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children?: ReactNode }) => <a>{children}</a>,
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: fixture.pathname, search: {} }),
  useMatch: ({ from }: { from: string }) => {
    if (from === "/_chat/draft/$draftId" && fixture.pathname.startsWith("/draft/")) {
      return { params: { draftId: "draft-a" } };
    }
    if (from === "/_chat/$environmentId/$threadId" && fixture.pathname === "/env-local/thread-a") {
      return { params: { environmentId: "env-local", threadId: "thread-a" } };
    }
    if (
      from === "/sandbox-connections/$sandboxId" &&
      fixture.pathname === "/sandbox-connections/sbx-local"
    )
      return { params: { sandboxId: "sbx-local" } };
    if (from === "/sandboxes/$sandboxId" && fixture.pathname === "/sandboxes/sbx-local") {
      return { params: { sandboxId: "sbx-local" } };
    }
    return undefined;
  },
}));
vi.mock("../../composerDraftStore", () => ({
  DraftId: { make: (id: string) => id },
  useComposerDraftStore: (select: (state: unknown) => unknown) =>
    select({
      getDraftSession: () => ({
        environmentId: fixture.draftEnvironmentId,
        threadId: "draft-thread-a",
        projectId: "project-a",
      }),
    }),
}));
vi.mock("../../state/entities", () => ({
  setActiveEnvironmentId: vi.fn(),
  useActiveEnvironmentId: () => useState(fixture.activeEnvironmentId)[0],
  useThreadShells: () => [
    { environmentId: "env-local", id: "thread-a", title: fixture.threadTitle },
    { environmentId: "env-other", id: "other-id", title: "Other sandbox title" },
  ],
  useProjects: () => [{ id: "project-a", environmentId: "env-local" }],
}));
vi.mock("../../state/environments", () => ({
  useEnvironments: () => ({ environments: [{ environmentId: "env-local" }] }),
  useEnvironment: (id: string) => {
    fixture.environment(id);
    return null;
  },
}));
vi.mock("./useSandboxConnections", () => ({
  useSandboxConnections: () => ({ disconnect: async () => true, disconnecting: null, error: null }),
}));
vi.mock("./sandboxEnvironmentMap", () => ({
  rememberSandboxWorkspace: vi.fn(),
  environmentIdForSandbox: (id: string) => (id === "sbx-local" ? "env-local" : null),
  workspaceTargetForEnvironment: () => ({
    to: "/draft/$draftId",
    params: { draftId: "draft-a" },
    replace: true,
  }),
  sandboxIdForEnvironment: (id: string) => (id === "env-local" ? "sbx-local" : null),
}));
vi.mock("./auth", () => ({
  refreshAuth: async () => {},
  useAuth: () => ({ user: null }),
  signIn: vi.fn(),
  signOut: vi.fn(),
}));
vi.mock("./cloud", () => ({ useCloudState: () => ({ sandboxes: [] }) }));
vi.mock("./usePlatformSandbox", () => ({
  usePlatformSandbox: () => ({ record: null, loading: false }),
  useLocalRuntimeSandbox: (id: string) => ({
    record:
      fixture.local && id === "sbx-local"
        ? {
            id,
            name: "真实本地沙箱",
            status: "running",
            resources: { vcpu: 6, memory_bytes: 12 * 2 ** 30, disk_bytes: 80 * 2 ** 30 },
          }
        : null,
    loading: false,
  }),
  platformSandboxView: (record: unknown) => record,
}));
vi.mock("../../hooks/useHandleNewThread", () => ({ useNewThreadHandler: () => fixture.newThread }));
vi.mock("../../rightPanelStore", () => ({
  useRightPanelStore: Object.assign(() => null, { getState: () => ({ open: fixture.openPanel }) }),
  selectActiveRightPanel: () => null,
}));
vi.mock("./CloudWorkspace", () => ({ isCloudWorkspacePane: () => false }));
vi.mock("../../env", () => ({ isElectron: false }));
vi.mock("../../components/ui/sidebar", () => {
  const Slot = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    SidebarContent: Slot,
    SidebarFooter: Slot,
    SidebarHeader: Slot,
    SidebarMenu: Slot,
    SidebarMenuButton: ({
      children,
      onClick,
      ...props
    }: {
      children?: ReactNode;
      onClick?: () => void;
      "aria-label"?: string;
      disabled?: boolean;
    }) => (
      <button aria-label={props["aria-label"]} disabled={props.disabled} onClick={onClick}>
        {children}
      </button>
    ),
    SidebarMenuItem: Slot,
    useSidebar: () => ({ setOpenMobile: vi.fn() }),
  };
});
vi.mock("../../components/ui/menu", () => {
  const Slot = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    DropdownMenu: Slot,
    DropdownMenuContent: Slot,
    DropdownMenuItem: Slot,
    DropdownMenuSeparator: Slot,
    DropdownMenuTrigger: Slot,
  };
});

import { CloudSidebar } from "./CloudSidebar";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("window", { location: { search: "" } });
  fixture.pathname = "/draft/draft-a";
  fixture.local = true;
  fixture.threadTitle = "hi";
  fixture.environment.mockClear();
});
afterEach(async () => {
  await act(async () => renderer?.unmount());
  vi.unstubAllGlobals();
});

async function renderSidebar() {
  await act(async () => {
    renderer = create(<CloudSidebar />);
  });
  return JSON.stringify(renderer!.toJSON());
}

describe("sandbox workspace sidebar", () => {
  it("resolves a draft from its own environment rather than the active environment or 'draft'", async () => {
    await renderSidebar();
    expect(fixture.environment).toHaveBeenLastCalledWith("env-local");
  });

  it("shows the workspace for a real desktop sandbox outside the mock and cloud catalogs", async () => {
    fixture.pathname = "/env-local/thread-a";
    const output = await renderSidebar();
    expect(output).toContain("真实本地沙箱");
    expect(output).toContain("新任务");
    expect(output).toContain("本地");
  });

  it("preserves hook order when navigating from a sandbox entry to a draft", async () => {
    fixture.pathname = "/sandboxes/sbx-local";
    await renderSidebar();
    fixture.pathname = "/draft/draft-a";
    await expect(act(async () => renderer!.update(<CloudSidebar />))).resolves.toBeUndefined();
    expect(JSON.stringify(renderer!.toJSON())).toContain("真实本地沙箱");
  });

  it("starts a task in the paired project without returning to the sandbox connection route", async () => {
    await renderSidebar();
    await act(async () => renderer!.root.findByProps({ "aria-label": "新任务" }).props.onClick());
    expect(fixture.newThread).toHaveBeenCalledWith({
      environmentId: "env-local",
      projectId: "project-a",
    });
  });

  it("opens T3 files for the current draft without leaving the conversation", async () => {
    await renderSidebar();
    await act(async () => renderer!.root.findByProps({ "aria-label": "文件" }).props.onClick());
    expect(fixture.openPanel).toHaveBeenCalledWith(
      { environmentId: "env-local", threadId: "draft-thread-a" },
      "files",
    );
  });

  it("keeps the paired workspace visible if the sandbox status lookup fails", async () => {
    fixture.local = false;
    const output = await renderSidebar();
    expect(output).toContain("新任务");
    expect(output).toContain("状态未知");
    expect(output).not.toContain("我的沙箱");
  });

  it("keeps the sandbox overview in list mode even with a paired environment", async () => {
    fixture.pathname = "/sandboxes";
    expect(await renderSidebar()).toContain("我的沙箱");
  });
});

it("keeps new task and files actions available on the sandbox credentials route", async () => {
  fixture.pathname = "/sandbox-connections/sbx-local";
  await renderSidebar();
  await act(async () => renderer!.root.findByProps({ "aria-label": "新任务" }).props.onClick());
  expect(fixture.newThread).toHaveBeenCalledWith({
    environmentId: "env-local",
    projectId: "project-a",
  });
  await act(async () => renderer!.root.findByProps({ "aria-label": "文件" }).props.onClick());
  expect(fixture.openPanel).toHaveBeenCalledWith(
    { environmentId: "env-local", threadId: "draft-thread-a" },
    "files",
  );
});

it("shows the shared session title and follows title updates without displaying internal IDs", async () => {
  const output = await renderSidebar();
  expect(output).toContain("hi");
  expect(output).not.toContain("Other sandbox title");
  fixture.threadTitle = "修复登录问题";
  await act(async () => renderer!.update(<CloudSidebar />));
  expect(JSON.stringify(renderer!.toJSON())).toContain("修复登录问题");
  fixture.threadTitle = "";
  await act(async () => renderer!.update(<CloudSidebar />));
  expect(JSON.stringify(renderer!.toJSON())).not.toContain(">thread-a<");
  const labels = renderer!.root.findAllByType("span").map((node) => node.children.join(""));
  expect(labels).toContain("新任务");
  expect(labels).not.toContain("thread-a");
});

it("uses the local icon and shows actual allocated resources", async () => {
  const output = await renderSidebar();
  expect(output).toContain("本地沙箱");
  const resourceLabels = renderer!.root.findAllByType("span").map((node) => node.children.join(""));
  expect(resourceLabels).toContain("6 vCPU");
  expect(resourceLabels).toContain("12 GiB 内存");
  expect(resourceLabels).toContain("80 GiB 磁盘");
  expect(renderer!.root.findByProps({ "aria-label": "本地沙箱" })).toBeDefined();
  expect(renderer!.root.findAllByProps({ "aria-label": "云端沙箱" })).toHaveLength(0);
});

it("keeps the Skills entry disabled in both account and sandbox sidebars", async () => {
  await renderSidebar();
  let skill = renderer!.root.findByProps({ "aria-label": "Skill", disabled: true });
  expect(skill.props.onClick).toBeUndefined();
  expect(
    renderer!.root.findByProps({ "aria-label": "MCP", disabled: true }).props.onClick,
  ).toBeUndefined();
  fixture.pathname = "/sandboxes";
  await act(async () => renderer!.update(<CloudSidebar />));
  skill = renderer!.root.findByProps({ "aria-label": "Skill", disabled: true });
  expect(skill.props.onClick).toBeUndefined();
  expect(
    renderer!.root.findByProps({ "aria-label": "MCP", disabled: true }).props.onClick,
  ).toBeUndefined();
});
