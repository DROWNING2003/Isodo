import { EnvironmentId } from "@t3tools/contracts";
import { afterEach, expect, it, vi } from "vite-plus/test";
const query = vi.hoisted(() => ({ execute: vi.fn() }));
vi.mock("@t3tools/client-runtime/state/runtime", () => ({ executeAtomQuery: query.execute }));
vi.mock("~/rpc/atomRegistry", () => ({ appAtomRegistry: {} }));
vi.mock("~/components/files/projectFilesQueryState", () => ({
  getProjectFileQueryAtom: () => Symbol("file"),
  resolveProjectFileQueryData: (_env: unknown, _cwd: unknown, _path: unknown, data: unknown) =>
    data,
}));
import { readT3ProjectFileDefaultThreadEnvMode } from "./t3ProjectFileDefaults";
afterEach(() => {
  vi.useRealTimers();
  query.execute.mockReset();
});
it("falls back when the optional project defaults read never responds", async () => {
  vi.useFakeTimers();
  query.execute.mockImplementation(
    (_registry, _atom, options) =>
      new Promise((resolve) => {
        options.signal?.addEventListener("abort", () => resolve({ _tag: "Interrupted" }));
      }),
  );
  let completed = false;
  const pending = readT3ProjectFileDefaultThreadEnvMode(EnvironmentId.make("env"), "/project").then(
    (result) => {
      completed = true;
      return result;
    },
  );
  await vi.advanceTimersByTimeAsync(2100);
  expect(completed).toBe(true);
  await expect(pending).resolves.toBeNull();
});
it("keeps a project file's explicit environment mode when the read succeeds", async () => {
  query.execute.mockResolvedValue({
    _tag: "Success",
    value: { contents: '{"defaultThreadEnvMode":"worktree"}', truncated: false },
  });
  await expect(
    readT3ProjectFileDefaultThreadEnvMode(EnvironmentId.make("env"), "/project"),
  ).resolves.toBe("worktree");
});
