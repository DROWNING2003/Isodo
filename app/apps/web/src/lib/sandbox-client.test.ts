import { afterEach, describe, expect, it, vi } from "vite-plus/test";

import {
  fetchSandboxT3ServerBundle,
  getSandboxPairingUrl,
  SANDBOX_T3_SERVER_BUNDLE_URL,
} from "./sandbox-client";

function decodeEnvdCommand(body: BodyInit | null | undefined): string {
  const buffer = body as ArrayBuffer;
  const length = new DataView(buffer).getUint32(1);
  const payload = JSON.parse(new TextDecoder().decode(new Uint8Array(buffer, 5, length))) as {
    readonly process: { readonly args: readonly string[] };
  };
  return payload.process.args[1] ?? "";
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchSandboxT3ServerBundle", () => {
  it("loads the current server bundle without using a browser cache", async () => {
    let request: Request | undefined;
    const bundle = await fetchSandboxT3ServerBundle(async (input, init) => {
      request = new Request(new URL(String(input), "http://localhost"), init);
      return new Response(new Blob(["bundle"]), { status: 200 });
    });

    expect(request?.url).toBe(new URL(SANDBOX_T3_SERVER_BUNDLE_URL, "http://localhost").toString());
    expect(request?.cache).toBe("no-store");
    expect(await bundle.text()).toBe("bundle");
  });

  it("reports when the build artifact is not published", async () => {
    await expect(
      fetchSandboxT3ServerBundle(async () => new Response("missing", { status: 404 })),
    ).rejects.toThrow("Build and publish t3-server-dist.bundle first");
  });
});

describe("getSandboxPairingUrl", () => {
  it("issues pairing codes through the uploaded runtime", async () => {
    const sandboxID = "sandbox-1";
    const domain = "sandbox.test.example";
    const serverUrl = `https://8080-${sandboxID}.${domain}`;
    const fetchImpl = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(null, { status: 200 }))
      .mockResolvedValueOnce(new Response(null, { status: 200 }))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ pairUrl: `${serverUrl}/pair#token=fresh-token` }), {
          status: 200,
        }),
      );
    vi.stubGlobal("fetch", fetchImpl);

    await expect(getSandboxPairingUrl(sandboxID, domain)).resolves.toBe(
      `${serverUrl}/pair#token=fresh-token`,
    );

    const command = decodeEnvdCommand(fetchImpl.mock.calls[1]?.[1]?.body);
    expect(command).toContain('T3_SERVER_RUNTIME_ENTRY="/tmp/.t3-server-runtime/bin.mjs"');
    expect(command).toContain('node "$T3_SERVER_ENTRY" auth pairing create');
    expect(command).not.toContain("/home/user/t3-server");
  });
});
