import { act } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vite-plus/test";
const fixture = vi.hoisted(() => ({
  update: vi.fn().mockResolvedValue({ _tag: "Success" }),
  projects: [
    {
      id: "project-a",
      environmentId: "env-a",
      title: "isodo Sandbox",
      workspaceRoot: "/workspace/project",
    },
    {
      id: "project-b",
      environmentId: "env-b",
      title: "isodo Sandbox",
      workspaceRoot: "/workspace",
    },
    {
      id: "custom",
      environmentId: "env-a",
      title: "My custom project",
      workspaceRoot: "/workspace",
    },
  ],
}));
vi.mock("../../state/entities", () => ({ useProjects: () => fixture.projects }));
vi.mock("../../state/environments", () => ({
  useEnvironments: () => ({
    environments: [
      { environmentId: "env-a", connection: { phase: "connected" } },
      { environmentId: "env-b", connection: { phase: "connected" } },
    ],
  }),
}));
vi.mock("../../state/projects", () => ({ projectEnvironment: { update: Symbol("update") } }));
vi.mock("../../state/use-atom-command", () => ({ useAtomCommand: () => fixture.update }));
vi.mock("./sandboxEnvironmentMap", () => ({
  sandboxIdForEnvironment: (id: string) => (id === "env-a" ? "local-a" : "local-b"),
}));
vi.mock("./sandboxQueries", () => ({
  useLocalSandboxes: () => ({
    data: [
      { id: "local-a", name: "fenno-test" },
      { id: "local-b", name: "fenno" },
    ],
  }),
  usePlatformSandboxes: () => ({ data: [] }),
}));
import { SandboxProjectNamesSync } from "./SandboxProjectNamesSync";
let renderer: ReactTestRenderer;
afterEach(async () => {
  await act(async () => renderer?.unmount());
  vi.unstubAllGlobals();
});
it("persists each sandbox's own name for its default project and leaves custom titles intact", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  await act(async () => {
    renderer = create(<SandboxProjectNamesSync />);
  });
  expect(fixture.update).toHaveBeenCalledTimes(2);
  expect(fixture.update).toHaveBeenCalledWith({
    environmentId: "env-a",
    input: { projectId: "project-a", title: "fenno-test" },
  });
  expect(fixture.update).toHaveBeenCalledWith({
    environmentId: "env-b",
    input: { projectId: "project-b", title: "fenno" },
  });
  await act(async () => renderer.update(<SandboxProjectNamesSync />));
  expect(fixture.update).toHaveBeenCalledTimes(2);
});
