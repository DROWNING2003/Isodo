import { describe, expect, it } from "vite-plus/test";
import {
  detectAndRedactConfig,
  modelEndpoint,
  modelsFromResponse,
  replaceSecretsWithPlaceholder,
} from "./agentConfigs";

import { modelDiscoveryHeaders, validModelEndpoint } from "./agentModelDiscovery";

describe("agent config helpers", () => {
  it("replaces the real key with the shared placeholder", () => {
    expect(replaceSecretsWithPlaceholder('{"apiKey":"sk-secret"}', ["sk-secret"])).toBe(
      '{"apiKey":"${ISODO_SECRET}"}',
    );
  });

  it("redacts common pasted bearer and key fields", () => {
    const result = detectAndRedactConfig(
      'Authorization: Bearer pasted-secret\napi_key: "another-secret"',
      { secret_hint: "••••" },
    );
    expect(result.detected).toBe(true);
    expect(result.content).not.toContain("pasted-secret");
    expect(result.content).not.toContain("another-secret");
  });

  it("accepts common model response shapes", () => {
    expect(modelsFromResponse({ data: [{ id: "gpt-5" }, { id: "claude-4" }] })).toEqual([
      "gpt-5",
      "claude-4",
    ]);
    expect(modelsFromResponse({ models: ["local-model"] })).toEqual(["local-model"]);
  });

  it("builds a stable models endpoint", () => {
    expect(modelEndpoint("https://relay.example/v1/")).toBe("https://relay.example/v1/models");
  });
});

describe("model discovery authentication", () => {
  it("preserves complete custom HTTP headers without adding Bearer twice", () => {
    const headers = modelDiscoveryHeaders("http", {
      headers: { Authorization: "Bearer saved-key", "X-Tenant": "team" },
    });
    expect(headers.get("Authorization")).toBe("Bearer saved-key");
    expect(headers.get("X-Tenant")).toBe("team");
  });
  it("uses the protocol's actual authentication header", () => {
    expect(modelDiscoveryHeaders("openai", { api_key: "key" }).get("Authorization")).toBe(
      "Bearer key",
    );
    const headers = modelDiscoveryHeaders("anthropic", { api_key: "key" });
    expect(headers.get("x-api-key")).toBe("key");
    expect(headers.get("anthropic-version")).toBe("2023-06-01");
    expect(headers.has("Authorization")).toBe(false);
  });
  it("rejects malformed or credential-bearing endpoints", () => {
    expect(validModelEndpoint("https://relay.example/v1")).toBe(true);
    for (const url of [
      "ftp://relay.example",
      "https://user:pass@relay.example/v1",
      "relay.example",
      "https://relay.example?api_key=secret",
    ])
      expect(validModelEndpoint(url)).toBe(false);
  });
});
