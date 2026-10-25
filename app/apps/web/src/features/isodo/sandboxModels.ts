import {
  defaultInstanceIdForDriver,
  ProviderDriverKind,
  type ServerSettings,
  type ServerSettingsPatch,
} from "@t3tools/contracts";
import { type AgentConfig } from "./agentConfigs";

export type AppliedAgentModels = Pick<AgentConfig, "id" | "agent" | "modelNames" | "defaultModel">;
const record = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
/** Only the target environment's managed catalog is replaced; authored models remain. */
export function sandboxModelSettingsPatch(
  settings: ServerSettings,
  agents: readonly AppliedAgentModels[],
): ServerSettingsPatch | null {
  const instances = { ...settings.providerInstances };
  let changed = false;
  for (const agent of ["codex", "claude", "opencode", "cursor", "grok"] as const) {
    const driverKey = agent === "claude" ? "claudeAgent" : agent;
    const driver = ProviderDriverKind.make(driverKey);
    const id = defaultInstanceIdForDriver(driver);
    const previous = instances[id];
    const config = record(previous?.config ?? settings.providers[driverKey]);
    const owned = Array.isArray(config.isodoManagedModelSlugs) ? config.isodoManagedModelSlugs : [];
    const selected = agents.filter((item) => item.agent === agent);
    if (!selected.length && !owned.length) continue;
    const models = Array.isArray(config.customModels) ? config.customModels : [];
    const retained = models.filter((item) => !owned.includes(record(item).slug));
    const slugs = [
      ...new Set(
        selected
          .flatMap((item) => [...item.modelNames, item.defaultModel])
          .filter(Boolean)
          .map((slug) =>
            agent === "opencode" && !slug.startsWith("isodo/") ? `isodo/${slug}` : slug,
          ),
      ),
    ];
    const additions = slugs
      .filter(
        (slug) =>
          !retained.some((item) => (typeof item === "string" ? item : record(item).slug) === slug),
      )
      .map((slug) => ({ slug, name: slug }));
    const { enabled: _enabled, ...driverConfig } = config;
    const next = {
      ...previous,
      driver,
      enabled: previous?.enabled ?? (selected.length ? true : config.enabled !== false),
      config: {
        ...driverConfig,
        customModels: [...retained, ...additions],
        isodoManagedModelSlugs: additions.map((item) => item.slug),
      },
    };
    if (JSON.stringify(previous) !== JSON.stringify(next)) {
      instances[id] = next;
      changed = true;
    }
  }
  return changed ? { providerInstances: instances } : null;
}
