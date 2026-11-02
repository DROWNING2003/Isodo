import { useQuery } from "@tanstack/react-query";
import type { EnvironmentId, ServerProviderSkill } from "@t3tools/contracts";
import { getSandboxSkills, listSkills } from "../../api/public.generated";
import { getAuthGeneration, useAuth } from "./auth";
import { sandboxIdForEnvironment } from "./sandboxEnvironmentMap";
import { skillKey } from "./skillMarket";
import { mergeManagedSkills } from "./managedSkills";

export function useManagedSkills(
  environmentId: EnvironmentId,
  codex: boolean,
  cwd: string | null | undefined,
  native: readonly ServerProviderSkill[],
) {
  const auth = useAuth();
  const sandbox = sandboxIdForEnvironment(environmentId);
  const remote = Boolean(
    sandbox &&
    !sandbox.startsWith("local-") &&
    codex &&
    (cwd === "/workspace" || cwd?.startsWith("/workspace/")),
  );
  const enabled = remote && auth.status === "authenticated";
  const generation = getAuthGeneration();
  const catalog = useQuery({
    queryKey: [...skillKey, generation],
    enabled,
    queryFn: () => listSkills(),
    staleTime: 5000,
  });
  const selection = useQuery({
    queryKey: [...skillKey, generation, "sandbox", sandbox],
    enabled,
    queryFn: () => getSandboxSkills(sandbox!),
    refetchInterval: 5000,
  });
  if (!remote) return native;
  return mergeManagedSkills(
    native,
    enabled && !catalog.isError ? (catalog.data?.items ?? []) : [],
    enabled && !selection.isError ? selection.data : undefined,
  );
}
