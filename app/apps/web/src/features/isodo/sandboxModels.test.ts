import { deriveProviderInstanceEntries } from "../../providerInstances";
import { DEFAULT_UNIFIED_SETTINGS } from "@t3tools/contracts/settings";
import { getAppModelOptionsForInstance } from "../../modelSelection";
import * as Schema from "effect/Schema";
import { describe, expect, it } from "vite-plus/test";
import {
  DEFAULT_SERVER_SETTINGS,
  defaultInstanceIdForDriver,
  ProviderDriverKind,
} from "@t3tools/contracts";
import { sandboxModelSettingsPatch } from "./sandboxModels";

const id = defaultInstanceIdForDriver(Schema.decodeUnknownSync(ProviderDriverKind)("codex"));
const agent = {
  id: "work",
  agent: "codex" as const,
  modelNames: ["deepseek-test"],
  defaultModel: "deepseek-test",
};
describe("sandbox model catalog", () => {
  it("publishes applied models to the default provider instance", () => {
    const patch = sandboxModelSettingsPatch(DEFAULT_SERVER_SETTINGS, [agent]);
    expect(
      getAppModelOptionsForInstance(
        { ...DEFAULT_UNIFIED_SETTINGS, providerInstances: patch!.providerInstances! },
        deriveProviderInstanceEntries([
          {
            instanceId: id,
            driver: ProviderDriverKind.make("codex"),
            enabled: true,
            installed: true,
            version: null,
            status: "ready",
            auth: { status: "authenticated" },
            checkedAt: "2026-10-25T00:00:00.000Z",
            models: [],
            slashCommands: [],
            skills: [],
          },
        ])[0]!,
      ).map((model) => model.slug),
    ).toContain("deepseek-test");
    expect(patch?.providerInstances?.[id]?.config).toMatchObject({
      customModels: [{ slug: "deepseek-test", name: "deepseek-test" }],
    });
  });
  it("removes only managed models and preserves authored settings", () => {
    const settings = {
      ...DEFAULT_SERVER_SETTINGS,
      providerInstances: {
        [id]: {
          driver: Schema.decodeUnknownSync(ProviderDriverKind)("codex"),
          config: {
            binaryPath: "/custom/codex",
            customModels: [
              { slug: "authored", name: "Mine" },
              { slug: "old", name: "old" },
            ],
            isodoManagedModelSlugs: ["old"],
          },
        },
      },
    };
    const patch = sandboxModelSettingsPatch(settings, []);
    expect(patch?.providerInstances?.[id]?.config).toMatchObject({
      binaryPath: "/custom/codex",
      customModels: [{ slug: "authored", name: "Mine" }],
    });
    expect(
      sandboxModelSettingsPatch({ ...settings, providerInstances: patch!.providerInstances! }, []),
    ).toBeNull();
  });
  it("does not copy models between sandbox catalogs", () => {
    expect(sandboxModelSettingsPatch(DEFAULT_SERVER_SETTINGS, [])).toBeNull();
    const patch = sandboxModelSettingsPatch(DEFAULT_SERVER_SETTINGS, [agent]);
    expect(
      sandboxModelSettingsPatch(
        { ...DEFAULT_SERVER_SETTINGS, providerInstances: patch!.providerInstances! },
        [agent],
      ),
    ).toBeNull();
  });
});
