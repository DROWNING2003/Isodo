import { describe, expect, it } from "vite-plus/test";

import {
  buildT3PairingCommand,
  buildT3StartCommand,
  getT3PairingUrl,
  startT3Server,
} from "./t3-orchestrator";

describe("sandbox T3 runtime selection", () => {
  it("extracts and starts the uploaded server bundle", () => {
    const command = buildT3StartCommand(
      [],
      [],
      'nohup node "$T3_SERVER_ENTRY" serve --port 8080 --host 0.0.0.0',
    );

    expect(command).toContain('T3_SERVER_BUNDLE="/tmp/.t3-server-t3-server-dist.bundle"');
    expect(command).toContain("export T3CODE_SANDBOX=1");
    expect(command).toContain('T3_SERVER_RUNTIME_DIR="/tmp/.t3-server-runtime"');
    expect(command).toContain('T3_SERVER_RUNTIME_ENTRY="$T3_SERVER_RUNTIME_DIR/bin.mjs"');
    expect(command).toContain('tar -xzf "$T3_SERVER_BUNDLE"');
    expect(command).toContain("aarch64|arm64) T3_SERVER_ARCH=arm64");
    expect(command).toContain("x86_64|amd64) T3_SERVER_ARCH=x64");
    expect(command).toContain("fff-bin-linux-${T3_SERVER_ARCH}-gnu/libfff_c.so");
    expect(command).toContain('gzip -d "$T3_FFF_LIBRARY.gz"');
    expect(command).toContain('T3_SERVER_ENTRY="$T3_SERVER_RUNTIME_ENTRY"');
    expect(command).toContain("if command -v fuser >/dev/null 2>&1; then fuser -k 8080/tcp");
    expect(command).not.toContain("npm install");
    expect(command).toContain('node "$T3_SERVER_ENTRY" serve --port 8080');
  });

  it("uses the uploaded runtime for fresh pairing commands when available", () => {
    const command = buildT3PairingCommand("https://8080-sandbox-1.sandbox.example.com");

    expect(command).toContain('T3_SERVER_RUNTIME_ENTRY="/tmp/.t3-server-runtime/bin.mjs"');
    expect(command).toContain(
      'if [ ! -s "$T3_SERVER_ENTRY" ]; then echo "T3 server runtime is missing" >&2; exit 1; fi',
    );
    expect(command).not.toContain("LEGACY");
    expect(command).toContain('node "$T3_SERVER_ENTRY" auth pairing create');
  });
});

describe("sandbox T3 orchestration URLs", () => {
  it("starts envd through the sandbox domain returned by the API", async () => {
    const requests: string[] = [];

    await startT3Server({
      sandboxID: "sandbox-1",
      domain: "sandbox.example.com",
      serverBundle: new Blob(["bundle"]),
      fetchImpl: async (input) => {
        requests.push(String(input));
        return new Response("{}", { status: 200 });
      },
      wait: async () => undefined,
    });

    expect(requests).toEqual([
      "https://49983-sandbox-1.sandbox.example.com/health",
      "https://49983-sandbox-1.sandbox.example.com/files?path=%2Ftmp%2F.t3-server-t3-server-dist.bundle&username=user",
      "https://49983-sandbox-1.sandbox.example.com/process.Process/Start",
    ]);
  });

  it("reads a pairing URL through the sandbox-domain envd endpoint", async () => {
    const requests: string[] = [];
    const serverUrl = "https://8080-sandbox-1.sandbox.example.com";

    const pairingUrl = await getT3PairingUrl("sandbox-1", "sandbox.example.com", undefined, {
      fetchImpl: async (input) => {
        const url = String(input);
        requests.push(url);
        if (url === serverUrl) {
          return new Response("ready", { status: 200 });
        }
        if (url.endsWith("/process.Process/Start")) {
          return new Response("", { status: 200 });
        }
        if (url.includes("/files?")) {
          return Response.json({ pairUrl: `${serverUrl}/pair#token=fresh-token` });
        }
        return new Response("unexpected request", { status: 500 });
      },
      attemptDelayMs: 0,
      pairingPollDelayMs: 0,
    });

    expect(pairingUrl).toBe(`${serverUrl}/pair#token=fresh-token`);
    expect(requests).toEqual([
      serverUrl,
      "https://49983-sandbox-1.sandbox.example.com/process.Process/Start",
      "https://49983-sandbox-1.sandbox.example.com/files?path=%2Fhome%2Fuser%2F.t3%2Fmobile-pairing.json&username=user",
    ]);
  });

  it("includes the T3 startup log when the public server never becomes ready", async () => {
    const serverUrl = "https://8080-sandbox-1.sandbox.example.com";

    await expect(
      getT3PairingUrl("sandbox-1", "sandbox.example.com", undefined, {
        fetchImpl: async (input) => {
          const url = String(input);
          if (url === serverUrl) return new Response(null, { status: 502 });
          if (url.includes("/files?")) return new Response("bundle entry failed", { status: 200 });
          return new Response(null, { status: 500 });
        },
        serverRetryCount: 1,
        serverRetryDelayMs: 0,
        wait: async () => undefined,
      }),
    ).rejects.toThrow("T3 startup log: bundle entry failed");
  });

  it("uploads the current server bundle before starting envd", async () => {
    const requests: Array<{ url: string; method: string }> = [];

    await startT3Server({
      sandboxID: "sandbox-1",
      domain: "sandbox.example.com",
      serverBundle: new Blob(["bundle"]),
      fetchImpl: async (input, init) => {
        requests.push({ url: String(input), method: init?.method ?? "GET" });
        return new Response(null, { status: requests.length === 1 ? 204 : 200 });
      },
      wait: async () => undefined,
    });

    expect(requests).toEqual([
      {
        url: "https://49983-sandbox-1.sandbox.example.com/health",
        method: "GET",
      },
      {
        url: "https://49983-sandbox-1.sandbox.example.com/files?path=%2Ftmp%2F.t3-server-t3-server-dist.bundle&username=user",
        method: "POST",
      },
      {
        url: "https://49983-sandbox-1.sandbox.example.com/process.Process/Start",
        method: "POST",
      },
    ]);
  });
});
