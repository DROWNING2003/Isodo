import { RefreshIcon } from "~/components/ui/refresh-icon";
import { scopeProjectRef } from "@t3tools/client-runtime/environment";
import type { EnvironmentId } from "@t3tools/contracts";
import {
  createFileRoute,
  Link,
  Navigate,
  useNavigate,
  useRouteContext,
} from "@tanstack/react-router";
import { CloudIcon, LinkIcon, LoaderIcon, PlusIcon } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { openCommandPalette } from "../commandPaletteBus";
import { sortScopedProjectsForSidebar } from "../components/Sidebar.logic";
import { Button } from "../components/ui/button";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "../components/ui/empty";
import { SidebarInset } from "../components/ui/sidebar";
import { WorkspacePageHeader } from "../components/WorkspacePageHeader";
import { useNewThreadHandler } from "../hooks/useHandleNewThread";
import { newProjectId } from "../lib/utils";
import {
  buildSandboxProjectCreateInput,
  isSandboxEnvironment,
  findLegacySandboxProject,
  SANDBOX_PROJECT_WORKSPACE_ROOT,
  selectProjectsForActiveEnvironment,
} from "../sandboxProject";
import {
  useAllEnvironmentShellsBootstrapped,
  useActiveEnvironmentId,
  useProjects,
  useThreadShells,
} from "../state/entities";
import { useEnvironments } from "../state/environments";
import { projectEnvironment } from "../state/projects";
import { useEnvironmentQuery } from "../state/query";
import { useAtomCommand } from "../state/use-atom-command";
import { vcsEnvironment } from "../state/vcs";
import { APP_DISPLAY_NAME } from "~/branding";
import { hasCloudPublicConfig } from "~/cloud/publicConfig";
import {
  resolveSandboxWebNoProjectsAction,
  shouldRedirectSandboxWebChatIndex,
} from "~/sandboxWebRouting";
import { isSandboxOnlyWeb, webSurface } from "~/webSurface";
import {
  environmentIdForSandbox,
  sandboxIdForEnvironment,
} from "../features/isodo/sandboxEnvironmentMap";
import { getActiveWorkspaceEntryTrace } from "../features/isodo/workspaceEntryTrace";

function isIsodoSandboxEnvironment(environment: {
  readonly environmentId: EnvironmentId;
  readonly displayUrl: string | null;
}): boolean {
  return isSandboxEnvironment(
    environment,
    sandboxIdForEnvironment(environment.environmentId) !== null,
  );
}

export function ChatIndexRouteView() {
  const { authGateState } = useRouteContext({ from: "__root__" });
  const { environments, isReady } = useEnvironments();
  if (isSandboxOnlyWeb) {
    const sandboxCount = environments.filter(isIsodoSandboxEnvironment).length;
    if (shouldRedirectSandboxWebChatIndex({ catalogReady: isReady, sandboxCount })) {
      return <Navigate to="/sandboxes" replace />;
    }
  }

  if (authGateState.status === "hosted-static") {
    if (!isReady) return <WorkspaceLoading />;
    if (environments.length === 0) return <HostedStaticOnboardingState />;
  }

  return <IndexDraftLanding />;
}

/**
 * Landing on the index route drops straight into a draft thread for the most
 * recently active project, so the first screen is a prompt instead of a dead
 * end. Falls back to an add-project hero when no project exists yet.
 */
function IndexDraftLanding() {
  const allProjects = useProjects();
  const allThreads = useThreadShells();
  const { environments } = useEnvironments();
  const activeEnvironmentId = useActiveEnvironmentId();
  const bootstrapped = useAllEnvironmentShellsBootstrapped();
  const handleNewThread = useNewThreadHandler();
  const createProject = useAtomCommand(projectEnvironment.create, { reportFailure: false });
  const updateProject = useAtomCommand(projectEnvironment.update, { reportFailure: false });
  const startingRef = useRef(false);
  const creatingSandboxProjectsRef = useRef(new Set<string>());
  const migratedLegacySandboxProjectIdsRef = useRef(new Set<string>());
  const [sandboxProjectRetryRequest, setSandboxProjectRetryRequest] = useState(0);
  const [sandboxProjectFailure, setSandboxProjectFailure] = useState<string | null>(null);
  const [startState, setStartState] = useState({ failed: false, retryRequest: 0 });
  const sandboxEnvironmentIds = useMemo(
    () =>
      new Set(
        environments
          .filter(isIsodoSandboxEnvironment)
          .map((environment) => environment.environmentId),
      ),
    [environments],
  );
  const projects = useMemo(() => {
    const visibleProjects = isSandboxOnlyWeb
      ? allProjects.filter((project) => sandboxEnvironmentIds.has(project.environmentId))
      : allProjects;
    return selectProjectsForActiveEnvironment(visibleProjects, activeEnvironmentId);
  }, [activeEnvironmentId, allProjects, sandboxEnvironmentIds]);
  const threads = useMemo(
    () =>
      isSandboxOnlyWeb
        ? allThreads.filter((thread) => sandboxEnvironmentIds.has(thread.environmentId))
        : allThreads,
    [allThreads, sandboxEnvironmentIds],
  );
  const legacySandboxProject = useMemo(
    () => findLegacySandboxProject(allProjects, sandboxEnvironmentIds),
    [allProjects, sandboxEnvironmentIds],
  );
  const entrySandboxId = getActiveWorkspaceEntryTrace()?.sandboxId;
  const entryEnvironmentId = entrySandboxId
    ? environmentIdForSandbox(
        entrySandboxId,
        environments.map((environment) => environment.environmentId),
      )
    : null;
  const activeSandboxEnvironment =
    environments.find((environment) => environment.environmentId === entryEnvironmentId) ??
    (activeEnvironmentId === null
      ? environments.find(isIsodoSandboxEnvironment)
      : environments.find(
          (environment) =>
            environment.environmentId === activeEnvironmentId &&
            isIsodoSandboxEnvironment(environment),
        ));
  const activeSandboxHasProject =
    activeSandboxEnvironment !== undefined &&
    allProjects.some((project) => project.environmentId === activeSandboxEnvironment.environmentId);

  const mostRecentProject = useMemo(
    () =>
      bootstrapped
        ? (sortScopedProjectsForSidebar(projects, threads, "updated_at")[0] ?? null)
        : null,
    [bootstrapped, projects, threads],
  );

  useEffect(() => {
    const environment = environments.find(
      (candidate) =>
        candidate.connection.phase === "connected" &&
        isIsodoSandboxEnvironment(candidate) &&
        !allProjects.some((project) => project.environmentId === candidate.environmentId),
    );
    if (
      environment === undefined ||
      creatingSandboxProjectsRef.current.has(environment.environmentId)
    ) {
      return;
    }
    creatingSandboxProjectsRef.current.add(environment.environmentId);
    setSandboxProjectFailure(null);
    void createProject({
      environmentId: environment.environmentId,
      input: buildSandboxProjectCreateInput(newProjectId()),
    })
      .then((result) => {
        if (result._tag === "Failure") {
          creatingSandboxProjectsRef.current.delete(environment.environmentId);
          setSandboxProjectFailure(environment.environmentId);
        }
      })
      .catch(() => {
        creatingSandboxProjectsRef.current.delete(environment.environmentId);
        setSandboxProjectFailure(environment.environmentId);
      });
  }, [allProjects, createProject, environments, sandboxProjectRetryRequest]);

  useEffect(() => {
    if (
      sandboxProjectFailure &&
      allProjects.some((project) => project.environmentId === sandboxProjectFailure)
    ) {
      setSandboxProjectFailure(null);
    }
    for (const environmentId of creatingSandboxProjectsRef.current) {
      if (allProjects.some((project) => project.environmentId === environmentId)) {
        creatingSandboxProjectsRef.current.delete(environmentId);
      }
    }
  }, [allProjects, sandboxProjectFailure]);

  useEffect(() => {
    // Isodo sandboxes always keep the workspace at /workspace, so an upstream
    // root is migrated without probing the guest for a git repository.
    if (
      legacySandboxProject === null ||
      migratedLegacySandboxProjectIdsRef.current.has(legacySandboxProject.id)
    ) {
      return;
    }
    migratedLegacySandboxProjectIdsRef.current.add(legacySandboxProject.id);
    void updateProject({
      environmentId: legacySandboxProject.environmentId,
      input: {
        projectId: legacySandboxProject.id,
        workspaceRoot: SANDBOX_PROJECT_WORKSPACE_ROOT,
      },
    });
  }, [legacySandboxProject, updateProject]);

  useEffect(() => {
    if (mostRecentProject === null || startingRef.current) {
      return;
    }
    startingRef.current = true;
    void handleNewThread(scopeProjectRef(mostRecentProject.environmentId, mostRecentProject.id), {
      replace: true,
    })
      .then((result) => {
        if (result === null) {
          startingRef.current = false;
          setStartState((state) => ({ ...state, failed: true }));
        }
      })
      .catch(() => {
        startingRef.current = false;
        setStartState((state) => ({ ...state, failed: true }));
      });
  }, [handleNewThread, mostRecentProject, startState.retryRequest]);

  if (!bootstrapped) {
    return <WorkspaceLoading />;
  }
  if (activeSandboxEnvironment && !activeSandboxHasProject) {
    if (sandboxProjectFailure === activeSandboxEnvironment.environmentId) {
      return (
        <SidebarInset className="h-dvh min-h-0 overflow-hidden bg-background text-foreground">
          <Empty className="flex-1">
            <EmptyHeader className="max-w-md">
              <EmptyTitle className="text-foreground text-xl">无法初始化沙箱工作区</EmptyTitle>
              <EmptyDescription className="mt-2 text-sm text-muted-foreground/78">
                沙箱已连接，但默认项目创建失败。请重试连接工作区。
              </EmptyDescription>
              <div className="mt-5 flex justify-center">
                <Button
                  size="sm"
                  onClick={() => {
                    setSandboxProjectFailure(null);
                    setSandboxProjectRetryRequest((request) => request + 1);
                  }}
                >
                  重试
                </Button>
              </div>
            </EmptyHeader>
          </Empty>
        </SidebarInset>
      );
    }
    return (
      <WorkspaceLoading
        message={
          activeSandboxEnvironment.connection.phase === "connected"
            ? "正在初始化沙箱工作区…"
            : "正在连接沙箱…"
        }
      />
    );
  }
  if (mostRecentProject !== null) {
    return startState.failed ? (
      <DraftStartError
        onRetry={() => {
          setStartState((state) => ({
            failed: false,
            retryRequest: state.retryRequest + 1,
          }));
        }}
      />
    ) : (
      <WorkspaceLoading message="正在创建任务…" />
    );
  }
  // First-run routing to the welcome wizard happens in FirstRunGate at the
  // root, before this route ever renders.
  return <NoProjectsHero />;
}

function WorkspaceLoading({ message = "正在准备沙箱工作台…" }: { message?: string }) {
  return (
    <SidebarInset className="h-dvh min-h-0 overflow-hidden bg-background text-foreground">
      <div
        className="flex flex-1 items-center justify-center gap-2 text-sm text-muted-foreground"
        role="status"
      >
        <LoaderIcon className="size-4 animate-spin" />
        {message}
      </div>
    </SidebarInset>
  );
}

function DraftStartError({ onRetry }: { readonly onRetry: () => void }) {
  return (
    <SidebarInset className="h-dvh min-h-0 overflow-hidden overscroll-y-none bg-background text-foreground">
      <Empty className="flex-1">
        <EmptyHeader className="max-w-md">
          <EmptyTitle className="text-foreground text-xl">Couldn’t start a new thread</EmptyTitle>
          <EmptyDescription className="mt-2 text-sm text-muted-foreground/78">
            The project is still available. Try opening the draft again.
          </EmptyDescription>
          <div className="mt-5 flex justify-center">
            <Button size="sm" onClick={onRetry}>
              <RefreshIcon className="size-4" />
              Try again
            </Button>
          </div>
        </EmptyHeader>
      </Empty>
    </SidebarInset>
  );
}

function NoProjectsHero() {
  const navigate = useNavigate();
  const openAddProject = useCallback(() => openCommandPalette({ open: "add-project" }), []);
  const action = resolveSandboxWebNoProjectsAction(webSurface);
  const openSandboxes = useCallback(() => void navigate({ to: "/sandboxes" }), [navigate]);
  const isSandboxAction = action === "sandboxes";

  return (
    <SidebarInset className="h-dvh min-h-0 overflow-hidden overscroll-y-none bg-background text-foreground">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-x-hidden bg-background">
        <Empty className="flex-1">
          <div className="w-full max-w-lg px-8 py-12">
            <EmptyHeader className="max-w-none">
              <EmptyTitle className="text-foreground text-2xl sm:text-3xl">
                {isSandboxAction ? "沙箱中还没有项目" : "What should we work on?"}
              </EmptyTitle>
              <EmptyDescription className="mt-2 text-sm text-muted-foreground/78">
                {isSandboxAction
                  ? "请检查沙箱状态，或创建一个新的沙箱。"
                  : "Add a project to start your first thread."}
              </EmptyDescription>
              <div className="mt-6 flex justify-center">
                <Button size="sm" onClick={isSandboxAction ? openSandboxes : openAddProject}>
                  {isSandboxAction ? (
                    <CloudIcon className="size-4" />
                  ) : (
                    <PlusIcon className="size-4" />
                  )}
                  {isSandboxAction ? "查看沙箱" : "Add project"}
                </Button>
              </div>
            </EmptyHeader>
          </div>
        </Empty>
      </div>
    </SidebarInset>
  );
}

export const Route = createFileRoute("/_chat/")({
  component: ChatIndexRouteView,
});

function HostedStaticOnboardingState() {
  const cloudEnabled = hasCloudPublicConfig();

  return (
    <SidebarInset className="h-dvh min-h-0 overflow-hidden overscroll-y-none bg-background text-foreground">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-x-hidden bg-background">
        <WorkspacePageHeader className="border-b border-border">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-foreground md:text-muted-foreground/60">
              {APP_DISPLAY_NAME}
            </span>
          </div>
        </WorkspacePageHeader>

        <Empty className="flex-1">
          <div className="w-full max-w-xl rounded-3xl border border-border/55 bg-card/20 px-8 py-12 shadow-sm/5">
            <EmptyHeader className="max-w-none">
              <div className="mx-auto mb-5 flex size-11 items-center justify-center rounded-xl border border-border/70 bg-background/70 text-muted-foreground">
                <LinkIcon className="size-5" />
              </div>
              <EmptyTitle className="text-foreground text-xl">
                Connect to a computer running T3 Code
              </EmptyTitle>
              <EmptyDescription className="mt-2 text-sm leading-relaxed text-muted-foreground/78">
                This browser connects to T3 Code running on your computer or a server. Start the T3
                Code desktop app or command-line server on that machine and keep it running.
              </EmptyDescription>
              <EmptyDescription className="mt-2 text-sm leading-relaxed text-muted-foreground/78">
                {cloudEnabled
                  ? "Sign in to isodo Connect to connect a linked environment through its managed tunnel, or add a reachable backend manually."
                  : "Add a reachable backend manually to start working from this browser."}
              </EmptyDescription>
              <div className="mt-6 flex justify-center">
                <Button render={<Link to="/settings/connections" />} size="sm">
                  <PlusIcon className="size-4" />
                  Open Connections
                </Button>
              </div>
            </EmptyHeader>
          </div>
        </Empty>
      </div>
    </SidebarInset>
  );
}
