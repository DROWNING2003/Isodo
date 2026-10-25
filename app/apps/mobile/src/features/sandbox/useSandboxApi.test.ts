import { describe, expect, it, vi } from "vite-plus/test";

import {
  DEFAULT_SANDBOX_API_URL,
  SANDBOX_T3_SERVER_COMMAND,
  buildSandboxCreateBody,
  createT3PairingRequest,
  createT3StartRequest,
  downloadMobileT3ServerBundle,
  envdHeaders,
  parseSandboxApiResponse,
  readPairingUrlFromJson,
  resolveSandboxApiUrl,
  startT3Server,
  waitForT3Server,
} from "./useSandboxApi";
import { isSandboxConnecting, sandboxUrl } from "./sandboxTypes";

vi.mock("expo-file-system", () => {
  class MockFile {
    static downloadFileAsync = vi.fn(async () => {
      const file = new Blob(["bundle"], { type: "application/gzip" });
      Object.defineProperty(file, "uri", { value: "file:///cache/t3-server-dist.bundle" });
      return file;
    });
    readonly uri: string;

    constructor(...parts: Array<string | { readonly uri: string }>) {
      this.uri = parts
        .map((part) => (typeof part === "string" ? part : part.uri))
        .join("/")
        .replace("file:////", "file:///");
    }
  }

  return {
    File: MockFile,
    Paths: { cache: { uri: "file:///cache" } },
  };
});

function decodeConnectJsonEnvelope(body: ArrayBuffer) {
  const length = new DataView(body).getUint32(1);
  return JSON.parse(new TextDecoder().decode(new Uint8Array(body, 5, length)));
}

describe("resolveSandboxApiUrl", () => {
  it("downloads the mobile bundle as a Blob-compatible local file", async () => {
    const file = await downloadMobileT3ServerBundle("http://127.0.0.1:5734/t3-server-dist.bundle");

    expect(file).toBeInstanceOf(Blob);
    expect(file).toMatchObject({
      uri: "file:///cache/t3-server-dist.bundle",
      type: "application/gzip",
    });
  });

  it("starts the uploaded T3 Server bundle", () => {
    expect(SANDBOX_T3_SERVER_COMMAND).toContain('node "$T3_SERVER_ENTRY" serve --port 8080');
  });

  it("marks only the selected sandbox as connecting", () => {
    expect(isSandboxConnecting("sandbox-a", "sandbox-a")).toBe(true);
    expect(isSandboxConnecting("sandbox-b", "sandbox-a")).toBe(false);
    expect(isSandboxConnecting("sandbox-a", null)).toBe(false);
  });

  it("passes placeholder OpenAI environment variables so Codex can discover models", () => {
    expect(
      buildSandboxCreateBody({
        templateID: "zkp-t3",
        timeout: 3600,
        envVars: {
          OPENAI_API_KEY: "sandbox-injection-placeholder",
          OPENAI_BASE_URL: "https://api.fenno.ai",
        },
        injections: [
          {
            type: "openai",
            api_key: "real-secret",
            base_url: "https://api.fenno.ai",
          },
        ],
      }),
    ).toMatchObject({
      envVars: {
        OPENAI_API_KEY: "sandbox-injection-placeholder",
        OPENAI_BASE_URL: "https://api.fenno.ai",
      },
      injections: [{ type: "openai", api_key: "real-secret" }],
    });
  });

  it("uses the configured Sandbox API URL and removes its trailing slash", () => {
    expect(resolveSandboxApiUrl("http://10.210.10.32:5003/")).toBe("http://10.210.10.32:5003");
  });

  it("falls back to the Qiniu API URL when no custom address is configured", () => {
    expect(resolveSandboxApiUrl("")).toBe(DEFAULT_SANDBOX_API_URL);
  });

  it("uses the public domain returned by the sandbox API", () => {
    expect(sandboxUrl("inq318zpim5qpis3xcmj4", "sandbox.test.example")).toBe(
      "https://8080-inq318zpim5qpis3xcmj4.sandbox.test.example",
    );
  });

  it("does not resolve a URL when the sandbox API omits its domain", () => {
    expect(sandboxUrl("inq318zpim5qpis3xcmj4", null)).toBeNull();
  });

  it("rejects a public domain containing a path or credentials", () => {
    expect(sandboxUrl("inq318zpim5qpis3xcmj4", "sandbox.test.example/files")).toBeNull();
    expect(sandboxUrl("inq318zpim5qpis3xcmj4", "user@sandbox.test.example")).toBeNull();
  });

  it("rejects a sandbox ID that could alter the envd hostname", () => {
    expect(sandboxUrl("sandbox.example.com/path", "sandbox.test.example")).toBeNull();
  });

  it("encodes an idempotent T3 launch as a Connect JSON envelope", () => {
    const body = decodeConnectJsonEnvelope(
      createT3StartRequest([
        {
          name: "OpenAI",
          baseUrl: "https://api.fenno.ai",
          envVar: "OPENAI_API_KEY",
          configKey: "requires_openai_auth",
        },
      ]),
    );

    expect(body.process.cmd).toBe("/bin/bash");
    expect(body.process.args).toEqual([
      "-lc",
      expect.stringContaining("unset T3CODE_PAIRING_TOKEN"),
    ]);
    const command = body.process.args[1] as string;
    expect(command).toContain(
      "if ! cmp -s /home/user/.codex/auth.json.tmp /home/user/.codex/auth.json; then",
    );
    expect(command).toContain(
      "  mv /home/user/.codex/auth.json.tmp /home/user/.codex/auth.json\n  chmod 600 /home/user/.codex/auth.json\nelse\n  rm -f /home/user/.codex/auth.json.tmp\nfi",
    );
    expect(command).not.toContain(
      "chmod 600 /home/user/.codex/auth.json\n  mv /home/user/.codex/auth.json.tmp",
    );
    expect(body.process.args[1]).toContain(
      'node "$T3_SERVER_ENTRY" serve --port 8080 --host 0.0.0.0 --base-dir /home/user/.t3 --mode web',
    );
    expect(body.process.args[1]).toContain("for PROC_DIR in /proc/[0-9]*; do");
    expect(body.process.args[1]).toContain(
      '*"bin.mjs serve --port 8080"*) RUNNING_PID="$PID"; break ;;',
    );
    expect(body.process.args[1]).toContain(
      'while kill -0 "$RUNNING_PID" 2>/dev/null && [ "$WAIT_ATTEMPTS" -lt 50 ]; do',
    );
    expect(body.process.args[1].indexOf('while kill -0 "$RUNNING_PID"')).toBeLessThan(
      body.process.args[1].indexOf('nohup node "$T3_SERVER_ENTRY" serve --port 8080'),
    );
    expect(body.process.args[1]).toContain(
      "export HOME=/home/user T3CODE_HOME=/home/user/.t3 CODEX_HOME=/home/user/.codex",
    );
    expect(body.process.args[1]).toContain('{"OPENAI_API_KEY":"sk-sandbox-injection-placeholder"}');
    expect(body.process.args[1]).toContain("chmod 600 /home/user/.codex/auth.json");
    expect(body.process.args[1]).toContain('model_provider = "OpenAI"');
    expect(body.process.args[1]).toContain('model = "gpt-5.4"');
    expect(body.process.args[1]).toContain('review_model = "gpt-5.4"');
    expect(body.process.args[1]).toContain('model_reasoning_effort = "xhigh"');
    expect(body.process.args[1]).toContain("[model_providers.OpenAI]");
    expect(body.process.args[1]).toContain('base_url = "https://api.fenno.ai"');
    expect(body.process.args[1]).toContain('wire_api = "responses"');
    expect(body.process.args[1]).toContain("requires_openai_auth = true");
    expect(body.process.args[1]).toContain(
      'OPENAI_API_KEY="${OPENAI_API_KEY:-sk-sandbox-injection-placeholder}"',
    );
    expect(body.process.args[1]).toContain('base_url = "https://api.fenno.ai"');
    expect(body.process.args[1]).toContain("> /home/user/.t3/startup.log 2>&1");
    expect(body.process.args[1]).not.toContain("/home/user/.opencode/config.json");
    expect(body.stdin).toBe(false);
  });

  it("issues a fresh one-time pairing URL for every connection attempt", () => {
    const serverUrl = "https://8080-inq318zpim5qpis3xcmj4.sandbox.test.example";
    const body = decodeConnectJsonEnvelope(createT3PairingRequest(serverUrl));

    expect(body.process.cmd).toBe("/bin/bash");
    expect(body.process.args[1]).toContain("auth pairing create --base-dir /home/user/.t3");
    expect(body.process.args[1]).toContain(`--base-url '${serverUrl}'`);
    expect(body.process.args[1]).toContain("> /home/user/.t3/mobile-pairing.json.tmp");
    expect(body.process.args[1]).toContain(
      "mv /home/user/.t3/mobile-pairing.json.tmp /home/user/.t3/mobile-pairing.json",
    );
  });

  it("accepts only a pairing URL for the expected sandbox origin", () => {
    const serverUrl = "https://8080-inq318zpim5qpis3xcmj4.sandbox.test.example";
    expect(
      readPairingUrlFromJson(
        JSON.stringify({ pairUrl: `${serverUrl}/pair#token=7QK5FJ2RB8NC` }),
        serverUrl,
      ),
    ).toBe(`${serverUrl}/pair#token=7QK5FJ2RB8NC`);
    expect(
      readPairingUrlFromJson(
        JSON.stringify({ pairUrl: "https://attacker.example/pair#token=secret" }),
        serverUrl,
      ),
    ).toBeNull();
  });

  it("sends the sandbox user and optional envd access credentials", () => {
    expect(
      envdHeaders({
        sandboxID: "inq318zpim5qpis3xcmj4",
        envdAccessToken: "envd-token",
        trafficAccessToken: "traffic-token",
      }),
    ).toEqual({
      Authorization: "Basic dXNlcjo=",
      "Connect-Protocol-Version": "1",
      "Content-Type": "application/connect+json",
      "X-Access-Token": "envd-token",
      "X-Sandbox-ID": "inq318zpim5qpis3xcmj4",
      "e2b-traffic-access-token": "traffic-token",
    });
  });

  it("waits for envd and starts T3 through Process/Start", async () => {
    const fetchImpl = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 200 }));

    await startT3Server({
      sandboxID: "inq318zpim5qpis3xcmj4",
      domain: "sandbox.test.example",
      serverBundle: new Blob(["bundle"]),
      fetchImpl,
      wait: async () => {},
    });

    expect(fetchImpl).toHaveBeenNthCalledWith(
      1,
      "https://49983-inq318zpim5qpis3xcmj4.sandbox.test.example/health",
      expect.objectContaining({ method: "GET" }),
    );
    expect(fetchImpl).toHaveBeenNthCalledWith(
      2,
      "https://49983-inq318zpim5qpis3xcmj4.sandbox.test.example/files?path=%2Ftmp%2F.t3-server-t3-server-dist.bundle&username=user",
      expect.objectContaining({
        method: "POST",
      }),
    );
    expect(fetchImpl).toHaveBeenNthCalledWith(
      3,
      "https://49983-inq318zpim5qpis3xcmj4.sandbox.test.example/process.Process/Start",
      expect.objectContaining({
        method: "POST",
        body: expect.any(ArrayBuffer),
      }),
    );
  });

  it("waits until T3 is accepting HTTP requests before issuing a pairing code", async () => {
    const fetchImpl = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(null, { status: 502 }))
      .mockResolvedValueOnce(new Response(null, { status: 200 }));
    const wait = vi.fn(async () => {});

    await waitForT3Server("https://8080-sandbox.test.example", {
      fetchImpl,
      wait,
      retryCount: 2,
    });

    expect(fetchImpl).toHaveBeenCalledTimes(2);
    expect(wait).toHaveBeenCalledTimes(1);
  });

  it("accepts the server-only sandbox response as ready", async () => {
    const fetchImpl = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        new Response("No static directory configured and no dev URL set.", { status: 503 }),
      );

    await waitForT3Server("https://8080-sandbox.test.example", {
      fetchImpl,
      retryCount: 1,
      wait: async () => {},
    });

    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it("accepts a successful API response with no JSON body", async () => {
    await expect(
      parseSandboxApiResponse<void>(new Response(null, { status: 204 })),
    ).resolves.toBeUndefined();
  });

  it("parses a successful API response with a JSON body", async () => {
    await expect(
      parseSandboxApiResponse<{ sandboxID: string }>(
        new Response('{"sandboxID":"sandbox-1"}', { status: 200 }),
      ),
    ).resolves.toEqual({ sandboxID: "sandbox-1" });
  });
});
