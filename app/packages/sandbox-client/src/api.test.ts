import { describe, expect, it } from "vite-plus/test";

import { createSandboxApi } from "./api";

describe("sandbox API", () => {
  it("lists all sandbox states through the v2 endpoint", async () => {
    const requests: string[] = [];
    const api = createSandboxApi({
      apiKey: "test-key",
      apiUrl: "https://sandbox.example.com",
      fetchImpl: async (input) => {
        requests.push(String(input));
        return new Response("[]", { status: 200 });
      },
    });

    await api.list({ metadata: { app: "boundly" } });

    expect(requests).toEqual(["https://sandbox.example.com/v2/sandboxes?metadata=app%3Dboundly"]);
  });

  it("reads runtime injections from a sandbox", async () => {
    const requests: string[] = [];
    const api = createSandboxApi({
      apiKey: "test-key",
      apiUrl: "https://sandbox.example.com",
      fetchImpl: async (input) => {
        requests.push(String(input));
        return new Response(
          JSON.stringify({ injections: [{ type: "openai", api_key: "sk-****" }] }),
          {
            status: 200,
          },
        );
      },
    });

    await expect(api.getInjections("sandbox/a")).resolves.toEqual([
      { type: "openai", api_key: "sk-****" },
    ]);
    expect(requests).toEqual(["https://sandbox.example.com/sandboxes/sandbox%2Fa/injections"]);
  });

  it("replaces runtime injections immediately", async () => {
    let request: { input: string; init?: RequestInit } | undefined;
    const api = createSandboxApi({
      apiKey: "test-key",
      apiUrl: "https://sandbox.example.com",
      fetchImpl: async (input, init) => {
        request = { input: String(input), ...(init ? { init } : {}) };
        return new Response(null, { status: 204 });
      },
    });
    const injections = [
      {
        type: "http" as const,
        base_url: "https://api.example.com",
        headers: { Authorization: "Bearer test" },
      },
    ];

    await api.updateInjections("sandbox-1", injections);

    expect(request?.input).toBe("https://sandbox.example.com/sandboxes/sandbox-1/injections");
    expect(request?.init?.method).toBe("PUT");
    expect(request?.init?.body).toBe(JSON.stringify({ injections }));
  });
});
