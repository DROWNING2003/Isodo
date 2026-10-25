import { describe, expect, it } from "vite-plus/test";

import { DEFAULT_T3_SERVER_BUNDLE_URL, fetchT3ServerBundle } from "./bundle";

describe("fetchT3ServerBundle", () => {
  it("publishes the gzip payload under an opaque URL", () => {
    expect(DEFAULT_T3_SERVER_BUNDLE_URL).toBe("/t3-server-dist.bundle");
  });

  it("fetches the bundle without using a stale cache", async () => {
    let request: Request | undefined;
    const bundle = await fetchT3ServerBundle("/t3-server-dist.bundle", async (input, init) => {
      request = new Request(new URL(String(input), "http://localhost"), init);
      return new Response(new Blob(["bundle"]), { status: 200 });
    });

    expect(request?.cache).toBe("no-store");
    expect(await bundle.text()).toBe("bundle");
  });

  it("rejects a missing or empty build artifact", async () => {
    await expect(fetchT3ServerBundle("", async () => new Response())).rejects.toThrow(
      "A T3 server bundle URL is required",
    );
    await expect(
      fetchT3ServerBundle(
        "/t3-server-dist.bundle",
        async () => new Response(null, { status: 404 }),
      ),
    ).rejects.toThrow("Build and publish t3-server-dist.bundle first");
    await expect(
      fetchT3ServerBundle(
        "/t3-server-dist.bundle",
        async () => new Response(new Blob(), { status: 200 }),
      ),
    ).rejects.toThrow("current T3 server bundle is empty");
  });
});
