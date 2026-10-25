import { Link, useLocation, useMatch, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { scopeProjectRef, scopeThreadRef } from "@t3tools/client-runtime/environment";
import { EnvironmentId, ThreadId } from "@t3tools/contracts";
import {
  ArrowLeftIcon,
  BookOpenIcon,
  ChevronDownIcon,
  CloudIcon,
  LaptopIcon,
  BoxIcon,
  Code2Icon,
  FileIcon,
  EllipsisIcon,
  KeyRoundIcon,
  LogOutIcon,
  LayersIcon,
  MessageSquareIcon,
  PlugIcon,
  PlusIcon,
  ShieldCheckIcon,
  SparklesIcon,
  NetworkIcon,
  SettingsIcon,
  UserRoundIcon,
} from "lucide-react";
import {
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "../../components/ui/sidebar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../../components/ui/menu";
import icon from "../../assets/isodo/icon.png";
import { cn } from "../../lib/utils";
import { isElectron } from "../../env";
import { sandboxIdFromUrl, isSandboxUrl } from "@t3tools/shared/sandbox";
import { DraftId, useComposerDraftStore } from "../../composerDraftStore";
import {
  setActiveEnvironmentId,
  useActiveEnvironmentId,
  useThreadShells,
  useProjects,
} from "../../state/entities";
import { useNewThreadHandler } from "../../hooks/useHandleNewThread";
import { selectActiveRightPanel, useRightPanelStore } from "../../rightPanelStore";
import { useEnvironment, useEnvironments } from "../../state/environments";
import { refreshAuth } from "./auth";
import { useCloudState } from "./cloud";
import { useSandboxConnections } from "./useSandboxConnections";
import {
  sandboxIdForEnvironment,
  rememberSandboxWorkspace,
  environmentIdForSandbox,
  workspaceTargetForEnvironment,
} from "./sandboxEnvironmentMap";
import {
  platformSandboxView,
  useLocalRuntimeSandbox,
  usePlatformSandbox,
} from "./usePlatformSandbox";
import { signOut, useAuth } from "./auth";
import { type CloudWorkspacePane } from "./CloudWorkspace";

function AccountMenu() {
  const { user: account } = useAuth();
  const { setOpenMobile } = useSidebar();
  const navigate = useNavigate();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left outline-none hover:bg-sidebar-accent focus-visible:ring-2 focus-visible:ring-ring"
        aria-label="个人账号菜单"
      >
        <span className="inline-flex size-8 shrink-0 items-center justify-center rounded-full border border-sidebar-border bg-sidebar-accent text-sm font-medium text-sidebar-foreground">
          {account?.name.slice(0, 1).toUpperCase() ?? "?"}
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">{account?.name ?? "未登录"}</span>
          <span className="block text-xs text-muted-foreground">个人账号</span>
        </span>
        <span className="rounded-md border border-sidebar-border px-2 py-1 text-xs font-medium">
          FREE
        </span>
        <EllipsisIcon className="size-4 shrink-0 text-muted-foreground" />
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="end" className="w-60">
        <div className="px-2 py-2">
          <p className="truncate text-sm font-medium">{account?.name ?? "未登录"}</p>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {account ? `${account.provider}` : "个人账号"}
          </p>
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          render={<Link to="/developer/settings" />}
          onClick={() => setOpenMobile(false)}
        >
          <UserRoundIcon />
          个人信息
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        {account ? (
          <DropdownMenuItem
            className="text-destructive data-highlighted:text-destructive"
            onClick={() => {
              void signOut()
                .then(() => navigate({ to: "/sandboxes/login" }))
                .catch(() => window.alert("退出登录失败，请重试"));
              setOpenMobile(false);
            }}
          >
            <LogOutIcon />
            退出登录
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem
            onClick={() => {
              setOpenMobile(false);
              void navigate({ to: "/sandboxes/login" });
            }}
          >
            <UserRoundIcon />
            登录
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function SandboxWorkspaceSidebar({
  sandbox,
  location,
  activePane,
  activeTask,
  activeThreadId,
  threads,
  onNavigate,
  onNewTask,
  onOpenFiles,
  onOpenConnections,
  creatingTask,
  statusKnown,
  resources,
  onDisconnect,
  disconnecting,
  error,
}: {
  sandbox: NonNullable<ReturnType<typeof useCloudState>["sandboxes"][number]>;
  /** Whether this sandbox is served by the desktop runtime or the control plane. */
  location: "local" | "cloud" | null;
  activePane: CloudWorkspacePane | null;
  activeTask: string | null;
  activeThreadId: string | null;
  threads: ReadonlyArray<{ environmentId: EnvironmentId; threadId: string; title: string | null }>;
  onNavigate: () => void;
  onNewTask: (() => void) | undefined;
  onOpenFiles: (() => void) | undefined;
  onOpenConnections: (() => void) | undefined;
  creatingTask: boolean;
  statusKnown: boolean;
  resources?:
    | { readonly vcpu?: number; readonly memory_bytes?: number; readonly disk_bytes?: number }
    | undefined;
  onDisconnect: () => void;
  disconnecting: boolean;
  error: string | null;
}) {
  return (
    <>
      <SidebarHeader
        className={cn("isodo-sidebar-header gap-4 px-4 pb-5", isElectron ? "drag-region" : "pt-5")}
        // Inline because utility padding is merged away by the sidebar's base
        // spacing; the traffic lights need a guaranteed strip of clear space.
        style={isElectron ? { paddingTop: 46 } : undefined}
      >
        <Link
          to="/sandboxes"
          className="flex items-center gap-2 text-base font-semibold"
          onClick={onNavigate}
        >
          <img src={icon} alt="" className="size-7 rounded-lg" />
          isodo
        </Link>
      </SidebarHeader>
      <SidebarContent className="px-2">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton render={<Link to="/sandboxes" />} onClick={onNavigate}>
              <ArrowLeftIcon />
              所有沙箱
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton
              className="h-auto! min-h-0! items-start gap-3 py-3! [&>svg]:mt-0.5"
              isActive={!activeTask && !activePane}
              onClick={onNavigate}
            >
              {location === "local" ? (
                <LaptopIcon aria-label="本地沙箱" />
              ) : location === "cloud" ? (
                <CloudIcon aria-label="云端沙箱" />
              ) : (
                <BoxIcon aria-label="沙箱" />
              )}
              <span className="min-w-0 flex-1 truncate">
                <span className="block truncate font-medium leading-5">{sandbox.name}</span>
                <span className="mt-1 block text-xs font-normal leading-4 text-muted-foreground">
                  {location ? `${location === "local" ? "本地" : "云端"} · ` : ""}
                  {statusKnown ? (sandbox.status === "running" ? "运行中" : "已停止") : "状态未知"}
                </span>
                {resources?.vcpu && resources.memory_bytes ? (
                  <span className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] font-normal leading-4 text-muted-foreground">
                    <span>{resources.vcpu} vCPU</span>
                    <span>{Number((resources.memory_bytes / 2 ** 30).toFixed(1))} GiB 内存</span>
                    {resources.disk_bytes ? (
                      <span>{Number((resources.disk_bytes / 2 ** 30).toFixed(1))} GiB 磁盘</span>
                    ) : null}
                  </span>
                ) : null}
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton
              className="mt-1 border border-sidebar-border bg-background font-medium"
              aria-label="新任务"
              disabled={!onNewTask || creatingTask}
              onClick={onNewTask}
            >
              <PlusIcon />
              新任务
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>

        {error && (
          <p role="alert" className="px-3 py-2 text-xs text-destructive">
            {error}
          </p>
        )}
        <p className="px-3 pb-2 pt-6 text-[11px] text-muted-foreground">最近任务</p>
        <SidebarMenu>
          {threads.length === 0 ? (
            <li className="px-3 py-2 text-xs text-muted-foreground">从第一个任务开始</li>
          ) : (
            threads.slice(0, 8).map((thread) => (
              <SidebarMenuItem key={thread.threadId}>
                <SidebarMenuButton
                  isActive={activeThreadId === thread.threadId}
                  render={
                    <Link
                      to="/$environmentId/$threadId"
                      params={{
                        environmentId: thread.environmentId,
                        threadId: thread.threadId,
                      }}
                    />
                  }
                  onClick={onNavigate}
                >
                  <MessageSquareIcon />
                  <span className="truncate">{thread.title?.trim() || "新任务"}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            ))
          )}
        </SidebarMenu>

        <div className="px-3 pb-2 pt-6 text-[11px] text-muted-foreground">工作台</div>
        <SidebarMenu>
          {(
            [
              ["files", "文件", FileIcon],
              ["connections", "安全连接", PlugIcon],
              ["skills", "Skill", SparklesIcon],
              ["mcp", "MCP", NetworkIcon],
              ["snapshots", "快照", LayersIcon],
              ["security", "安全", ShieldCheckIcon],
            ] as const
          ).map(([pane, label, Icon]) => (
            <SidebarMenuItem key={pane}>
              <SidebarMenuButton
                isActive={activePane === pane}
                aria-label={label}
                disabled={
                  pane === "files"
                    ? !onOpenFiles
                    : pane === "connections"
                      ? !onOpenConnections
                      : true
                }
                title={
                  pane === "files"
                    ? "浏览当前任务的工作文件"
                    : pane === "connections"
                      ? "管理当前沙箱的安全连接"
                      : "尚未接入"
                }
                onClick={
                  pane === "files"
                    ? onOpenFiles
                    : pane === "connections"
                      ? onOpenConnections
                      : undefined
                }
              >
                <Icon />
                {label}
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarContent>
      <SidebarFooter className="gap-1 px-2 py-3">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              onClick={onDisconnect}
              disabled={disconnecting}
              aria-label="断开连接"
            >
              <LogOutIcon />
              {disconnecting ? "正在暂停…" : "暂停并断开"}
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
        <div className="mt-1 border-t border-sidebar-border pt-2">
          <AccountMenu />
        </div>
      </SidebarFooter>
    </>
  );
}

const DOCS_URL = import.meta.env.VITE_ISODO_DOCS_URL?.trim() || "http://127.0.0.1:3000/docs";

export function CloudSidebar() {
  // The isodo account state is only fetched on some routes; refresh it here so
  // a sandbox chat route does not show a stale "not signed in" account row.
  useEffect(() => {
    void refreshAuth();
  }, []);
  const connections = useSandboxConnections();
  const navigate = useNavigate();
  const state = useCloudState();
  const location = useLocation();
  const pathname = location.pathname;
  const { setOpenMobile } = useSidebar();
  const sandboxRoute = useMatch({ from: "/sandboxes/$sandboxId", shouldThrow: false });
  const connectionsRoute = useMatch({
    from: "/sandbox-connections/$sandboxId",
    shouldThrow: false,
  });
  const draftRoute = useMatch({ from: "/_chat/draft/$draftId", shouldThrow: false });
  const threadRoute = useMatch({ from: "/_chat/$environmentId/$threadId", shouldThrow: false });
  const routeSandboxId = sandboxRoute?.params.sandboxId ?? connectionsRoute?.params.sandboxId;
  const { environments } = useEnvironments();
  const routeEnvironmentId = routeSandboxId
    ? environmentIdForSandbox(
        routeSandboxId,
        environments.map((entry) => entry.environmentId),
      )
    : null;
  const rememberedTarget = routeEnvironmentId
    ? workspaceTargetForEnvironment(routeEnvironmentId)
    : null;
  const draftId =
    draftRoute?.params.draftId ??
    (rememberedTarget?.to === "/draft/$draftId" ? rememberedTarget.params.draftId : undefined);
  const draftSession = useComposerDraftStore((store) =>
    draftId ? store.getDraftSession(DraftId.make(draftId)) : null,
  );
  const activeEnvironmentId = useActiveEnvironmentId();
  const isChatRoute =
    pathname === "/" || pathname === "/workspace" || Boolean(draftRoute || threadRoute);
  const sidebarEnvironmentId =
    routeEnvironmentId ??
    (isChatRoute
      ? (draftSession?.environmentId ??
        (threadRoute ? EnvironmentId.make(threadRoute.params.environmentId) : activeEnvironmentId))
      : null);
  const activeEnvironment = useEnvironment(sidebarEnvironmentId);
  const environmentSandboxId =
    sandboxIdForEnvironment(sidebarEnvironmentId) ??
    (activeEnvironment?.displayUrl && isSandboxUrl(activeEnvironment.displayUrl)
      ? sandboxIdFromUrl(activeEnvironment.displayUrl)
      : null);
  const activeId = routeSandboxId ?? environmentSandboxId ?? undefined;
  const mockSandbox = state.sandboxes.find((sandbox) => sandbox.id === activeId);
  const localRuntime = useLocalRuntimeSandbox(mockSandbox ? "" : (activeId ?? ""));
  const platform = usePlatformSandbox(
    mockSandbox || localRuntime.record || localRuntime.loading ? "" : (activeId ?? ""),
  );
  const resolvedSandbox =
    mockSandbox ??
    (localRuntime.record ? platformSandboxView(localRuntime.record) : undefined) ??
    (platform.record ? platformSandboxView(platform.record) : undefined);
  // Pairing establishes workspace identity independently of a status request.
  // A transient runtime failure must not turn an open task into the overview.
  const activeSandbox =
    resolvedSandbox ??
    (isChatRoute && activeId
      ? platformSandboxView({ id: activeId, name: "当前沙箱", status: "stopped", description: "" })
      : undefined);
  const searchParams = location.search as { task?: string; pane?: string };
  const activeTask = searchParams.task ?? null;
  const activeThreadId =
    threadRoute?.params.threadId ??
    (rememberedTarget?.to === "/$environmentId/$threadId"
      ? rememberedTarget.params.threadId
      : null);
  const threads = useThreadShells()
    .filter((thread) => thread.environmentId === sidebarEnvironmentId)
    .map((thread) => ({
      environmentId: thread.environmentId,
      threadId: String(thread.id),
      title: thread.title.trim() || "新任务",
    }));
  const projects = useProjects();
  const project = projects.find(
    (entry) =>
      entry.environmentId === sidebarEnvironmentId &&
      (!draftSession || entry.id === draftSession.projectId),
  );
  const handleNewThread = useNewThreadHandler();
  const [openFilesWhenReady, setOpenFilesWhenReady] = useState(false);
  const [creatingTask, setCreatingTask] = useState(false);
  const [taskError, setTaskError] = useState<string | null>(null);
  const currentThreadId =
    draftSession?.threadId ?? (activeThreadId ? ThreadId.make(activeThreadId) : null);
  const currentThreadRef =
    sidebarEnvironmentId && currentThreadId
      ? scopeThreadRef(sidebarEnvironmentId, currentThreadId)
      : null;
  useEffect(() => {
    if (openFilesWhenReady && currentThreadRef) {
      useRightPanelStore.getState().open(currentThreadRef, "files");
      setOpenFilesWhenReady(false);
    }
  }, [openFilesWhenReady, currentThreadRef]);
  const rightPanel = useRightPanelStore((store) =>
    selectActiveRightPanel(store.byThreadKey, currentThreadRef),
  );
  const activePane = connectionsRoute ? "connections" : rightPanel === "files" ? "files" : null;
  const rememberedDraftId = draftRoute?.params.draftId;
  const rememberedThreadId = threadRoute?.params.threadId;
  const rememberedEnvironmentId = threadRoute?.params.environmentId;
  useEffect(() => {
    if (!sidebarEnvironmentId) return;
    if (rememberedDraftId)
      rememberSandboxWorkspace(sidebarEnvironmentId, {
        to: "/draft/$draftId",
        params: { draftId: rememberedDraftId },
        replace: true,
      });
    else if (rememberedThreadId && rememberedEnvironmentId)
      rememberSandboxWorkspace(sidebarEnvironmentId, {
        to: "/$environmentId/$threadId",
        params: { environmentId: rememberedEnvironmentId, threadId: rememberedThreadId },
        replace: true,
      });
  }, [sidebarEnvironmentId, rememberedDraftId, rememberedThreadId, rememberedEnvironmentId]);
  const newTask = project
    ? () => {
        if (creatingTask) return;
        setCreatingTask(true);
        setTaskError(null);
        void handleNewThread(scopeProjectRef(project.environmentId, project.id))
          .catch((reason: unknown) =>
            setTaskError(reason instanceof Error ? reason.message : "无法创建任务，请重试"),
          )
          .finally(() => setCreatingTask(false));
        setOpenMobile(false);
      }
    : activeSandbox
      ? () => {
          if (sidebarEnvironmentId) setActiveEnvironmentId(sidebarEnvironmentId);
          void navigate({ to: "/sandboxes/$sandboxId", params: { sandboxId: activeSandbox.id } });
        }
      : undefined;
  const openFiles = currentThreadRef
    ? () => {
        useRightPanelStore.getState().open(currentThreadRef, "files");
        if (!isChatRoute && sidebarEnvironmentId)
          void navigate(workspaceTargetForEnvironment(sidebarEnvironmentId));
        setOpenMobile(false);
      }
    : project
      ? () => {
          void handleNewThread(scopeProjectRef(project.environmentId, project.id))
            .then((result) => {
              if (result)
                useRightPanelStore
                  .getState()
                  .open(scopeThreadRef(project.environmentId, result.threadId), "files");
            })
            .catch((reason: unknown) =>
              setTaskError(reason instanceof Error ? reason.message : "无法打开文件"),
            );
        }
      : newTask
        ? () => {
            setOpenFilesWhenReady(true);
            newTask();
          }
        : undefined;
  const openConnections = activeSandbox
    ? () => {
        void navigate({
          to: "/sandbox-connections/$sandboxId",
          params: { sandboxId: activeSandbox.id },
        });
        setOpenMobile(false);
      }
    : undefined;

  if (activeSandbox) {
    return (
      <SandboxWorkspaceSidebar
        sandbox={activeSandbox}
        location={mockSandbox || localRuntime.record ? "local" : platform.record ? "cloud" : null}
        activeThreadId={activeThreadId}
        threads={threads}
        activePane={activePane}
        activeTask={activeTask}
        onNavigate={() => setOpenMobile(false)}
        onNewTask={newTask}
        onOpenFiles={openFiles}
        onOpenConnections={openConnections}
        creatingTask={creatingTask}
        statusKnown={Boolean(resolvedSandbox)}
        resources={localRuntime.record?.resources ?? platform.record?.resources}
        error={taskError ?? connections.error}
        disconnecting={connections.disconnecting !== null}
        onDisconnect={() => {
          void connections.pauseAndDisconnect(activeSandbox.id).then((done) => {
            if (done) void navigate({ to: "/sandboxes" });
          });
        }}
      />
    );
  }

  return (
    <>
      <SidebarHeader
        className={cn("isodo-sidebar-header gap-4 px-4 pb-5", isElectron ? "drag-region" : "pt-5")}
        // Inline because utility padding is merged away by the sidebar's base
        // spacing; the traffic lights need a guaranteed strip of clear space.
        style={isElectron ? { paddingTop: 46 } : undefined}
      >
        <Link
          to="/sandboxes"
          className="flex items-center gap-2 text-base font-semibold"
          onClick={() => setOpenMobile(false)}
        >
          <img src={icon} alt="" className="size-7 rounded-lg" />
          isodo
        </Link>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              className="font-medium"
              render={<Link to="/sandboxes/new" />}
              onClick={() => setOpenMobile(false)}
            >
              <PlusIcon />
              创建沙箱
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent className="px-2">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              isActive={pathname === "/sandboxes"}
              render={<Link to="/sandboxes" />}
              onClick={() => setOpenMobile(false)}
            >
              <CloudIcon />
              我的沙箱
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton
              isActive={pathname === "/settings/sandbox"}
              render={<Link to="/settings/sandbox" />}
              onClick={() => setOpenMobile(false)}
            >
              <KeyRoundIcon />
              凭证库
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton disabled aria-label="Skill" title="暂未开放">
              <SparklesIcon />
              Skill
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton disabled aria-label="MCP" title="暂未开放">
              <NetworkIcon />
              MCP
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarContent>
      <SidebarFooter className="gap-2 border-t border-sidebar-border px-2 py-3">
        <details className="group/developer">
          <summary className="flex cursor-pointer list-none items-center gap-2 rounded-md px-3 py-2 text-sm font-medium hover:bg-sidebar-accent [&::-webkit-details-marker]:hidden">
            <Code2Icon className="size-4 text-muted-foreground" />
            开发者
            <ChevronDownIcon className="ml-auto size-4 text-muted-foreground transition-transform group-open/developer:rotate-180" />
          </summary>
          <SidebarMenu className="mt-1 border-l border-sidebar-border pl-2">
            <SidebarMenuItem>
              <SidebarMenuButton
                isActive={pathname === "/developer/api-keys"}
                render={<Link to="/developer/api-keys" />}
                onClick={() => setOpenMobile(false)}
              >
                <KeyRoundIcon />
                API 密钥
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                render={<a href={DOCS_URL} target="_blank" rel="noreferrer" />}
                onClick={() => setOpenMobile(false)}
              >
                <BookOpenIcon />
                文档
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </details>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              isActive={pathname === "/developer/settings"}
              render={<Link to="/developer/settings" />}
              onClick={() => setOpenMobile(false)}
            >
              <SettingsIcon />
              设置
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
        <div className="mt-1 border-t border-sidebar-border pt-2">
          <AccountMenu />
        </div>
      </SidebarFooter>
    </>
  );
}
