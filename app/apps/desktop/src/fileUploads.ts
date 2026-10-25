import * as NodeCrypto from "node:crypto";
import * as NodeFS from "node:fs/promises";
import * as NodeFSSync from "node:fs";
import * as NodeOS from "node:os";
import * as NodePath from "node:path";

export const FILE_CHUNK_BYTES = 64 * 1024;
export const MAX_FILE_BYTES = 256 * 1024 * 1024;
export type FileRequestInit = RequestInit & { duplex?: "half" };
type Request = (path: string, init: FileRequestInit) => Promise<unknown>;
interface Upload {
  owner: number;
  sandboxId: string;
  path: string;
  size: number;
  received: number;
  overwrite: boolean;
  directory: string;
  file: NodeFS.FileHandle;
  hash: NodeCrypto.Hash;
  busy: boolean;
  committing: boolean;
  timer: ReturnType<typeof setTimeout>;
  controller: AbortController;
}
export interface FileWriteResult {
  path: string;
  bytes: number;
  sha256: string;
}

function validateInput(value: unknown): {
  sandboxId: string;
  path: string;
  size: number;
  overwrite: boolean;
} {
  if (typeof value !== "object" || value === null) throw new Error("Invalid file upload");
  const input = value as Record<string, unknown>;
  if (
    typeof input.sandboxId !== "string" ||
    !/^[a-zA-Z0-9_-]{1,128}$/.test(input.sandboxId) ||
    typeof input.path !== "string" ||
    input.path.length > 4096 ||
    !input.path.startsWith("/workspace/") ||
    NodePath.posix.normalize(input.path) !== input.path ||
    /[\0\r\n]/.test(input.path) ||
    !Number.isSafeInteger(input.size) ||
    typeof input.size !== "number" ||
    input.size < 0 ||
    input.size > MAX_FILE_BYTES ||
    (input.overwrite !== undefined && typeof input.overwrite !== "boolean")
  )
    throw new Error("Invalid file upload target or size");
  return {
    sandboxId: input.sandboxId,
    path: input.path,
    size: input.size,
    overwrite: input.overwrite !== false,
  };
}

/** Main owns private temporary files. Renderer sends one bounded chunk per ACK. */
export class SandboxFileUploads {
  private readonly uploads = new Map<string, Upload>();
  private readonly pending = new Map<number, number>();
  private readonly closedOwners = new Set<number>();
  private readonly request: Request;
  constructor(request: Request) {
    this.request = request;
  }

  async begin(owner: number, value: unknown): Promise<{ id: string; chunkBytes: number }> {
    const input = validateInput(value);
    const reservations = [...this.pending.values()].reduce((sum, count) => sum + count, 0);
    const owned = [...this.uploads.values()].filter((entry) => entry.owner === owner).length;
    if (
      this.closedOwners.has(owner) ||
      this.uploads.size + reservations >= 4 ||
      owned + (this.pending.get(owner) ?? 0) >= 2
    )
      throw new Error("Too many active file uploads");
    this.pending.set(owner, (this.pending.get(owner) ?? 0) + 1);
    let directory: string | undefined;
    let file: NodeFS.FileHandle | undefined;
    try {
      directory = await NodeFS.mkdtemp(NodePath.join(NodeOS.tmpdir(), "isodo-ipc-upload-"));
      file = await NodeFS.open(NodePath.join(directory, "body"), "wx", 0o600);
      if (this.closedOwners.has(owner)) throw new Error("Upload owner closed");
      const id = NodeCrypto.randomBytes(24).toString("hex");
      const timer = setTimeout(() => void this.abort(owner, id).catch(() => {}), 30_000);
      timer.unref();
      this.uploads.set(id, {
        ...input,
        owner,
        directory,
        file,
        received: 0,
        hash: NodeCrypto.createHash("sha256"),
        busy: false,
        committing: false,
        timer,
        controller: new AbortController(),
      });
      return { id, chunkBytes: FILE_CHUNK_BYTES };
    } catch (error) {
      await file?.close().catch(() => {});
      if (directory) await NodeFS.rm(directory, { recursive: true, force: true });
      throw error;
    } finally {
      const count = (this.pending.get(owner) ?? 1) - 1;
      if (count) this.pending.set(owner, count);
      else this.pending.delete(owner);
    }
  }
  private entry(owner: number, id: unknown): Upload {
    if (typeof id !== "string") throw new Error("Invalid upload handle");
    const entry = this.uploads.get(id);
    if (!entry || entry.owner !== owner)
      throw new Error("Upload handle does not belong to this window");
    return entry;
  }
  async append(
    owner: number,
    id: unknown,
    offset: unknown,
    value: unknown,
  ): Promise<{ received: number }> {
    const entry = this.entry(owner, id);
    if (entry.busy || entry.committing) throw new Error("Wait for the previous file ACK");
    if (
      !(value instanceof Uint8Array) ||
      value.byteLength < 1 ||
      value.byteLength > FILE_CHUNK_BYTES ||
      offset !== entry.received ||
      entry.received + value.byteLength > entry.size
    )
      throw new Error("Invalid file chunk or offset");
    entry.busy = true;
    const data = Buffer.from(value);
    try {
      let written = 0;
      while (written < data.length) {
        const result = await entry.file.write(
          data,
          written,
          data.length - written,
          entry.received + written,
        );
        if (!result.bytesWritten) throw new Error("File staging made no progress");
        written += result.bytesWritten;
      }
      entry.hash.update(data);
      entry.received += data.length;
      entry.timer.refresh();
      return { received: entry.received };
    } finally {
      entry.busy = false;
    }
  }
  async finish(owner: number, id: unknown): Promise<FileWriteResult> {
    const entry = this.entry(owner, id);
    if (entry.busy || entry.committing || entry.received !== entry.size)
      throw new Error("File upload has not received all acknowledged bytes");
    entry.committing = true;
    clearTimeout(entry.timer);
    let source: NodeFSSync.ReadStream | undefined;
    try {
      await entry.file.close();
      const digest = entry.hash.digest("hex");
      const boundary = `isodo-${NodeCrypto.randomBytes(16).toString("hex")}`;
      const start = Buffer.from(
        `--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="upload.bin"\r\nContent-Type: application/octet-stream\r\n\r\n`,
      );
      const end = Buffer.from(`\r\n--${boundary}--\r\n`);
      const file = NodeFSSync.createReadStream(NodePath.join(entry.directory, "body"), {
        highWaterMark: FILE_CHUNK_BYTES,
      });
      source = file;
      file.on("error", () => {});
      const chunks = (async function* () {
        try {
          yield start;
          for await (const chunk of file) yield new Uint8Array(chunk as Buffer);
          yield end;
        } finally {
          file.destroy();
        }
      })();
      const body = new ReadableStream<Uint8Array>({
        async pull(controller) {
          try {
            const next = await chunks.next();
            if (next.done) controller.close();
            else controller.enqueue(next.value);
          } catch (error) {
            controller.error(error);
          }
        },
        async cancel() {
          await chunks.return(undefined);
        },
      });
      const query = new URLSearchParams({ path: entry.path, overwrite: String(entry.overwrite) });
      const response = await this.request(
        `/v1/sandboxes/${encodeURIComponent(entry.sandboxId)}/files?${query}`,
        {
          method: "POST",
          body,
          duplex: "half",
          signal: AbortSignal.any([entry.controller.signal, AbortSignal.timeout(5 * 60_000)]),
          headers: {
            "Content-Type": `multipart/form-data; boundary=${boundary}`,
            "Content-Length": String(start.length + entry.size + end.length),
          },
        },
      );
      if (
        typeof response !== "object" ||
        response === null ||
        !("path" in response) ||
        response.path !== entry.path ||
        !("bytes" in response) ||
        response.bytes !== entry.size ||
        !("sha256" in response) ||
        response.sha256 !== digest
      )
        throw new Error("文件提交结果无法确认，请读取目标文件后再重试。");
      return { path: entry.path, bytes: entry.size, sha256: digest };
    } finally {
      entry.controller.abort();
      source?.destroy();
      await this.cleanup(String(id), entry);
    }
  }
  async abort(owner: number, id: unknown): Promise<void> {
    if (typeof id !== "string" || !this.uploads.has(id)) return;
    const entry = this.entry(owner, id);
    entry.controller.abort();
    await this.cleanup(id, entry);
  }
  async closeOwner(owner: number): Promise<void> {
    this.closedOwners.add(owner);
    await Promise.allSettled(
      [...this.uploads]
        .filter(([, entry]) => entry.owner === owner)
        .map(([id]) => this.abort(owner, id)),
    );
  }
  private async cleanup(id: string, entry: Upload) {
    clearTimeout(entry.timer);
    if (this.uploads.get(id) === entry) this.uploads.delete(id);
    await entry.file.close().catch(() => {});
    await NodeFS.rm(entry.directory, { recursive: true, force: true });
  }
}
