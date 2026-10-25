import { afterEach, expect, it, vi } from "vite-plus/test";
const api = vi.hoisted(() => ({
  mkdir: vi.fn().mockResolvedValue({}),
  upload: vi
    .fn()
    .mockResolvedValue({ path: "/workspace/.codex/config.toml", bytes: 1, sha256: "digest" }),
}));
vi.mock("../../api/public.generated", () => ({
  makeSandboxDirectory: api.mkdir,
  uploadSandboxFile: api.upload,
}));
import {
  agentConfigurationPath,
  writeAgentConfigurationFile,
  writeSandboxFile,
} from "./sandboxFiles";
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});
it.each([
  ["~/.codex/config.toml", "/workspace/.codex/config.toml"],
  ["~/.codex/auth.json", "/workspace/.codex/auth.json"],
  ["~/.claude/settings.json", "/workspace/.home/.claude/settings.json"],
  ["~/.config/opencode/opencode.json", "/workspace/.home/.config/opencode/opencode.json"],
])("uses the actual Guest HOME layout for %s", (input, expected) =>
  expect(agentConfigurationPath(input)).toBe(expected),
);
it.each(["/etc/passwd", "../config", "~/.codex/../config", "/workspace/.codex/../config"])(
  "rejects unsafe configuration path %s",
  (path) => expect(() => agentConfigurationPath(path)).toThrow(),
);
it("does not send the next local chunk before ACK and preserves binary bytes", async () => {
  let acknowledge!: (value: { received: number }) => void;
  const append = vi
    .fn()
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          acknowledge = resolve;
        }),
    )
    .mockImplementation(async (_id, offset: number, data: Uint8Array) => ({
      received: offset + data.length,
    }));
  const finish = vi
    .fn()
    .mockResolvedValue({ path: "/workspace/a", bytes: 65537, sha256: "digest" });
  vi.stubGlobal("window", {
    isodoDesktop: {
      beginFileUpload: async () => ({ id: "upload", chunkBytes: 65536 }),
      appendFileUpload: append,
      finishFileUpload: finish,
      abortFileUpload: vi.fn(),
    },
  });
  const data = new Uint8Array(65537).fill(255);
  const operation = writeSandboxFile("local-a", "/workspace/a", new Blob([data]));
  await vi.waitFor(() => expect(append).toHaveBeenCalledOnce());
  expect(finish).not.toHaveBeenCalled();
  expect(append.mock.calls[0]?.[2]).toEqual(data.slice(0, 65536));
  acknowledge({ received: 65536 });
  await operation;
  expect(append).toHaveBeenCalledTimes(2);
  expect(append.mock.calls[1]?.[2]).toEqual(data.slice(65536));
  expect(finish).toHaveBeenCalledExactlyOnceWith("upload");
});
it("aborts a broken ACK without committing", async () => {
  const finish = vi.fn();
  const abort = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("window", {
    isodoDesktop: {
      beginFileUpload: async () => ({ id: "upload", chunkBytes: 65536 }),
      appendFileUpload: async () => ({ received: 0 }),
      finishFileUpload: finish,
      abortFileUpload: abort,
    },
  });
  await expect(writeSandboxFile("local-a", "/workspace/a", new Blob(["x"]))).rejects.toThrow(
    "确认",
  );
  expect(abort).toHaveBeenCalledExactlyOnceWith("upload");
  expect(finish).not.toHaveBeenCalled();
});
it("writes remote Agent config through directories and multipart Files", async () => {
  vi.stubGlobal("window", {});
  await writeAgentConfigurationFile("cloud-a", {
    path: "~/.codex/config.toml",
    content: 'model = "configured-model"',
  });
  expect(api.mkdir).toHaveBeenCalledWith("cloud-a", { path: "/workspace/.codex", parents: true });
  expect(api.upload).toHaveBeenCalledWith(
    "cloud-a",
    { file: expect.any(Blob) },
    { path: "/workspace/.codex/config.toml", overwrite: true },
    undefined,
  );
  expect(await api.upload.mock.calls[0]?.[1].file.text()).toContain("configured-model");
});
