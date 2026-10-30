import type { EnvironmentId, ProjectId } from "@t3tools/contracts";
import { isSandboxUrl } from "@t3tools/shared/sandbox";

// A sandbox keeps its project in a directory *below* /workspace: the workspace
// itself also holds the runtime (.isodo/t3, .t3, .codex), and indexing that as
// project files fails. /home/user* was the upstream cloud layout.
export const LEGACY_SANDBOX_PROJECT_WORKSPACE_ROOT = "/home/user";
export const SANDBOX_PROJECT_WORKSPACE_ROOT = "/workspace/project";

export function isSandboxEnvironment<T extends { readonly displayUrl: string | null }>(
  environment: T,
  isMappedSandbox = false,
): boolean {
  return (
    isMappedSandbox || (environment.displayUrl !== null && isSandboxUrl(environment.displayUrl))
  );
}

/** Roots that must be migrated to SANDBOX_PROJECT_WORKSPACE_ROOT. */
export function isLegacySandboxWorkspaceRoot(workspaceRoot: string): boolean {
  return (
    workspaceRoot === LEGACY_SANDBOX_PROJECT_WORKSPACE_ROOT ||
    workspaceRoot === "/home/user/project" ||
    workspaceRoot === "/workspace"
  );
}

export function findSandboxProject<
  T extends { readonly environmentId: EnvironmentId } & { readonly id: ProjectId },
>(projects: ReadonlyArray<T>, environmentId: EnvironmentId): T | null {
  return projects.find((project) => project.environmentId === environmentId) ?? null;
}

export function findLegacySandboxProject<
  T extends {
    readonly environmentId: EnvironmentId;
    readonly workspaceRoot: string;
  },
>(projects: ReadonlyArray<T>, sandboxEnvironmentIds: ReadonlySet<EnvironmentId>): T | null {
  return (
    projects.find(
      (project) =>
        sandboxEnvironmentIds.has(project.environmentId) &&
        isLegacySandboxWorkspaceRoot(project.workspaceRoot) &&
        !projects.some(
          (candidate) =>
            candidate.environmentId === project.environmentId &&
            candidate.workspaceRoot === SANDBOX_PROJECT_WORKSPACE_ROOT,
        ),
    ) ?? null
  );
}

export function selectProjectsForActiveEnvironment<
  T extends { readonly environmentId: EnvironmentId },
>(projects: ReadonlyArray<T>, activeEnvironmentId: EnvironmentId | null): ReadonlyArray<T> {
  if (activeEnvironmentId === null) return projects;
  return projects.filter((project) => project.environmentId === activeEnvironmentId);
}

export function findConnectedSandboxMissingProject<
  T extends {
    readonly environmentId: EnvironmentId;
    readonly displayUrl: string | null;
    readonly connection: { readonly phase: string };
  },
  P extends { readonly environmentId: EnvironmentId },
>(environments: ReadonlyArray<T>, projects: ReadonlyArray<P>): T | null {
  return (
    environments.find(
      (environment) =>
        environment.connection.phase === "connected" &&
        environment.displayUrl !== null &&
        isSandboxUrl(environment.displayUrl) &&
        !projects.some((project) => project.environmentId === environment.environmentId),
    ) ?? null
  );
}

export function buildSandboxProjectCreateInput(projectId: ProjectId) {
  return {
    projectId,
    title: "isodo Sandbox",
    workspaceRoot: SANDBOX_PROJECT_WORKSPACE_ROOT,
    createWorkspaceRootIfMissing: true,
    defaultModelSelection: null,
  } as const;
}
