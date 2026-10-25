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
