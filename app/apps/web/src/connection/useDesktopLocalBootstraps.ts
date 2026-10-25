import type { DesktopEnvironmentBootstrap } from "@t3tools/contracts";
const EMPTY: ReadonlyArray<DesktopEnvironmentBootstrap> = [];
// Kept for generic T3 remote UI consumers; Isodo has no host-managed runtimes.
export function useDesktopLocalBootstraps(): ReadonlyArray<DesktopEnvironmentBootstrap> {
  return EMPTY;
}
