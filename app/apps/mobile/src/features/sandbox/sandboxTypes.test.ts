import { describe, expect, it } from "vite-plus/test";

import { mergeSandboxCredentials } from "./sandboxTypes";

describe("mergeSandboxCredentials", () => {
  it("preserves optional provider credentials when saving core settings", () => {
    expect(
      mergeSandboxCredentials(
        {
          openaiApiKey: "sk-secret",
          openaiBaseUrl: "https://api.example.com",
        },
        {
          e2bApiKey: "e2b-key",
          e2bApiUrl: "https://sandbox.example.com",
          templateID: "template-id",
        },
      ),
    ).toEqual({
      e2bApiKey: "e2b-key",
      e2bApiUrl: "https://sandbox.example.com",
      openaiApiKey: "sk-secret",
      openaiBaseUrl: "https://api.example.com",
      templateID: "template-id",
    });
  });

  it("uses edited optional connection credentials when provided", () => {
    expect(
      mergeSandboxCredentials(
        {
          openaiApiKey: "old-key",
          openaiBaseUrl: "https://old.example.com",
        },
        {
          e2bApiKey: "e2b-key",
          e2bApiUrl: "https://sandbox.example.com",
          openaiApiKey: "new-key",
          openaiBaseUrl: "https://new.example.com",
          templateID: "template-id",
        },
      ),
    ).toMatchObject({
      openaiApiKey: "new-key",
      openaiBaseUrl: "https://new.example.com",
    });
  });
});
