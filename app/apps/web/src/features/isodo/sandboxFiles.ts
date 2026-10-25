import {
  makeSandboxDirectory,
  uploadSandboxFile,
  type FileWriteResult,
} from "../../api/public.generated";

const CHUNK_BYTES = 64 * 1024;
const MAX_FILE_BYTES = 256 * 1024 * 1024;
export interface FileWriteOptions {
  overwrite?: boolean;
  signal?: AbortSignal;
}

export function agentConfigurationPath(input: string): string {
  const value = input.trim();
  if (!value || /[\0\r\n]/.test(value)) throw new Error("Agent 配置路径无效。");
  const relative = value.startsWith("~/") ? value.slice(2) : value;
  if (
    relative.split("/").some((segment) => segment === ".." || segment === "." || segment === "")
  ) {
    // Absolute workspace paths are checked separately below.
    if (!value.startsWith("/workspace/")) throw new Error("Agent 配置路径不能包含目录跳转。");
  }
  if (value.startsWith("/workspace/")) return workspacePath(value);
  if (relative.startsWith("/") || relative.startsWith("~"))
    throw new Error("Agent 配置必须位于沙箱工作区或 HOME。");
  return workspacePath(
    relative.startsWith(".codex/") ? `/workspace/${relative}` : `/workspace/.home/${relative}`,
  );
}
function workspacePath(value: string): string {
  if (
    !value.startsWith("/workspace/") ||
    /[\0\r\n]/.test(value) ||
    value
      .slice(1)
      .split("/")
      .some((part) => !part || part === "." || part === "..")
  )
    throw new Error("文件路径必须是规范的 /workspace 绝对路径。");
  return value;
}
export async function makeSandboxDirectoryAt(sandboxId: string, path: string): Promise<void> {
  workspacePath(path);
  if (sandboxId.startsWith("local-")) {
    const bridge = window.isodoDesktop;
    if (!bridge) throw new Error("本地运行时不可用。");
    await bridge.sandboxRequest(
      `/v1/sandboxes/${encodeURIComponent(sandboxId)}/directories`,
      "POST",
      JSON.stringify({ path, parents: true }),
    );
  } else {
    await makeSandboxDirectory(sandboxId, { path, parents: true });
  }
}

/** Blob slices + ACK keep large local uploads out of a single IPC message. */
export async function writeSandboxFile(
  sandboxId: string,
  path: string,
  file: Blob,
  options: FileWriteOptions = {},
): Promise<FileWriteResult> {
  workspacePath(path);
  if (file.size > MAX_FILE_BYTES) throw new Error("文件不能超过 256 MiB。");
  options.signal?.throwIfAborted();
  if (!sandboxId.startsWith("local-"))
    return uploadSandboxFile(
      sandboxId,
      { file },
      { path, overwrite: options.overwrite ?? true },
      options.signal ? { signal: options.signal } : undefined,
    );
  const bridge = window.isodoDesktop;
  if (!bridge?.beginFileUpload) throw new Error("请更新桌面应用以使用文件上传。");
  const handle = await bridge.beginFileUpload({
    sandboxId,
    path,
    size: file.size,
    overwrite: options.overwrite ?? true,
  });
  const abort = () => {
    void bridge.abortFileUpload(handle.id).catch(() => {});
  };
  options.signal?.addEventListener("abort", abort, { once: true });
  try {
    const chunkBytes = Math.min(CHUNK_BYTES, handle.chunkBytes);
    if (!Number.isInteger(chunkBytes) || chunkBytes < 1) throw new Error("文件传输协议无效。");
    for (let offset = 0; offset < file.size; offset += chunkBytes) {
      options.signal?.throwIfAborted();
      const data = new Uint8Array(await file.slice(offset, offset + chunkBytes).arrayBuffer());
      const ack = await bridge.appendFileUpload(handle.id, offset, data);
      if (ack.received !== offset + data.byteLength) throw new Error("文件块确认无效。");
    }
    options.signal?.throwIfAborted();
    return await bridge.finishFileUpload(handle.id);
  } catch (error) {
    await bridge.abortFileUpload(handle.id).catch(() => {});
    throw error;
  } finally {
    options.signal?.removeEventListener("abort", abort);
  }
}

export async function writeAgentConfigurationFile(
  sandboxId: string,
  input: { path: string; content: string },
) {
  const path = agentConfigurationPath(input.path);
  const parent = path.slice(0, path.lastIndexOf("/"));
  if (parent !== "/workspace") await makeSandboxDirectoryAt(sandboxId, parent);
  const result = await writeSandboxFile(
    sandboxId,
    path,
    new Blob([input.content], { type: "application/octet-stream" }),
  );
  return { ...result, requires_restart: true };
}
