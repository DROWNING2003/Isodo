import { afterEach, expect, it, vi } from "vite-plus/test";
import { isodoFetch } from "./transport";
afterEach(() => vi.unstubAllGlobals());
it("explains an outdated API when the credential endpoint returns a plain-text 404", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(new Response("404 page not found", { status: 404 })),
  );
  await expect(isodoFetch("/v1/injection-rules", { method: "POST", body: "{}" })).rejects.toThrow(
    "凭证接口不存在（HTTP 404），请更新并重启 Isodo API 服务。",
  );
});
it("lets fetch supply the multipart boundary and returns verified binary bodies as Blob", async () => {
  const data = new Uint8Array([0, 255, 128, 1]);
  const fetch = vi
    .fn()
    .mockResolvedValue(
      new Response(data, { headers: { "Content-Type": "application/octet-stream" } }),
    );
  vi.stubGlobal("fetch", fetch);
  const form = new FormData();
  form.append("file", new Blob([data]), "config.bin");
  const result = await isodoFetch<Blob>("/v1/sandboxes/id/files?path=/workspace/config.bin", {
    method: "POST",
    body: form,
    headers: { "Content-Type": "multipart/form-data" },
  });
  expect(new Headers(fetch.mock.calls[0]?.[1].headers).has("Content-Type")).toBe(false);
  expect(new Uint8Array(await result.arrayBuffer())).toEqual(data);
});
