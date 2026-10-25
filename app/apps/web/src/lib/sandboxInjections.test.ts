import { describe, expect, it } from "vite-plus/test";

import {
  createSandboxInjectionDrafts,
  getConfiguredSandboxInjections,
  serializeSandboxInjectionDrafts,
  validateSandboxInjectionDrafts,
} from "./sandboxInjections";

describe("sandbox injection editor logic", () => {
  it("restores a locally configured secret when the API masks it", () => {
    const result = createSandboxInjectionDrafts(
      [{ type: "openai", api_key: "sk-****", base_url: "https://api.example.com" }],
      [{ type: "openai", api_key: "sk-local", base_url: "https://api.example.com" }],
    );

    expect(result.drafts).toEqual([
      expect.objectContaining({ type: "openai", secret: "sk-local", wasMasked: false }),
    ]);
  });

  it("keeps ID-based injections when replacing editable rules", () => {
    const result = createSandboxInjectionDrafts([{ type: "id", id: "inj-1" }], []);

    expect(serializeSandboxInjectionDrafts(result.drafts, result.fixed)).toEqual([
      { type: "id", id: "inj-1" },
    ]);
  });

  it("requires secrets before sending a full replacement", () => {
    const drafts = [
      {
        id: 0,
        type: "openai" as const,
        baseUrl: "",
        secret: "",
        header: "",
        wasMasked: true,
      },
    ];

    expect(validateSandboxInjectionDrafts(drafts)).toBe("第 1 条注入需要重新填写密钥。");
  });

  it("does not crash when legacy credentials omit optional OpenAI fields", () => {
    const credentials = {
      e2bApiKey: "e2b-key",
      e2bApiUrl: "https://sandbox.example.com",
      openaiApiKey: null,
      openaiBaseUrl: null,
      templateID: "template-id",
    } as unknown as Parameters<typeof getConfiguredSandboxInjections>[0];

    expect(() => getConfiguredSandboxInjections(credentials)).not.toThrow();
  });
});
