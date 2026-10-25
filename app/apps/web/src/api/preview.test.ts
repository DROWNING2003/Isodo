import { afterEach, expect, it, vi } from "vite-plus/test";
import { bootstrapSandboxPreview, previewFetch } from "./preview";

afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); });
const host = "8080-3f40c38e-0af8-4628-878d-b0df7e42de80.isodo.localhost:8080";

it("bootstraps the preview cookie before pairing without sending the fragment", async () => {
  vi.stubEnv("VITE_ISODO_PREVIEW_DOMAIN", "isodo.localhost");
  const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
  vi.stubGlobal("fetch", fetch);
  await bootstrapSandboxPreview(`http://${host}/__isodo/auth/signed-claim?next=/pair#token=one-time`);
  const [target, init] = fetch.mock.calls[0]!;
  const url = new URL(target);
  expect(url.hash).toBe("");
  expect(url.searchParams.get("next")).toBe("/__isodo/ready");
  expect(init.credentials).toBe("include");
});

it("includes gateway cookies only for configured sandbox preview hosts", async () => {
  vi.stubEnv("VITE_ISODO_PREVIEW_DOMAIN", "isodo.localhost");
  const fetch = vi.fn().mockResolvedValue(new Response());
  vi.stubGlobal("fetch", fetch);
  await previewFetch(`http://${host}/.well-known/t3/environment`, { credentials: "omit" });
  expect(fetch.mock.calls[0]![1].credentials).toBe("include");
  await previewFetch("https://evil.isodo.localhost.example.com", { credentials: "omit" });
  expect(fetch.mock.calls[1]![1].credentials).toBe("omit");
  await expect(bootstrapSandboxPreview("https://evil.example/__isodo/auth/claim")).rejects.toThrow();
});

it("does not turn a local direct pairing link into a gateway request", async () => {
  const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
  await bootstrapSandboxPreview("http://127.0.0.1:4000/pair#token=one-time");
  expect(fetch).not.toHaveBeenCalled();
});
