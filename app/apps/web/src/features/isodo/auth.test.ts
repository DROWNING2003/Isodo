import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";

beforeEach(() => {
  vi.resetModules();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

const me = () =>
  new Response(JSON.stringify({ user: { name: "A", provider: "GitHub" }, csrf_token: "csrf-a" }), {
    status: 200,
  });

describe("Isodo authenticated API client", () => {
  it("uses the server session and CSRF token for key creation, never a browser-generated key", async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce(me())
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ id: "key-a", api_key: "server-issued-secret" }), {
          status: 201,
        }),
      );
    vi.stubGlobal("fetch", request);
    const { refreshAuth, apiKeys } = await import("./auth");
    await refreshAuth();
    const input = { name: "ci", scopes: ["sandboxes:read"] as const, expires_at: null };
    const key = await apiKeys.create({ ...input, scopes: [...input.scopes] });
    expect(key.api_key).toBe("server-issued-secret");
    const [path, options] = request.mock.calls[1]!;
    expect(path).toBe("/v1/api-keys");
    expect(options.credentials).toBe("same-origin");
    expect(options.headers.get("X-CSRF-Token")).toBe("csrf-a");
    expect(options.headers.has("Authorization")).toBe(false);
    expect(JSON.parse(options.body)).toEqual(input);
  });

  it("performs list and revoke on the server and propagates failures", async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce(me())
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: { code: "API_KEY_LIMIT" } }), { status: 409 }),
      );
    vi.stubGlobal("fetch", request);
    const { refreshAuth, apiKeys } = await import("./auth");
    await refreshAuth();
    expect(await apiKeys.list()).toEqual({ items: [] });
    await apiKeys.revoke("key-a");
    expect(request.mock.calls[2]![0]).toBe("/v1/api-keys/key-a");
    expect(request.mock.calls[2]![1].method).toBe("DELETE");
    await expect(
      apiKeys.create({ name: "ci", scopes: ["sandboxes:read"], expires_at: null }),
    ).rejects.toThrow("10");
  });

  it("does not restore a stale session if an in-flight refresh completes after logout", async () => {
    let resolveRefresh!: (response: Response) => void;
    const request = vi
      .fn()
      .mockResolvedValueOnce(me())
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            resolveRefresh = resolve;
          }),
      )
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", request);
    const { refreshAuth, signOut, apiRequest } = await import("./auth");
    await refreshAuth();
    const stale = refreshAuth();
    await signOut();
    resolveRefresh(me());
    await stale;
    await apiRequest("/probe", { method: "POST" });
    expect(request.mock.calls[3]![1].headers.has("X-CSRF-Token")).toBe(false);
  });
});
