import { QueryClient, queryOptions, useQuery } from "@tanstack/react-query";
import { listSandboxes, type Sandbox } from "../../api/public.generated";
import { getAuthGeneration, useAuth } from "./auth";

export interface LocalSandboxItem {
  readonly id: string;
  readonly name?: string;
  readonly state?: string;
  readonly resources?: {
    readonly vcpu?: number;
    readonly memory_bytes?: number;
    readonly disk_bytes?: number;
  };
}

// One cache for the list, workspace and sidebar, retained across route changes.
export const sandboxQueryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      gcTime: 30 * 60_000,
      retry: false,
      refetchOnWindowFocus: false,
    },
  },
});
export const localSandboxesKey = ["isodo", "sandboxes", "local"] as const;
export const platformSandboxesPrefix = ["isodo", "sandboxes", "cloud"] as const;
export const localSandboxesOptions = queryOptions({
  queryKey: localSandboxesKey,
  queryFn: async (): Promise<LocalSandboxItem[]> => {
    const bridge = window.isodoDesktop;
    if (!bridge) return [];
    const result = (await bridge.listLocalSandboxes()) as { items?: LocalSandboxItem[] };
    return result.items ?? [];
  },
  refetchInterval: (query) =>
    query.state.data?.some((item) => ["creating", "starting"].includes(item.state ?? ""))
      ? 2_000
      : false,
});
export function useLocalSandboxes(enabled = true) {
  return useQuery(
    { ...localSandboxesOptions, enabled: enabled && Boolean(window.isodoDesktop) },
    sandboxQueryClient,
  );
}
export function platformSandboxRefetchInterval(
  items: readonly Pick<Sandbox, "desired_state" | "observed_state">[] | undefined,
): 2_000 | false {
  return items?.some((item) => {
    // Failed operations cannot converge without another user action.
    if (["failed", "error"].includes(item.observed_state)) return false;
    return (
      item.desired_state !== item.observed_state ||
      ["requested", "preparing", "starting"].includes(item.observed_state)
    );
  })
    ? 2_000
    : false;
}
export function usePlatformSandboxes(enabled = true) {
  const auth = useAuth();
  const queryKey = [
    ...platformSandboxesPrefix,
    getAuthGeneration(),
    auth.user?.provider ?? "",
    auth.user?.name ?? "",
  ] as const;
  const result = useQuery(
    {
      queryKey,
      enabled: enabled && auth.status === "authenticated",
      queryFn: async (): Promise<Sandbox[]> => (await listSandboxes({ limit: 100 })).items,
      refetchInterval: (query) => platformSandboxRefetchInterval(query.state.data),
    },
    sandboxQueryClient,
  );
  return { ...result, queryKey };
}
export function updateLocalSandboxCache(
  update: (items: LocalSandboxItem[] | null) => LocalSandboxItem[] | null,
) {
  void sandboxQueryClient.cancelQueries({ queryKey: localSandboxesKey });
  sandboxQueryClient.setQueryData<LocalSandboxItem[]>(
    localSandboxesKey,
    (items) => update(items ?? null) ?? undefined,
  );
}
export function invalidateSandboxLists() {
  void sandboxQueryClient.invalidateQueries({ queryKey: localSandboxesKey });
  void sandboxQueryClient.invalidateQueries({ queryKey: platformSandboxesPrefix });
}
