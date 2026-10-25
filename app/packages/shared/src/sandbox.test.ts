import { describe, expect, it } from "vite-plus/test";

import {
  buildCodexAuthJson,
  buildCodexConfigToml,
  buildSkillInstallCommands,
  detectCodexProviders,
  isSandboxUrl,
  sandboxIdFromUrl,
} from "./sandbox.js";

describe("sandbox URLs", () => {
  it("recognizes sandbox IDs containing hyphens", () => {
    expect(isSandboxUrl("https://8080-sandbox-1.e2b.example.com")).toBe(true);
    expect(isSandboxUrl("http://localhost:5733")).toBe(false);
  });

  it("extracts sandbox IDs containing hyphens from proxied URLs", () => {
    expect(
      sandboxIdFromUrl("https://8080-sbx-abc-123.e2b.example.com/.well-known/t3/environment"),
    ).toBe("sbx-abc-123");
    expect(sandboxIdFromUrl("http://localhost:5733")).toBeNull();
  });
});

describe("Codex provider configuration", () => {
  it("keeps the OpenAI provider name and fills only its base URL", () => {
    const [provider] = detectCodexProviders([{ base_url: "https://api.fenno.ai" }]);

    expect(provider).toBeDefined();
    if (!provider) throw new Error("Expected a provider for the Fenno endpoint");
    expect(provider).toMatchObject({ name: "OpenAI", baseUrl: "https://api.fenno.ai" });

    const config = buildCodexConfigToml([provider]);
    expect(config).toContain('model_provider = "OpenAI"');
    expect(config).toContain("[model_providers.OpenAI]");
    expect(config).not.toContain("model_providers.Fenno");
    expect(config).toContain('name = "OpenAI"');
    expect(config).toContain('base_url = "https://api.fenno.ai"');

    const fallbackConfig = buildCodexConfigToml([
      {
        name: "",
        baseUrl: "https://api.fenno.ai",
        envVar: "OPENAI_API_KEY",
        configKey: "requires_openai_auth",
      },
    ]);
    expect(fallbackConfig).toContain('model_provider = "OpenAI"');
    expect(fallbackConfig).toContain('base_url = "https://api.fenno.ai"');
    expect(fallbackConfig).not.toContain('name = ""');
    expect(buildCodexAuthJson([provider])).toBe(
      '{"OPENAI_API_KEY":"sk-sandbox-injection-placeholder"}',
    );
  });
});

describe("buildSkillInstallCommands", () => {
  it("installs skills into OpenCode's global skill directory", () => {
    const commands = buildSkillInstallCommands([{ name: "sandbox-preview-url", content: "skill" }]);

    expect(commands).toContain(
      "mkdir -p /home/user/.codex/skills/sandbox-preview-url /home/user/.agents/skills/sandbox-preview-url /home/user/.config/opencode/skills/sandbox-preview-url /home/user/.opencode/skills/sandbox-preview-url",
    );
    expect(commands).toContain(
      "cp /home/user/.codex/skills/sandbox-preview-url/SKILL.md /home/user/.config/opencode/skills/sandbox-preview-url/SKILL.md",
    );
    expect(commands).toContain(
      "cp /home/user/.codex/skills/sandbox-preview-url/SKILL.md /home/user/.agents/skills/sandbox-preview-url/SKILL.md",
    );
  });
});
