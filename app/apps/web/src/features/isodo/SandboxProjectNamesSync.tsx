import { SANDBOX_PROJECT_WORKSPACE_ROOT, isLegacySandboxWorkspaceRoot } from "../../sandboxProject";
import { useEffect, useRef } from "react";
import { useProjects } from "../../state/entities";
import { useEnvironments } from "../../state/environments";
import { projectEnvironment } from "../../state/projects";
import { useAtomCommand } from "../../state/use-atom-command";
import { sandboxIdForEnvironment } from "./sandboxEnvironmentMap";
import { useLocalSandboxes, usePlatformSandboxes } from "./sandboxQueries";

export function SandboxProjectNamesSync() {
  const projects = useProjects();
  const { environments } = useEnvironments();
  const local = useLocalSandboxes();
  const cloud = usePlatformSandboxes();
  const update = useAtomCommand(projectEnvironment.update, "同步沙箱名称");
  const attempted = useRef(new Set<string>());
  useEffect(() => {
    for (const project of projects) {
      if (
        project.title !== "isodo Sandbox" ||
        (project.workspaceRoot !== SANDBOX_PROJECT_WORKSPACE_ROOT &&
          !isLegacySandboxWorkspaceRoot(project.workspaceRoot))
      )
        continue;
      if (
        !environments.some(
          (environment) =>
            environment.environmentId === project.environmentId &&
            environment.connection.phase === "connected",
        )
      )
        continue;
      const sandboxId = sandboxIdForEnvironment(project.environmentId);
      if (!sandboxId) continue;
      const name = (
        local.data?.find((sandbox) => sandbox.id === sandboxId)?.name ??
        cloud.data?.find((sandbox) => sandbox.id === sandboxId)?.name
      )?.trim();
      if (!name || name === project.title) continue;
      const key = `${project.environmentId}:${project.id}:${name}`;
      if (attempted.current.has(key)) continue;
      attempted.current.add(key);
      void update({
        environmentId: project.environmentId,
        input: { projectId: project.id, title: name },
      });
    }
  }, [projects, environments, local.data, cloud.data, update]);
  return null;
}
