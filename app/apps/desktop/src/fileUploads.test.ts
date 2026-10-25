import { expect, it, vi } from "vite-plus/test";
import * as NodeCrypto from "node:crypto";
import { SandboxFileUploads, FILE_CHUNK_BYTES } from "./fileUploads.js";
it("commits only acknowledged bounded chunks through multipart and checks the Guest result", async () => {
  const bytes = new Uint8Array(FILE_CHUNK_BYTES + 3).fill(255);
  const sha256 = NodeCrypto.createHash("sha256").update(bytes).digest("hex");
  const request = vi.fn(async (path: string, init: RequestInit) => {
    expect(path).toContain("/v1/sandboxes/local-a/files?");
    const body = await new Response(init.body).arrayBuffer();
    expect(body.byteLength).toBeGreaterThan(bytes.length);
    return { path: "/workspace/config.bin", bytes: bytes.length, sha256 };
  });
  const uploads = new SandboxFileUploads(request);
  const handle = await uploads.begin(1, {
    sandboxId: "local-a",
    path: "/workspace/config.bin",
    size: bytes.length,
  });
  const first = uploads.append(1, handle.id, 0, bytes.slice(0, FILE_CHUNK_BYTES));
  await expect(uploads.append(1, handle.id, 0, bytes.slice(0, 1))).rejects.toThrow("ACK");
  await first;
  await expect(uploads.finish(1, handle.id)).rejects.toThrow("acknowledged");
  await uploads.append(1, handle.id, FILE_CHUNK_BYTES, bytes.slice(FILE_CHUNK_BYTES));
  expect(await uploads.finish(1, handle.id)).toEqual({
    path: "/workspace/config.bin",
    bytes: bytes.length,
    sha256,
  });
  expect(request).toHaveBeenCalledOnce();
});
it("rejects another window, oversized chunks, and cancels abandoned handles", async () => {
  const uploads = new SandboxFileUploads(vi.fn());
  const handle = await uploads.begin(1, { sandboxId: "local-a", path: "/workspace/a", size: 1 });
  await expect(uploads.append(2, handle.id, 0, new Uint8Array([1]))).rejects.toThrow("belong");
  await expect(
    uploads.append(1, handle.id, 0, new Uint8Array(FILE_CHUNK_BYTES + 1)),
  ).rejects.toThrow("chunk");
  await uploads.closeOwner(1);
  await expect(uploads.finish(1, handle.id)).rejects.toThrow("belong");
  await expect(
    uploads.begin(1, { sandboxId: "local-a", path: "/workspace/a", size: 1 }),
  ).rejects.toThrow();
});
it("does not retry an unconfirmed commit and releases its handle", async () => {
  const request = vi.fn().mockResolvedValue({ path: "/workspace/a", bytes: 0, sha256: "wrong" });
  const uploads = new SandboxFileUploads(request);
  const handle = await uploads.begin(1, { sandboxId: "local-a", path: "/workspace/a", size: 0 });
  await expect(uploads.finish(1, handle.id)).rejects.toThrow("无法确认");
  await expect(uploads.finish(1, handle.id)).rejects.toThrow("belong");
  expect(request).toHaveBeenCalledOnce();
});
