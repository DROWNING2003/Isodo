import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { listSkills, getSandboxSkills, setSandboxSkills } from "../../api/public.generated";
import { getAuthGeneration, useAuth } from "./auth";

export const skillKey = ["isodo", "skills"] as const;
export function useSkillMarket() {
  const auth = useAuth();
  return useQuery({
    queryKey: [...skillKey, getAuthGeneration()],
    enabled: auth.status === "authenticated",
    queryFn: () => listSkills(),
  });
}
export function useSandboxSkillSelection(sandboxId: string, enabled: boolean) {
  const auth = useAuth();
  const generation = getAuthGeneration();
  const client = useQueryClient();
  const catalog = useSkillMarket();
  const key = [...skillKey, generation, "sandbox", sandboxId];
  const saved = useQuery({
    queryKey: key,
    enabled: enabled && auth.status === "authenticated",
    queryFn: () => getSandboxSkills(sandboxId),
  });
  const [draft, setDraft] = useState<string[] | null>(null);
  useEffect(() => setDraft(null), [sandboxId, generation]);
  const selected = draft ?? saved.data?.skill_ids ?? [];
  const changed =
    draft !== null &&
    JSON.stringify([...selected].sort()) !==
      JSON.stringify([...(saved.data?.skill_ids ?? [])].sort());
  return {
    catalog,
    saved,
    selected,
    changed,
    loading: enabled && (catalog.isLoading || saved.isLoading),
    failed: enabled && (catalog.isError || saved.isError),
    toggle: (id: string, checked: boolean) =>
      setDraft(
        checked ? [...selected.filter((x) => x !== id), id] : selected.filter((x) => x !== id),
      ),
    apply: async () => {
      if (getAuthGeneration() !== generation) throw new Error("账号已切换，请重新选择 Skill");
      const result = await setSandboxSkills(sandboxId, { skill_ids: selected });
      if (getAuthGeneration() !== generation) throw new Error("账号已切换");
      client.setQueryData(key, result);
      if (result.sync_status !== "applied")
        throw new Error(
          "Skill 选择已保存，但尚未同步到沙箱。启动后请重新保存，失败时检查托管目录冲突。",
        );
      setDraft(null);
    },
  };
}
