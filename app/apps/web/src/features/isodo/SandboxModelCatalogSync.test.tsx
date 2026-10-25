import { act } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { DEFAULT_SERVER_SETTINGS } from "@t3tools/contracts";
const fixture = vi.hoisted(() => ({ update: vi.fn().mockResolvedValue({ _tag: "Success" }) }));
vi.mock("../../state/environments", () => ({
  useEnvironments: () => ({
    environments: [
      {
        environmentId: "env-a",
        connection: { phase: "connected" },
        serverConfig: { settings: DEFAULT_SERVER_SETTINGS },
      },
      {
        environmentId: "env-b",
        connection: { phase: "connected" },
        serverConfig: { settings: DEFAULT_SERVER_SETTINGS },
      },
      {
        environmentId: "env-offline",
        connection: { phase: "available" },
        serverConfig: { settings: DEFAULT_SERVER_SETTINGS },
      },
    ],
  }),
}));
vi.mock("../../state/server", () => ({ serverEnvironment: { updateSettings: Symbol("update") } }));
vi.mock("../../state/use-atom-command", () => ({ useAtomCommand: () => fixture.update }));
vi.mock("./sandboxEnvironmentMap", () => ({ sandboxIdForEnvironment: (id: string) => id }));
vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}));
vi.mock("./credentialDocuments", () => ({
  CREDENTIAL_DOCUMENTS_CHANGED: "changed",
  credentialDocumentsKey: ["docs"],
  useServerSandboxCredentials: () => ({
    data: {
      "env-a": {
        models: [
          { id: "codex", agent: "codex", modelNames: ["relay-model"], defaultModel: "relay-model" },
        ],
      },
    },
  }),
}));
import { SandboxModelCatalogSync } from "./SandboxModelCatalogSync";
let renderer: ReactTestRenderer;
afterEach(async () => {
  await act(async () => renderer?.unmount());
  vi.unstubAllGlobals();
});
it("writes only the connected sandbox catalog and never writes on unmount", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("window", { addEventListener: vi.fn(), removeEventListener: vi.fn() });
  await act(async () => {
    renderer = create(<SandboxModelCatalogSync />);
  });
  expect(fixture.update).toHaveBeenCalledTimes(1);
  expect(fixture.update.mock.calls[0]?.[0]).toMatchObject({ environmentId: "env-a" });
  await act(async () => renderer.unmount());
  expect(fixture.update).toHaveBeenCalledTimes(1);
});
