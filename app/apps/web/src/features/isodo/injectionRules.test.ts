import { expect, it } from "vite-plus/test";
import type { InjectionRule, InjectionRuleMaterial } from "../../api/public.generated";
import {
  localRuleFromPersistent,
  ruleMaterialIsSet,
  ruleSecretPreview,
  ruleTarget,
  ruleTypeLabel,
} from "./injectionRules";

const rule: InjectionRule = {
  id: "rule-1",
  name: "个人 GitHub",
  type: "github",
  base_url: "https://api.github.com/repos/qiniu/*",
  secret_hint: "••••8f2a",
  secret_set: true,
  created_at: "2026-10-25T00:00:00Z",
  updated_at: "2026-10-25T00:00:00Z",
};

const material: InjectionRuleMaterial = {
  id: "rule-1",
  type: "github",
  base_url: "https://api.github.com/repos/qiniu/*",
  token: "ghp_secret_8f2a",
};

it("maps an account rule and its material into a local reference", () => {
  const local = localRuleFromPersistent(rule, material);
  expect(local.key).toBe("persistent:rule-1");
  expect(local.kind).toBe("persistent");
  expect(local.persistent_id).toBe("rule-1");
  expect(local.spec.base_url).toBe("https://api.github.com/repos/qiniu/*");
  expect(local.secret?.token).toBe("ghp_secret_8f2a");
  expect(ruleMaterialIsSet(local)).toBe(true);
  expect(ruleSecretPreview(local)).toBe("••••8f2a");
});

it("uses provider defaults when a rule has no base URL", () => {
  expect(ruleTarget({ spec: { type: "openai" } })).toBe("api.openai.com");
  expect(ruleTarget({ spec: { type: "anthropic" } })).toBe("api.anthropic.com");
  expect(ruleTarget({ spec: { type: "github" } })).toBe("github.com · api.github.com");
  expect(ruleTypeLabel("http")).toBe("HTTP Header");
  expect(ruleTypeLabel("qiniu")).toBe("七牛 AI");
});

it("materializes MCP credentials as HTTP injection without changing the account type", () => {
  const mcp: InjectionRule = {
    ...rule,
    type: "mcp",
    base_url: "https://mcp.example.com/mcp",
    header_name: "Authorization",
  };
  const local = localRuleFromPersistent(mcp, {
    id: mcp.id,
    type: "mcp",
    headers: { Authorization: "Bearer test-token" },
  });
  expect(local.spec.type).toBe("http");
  expect(local.spec.header_name).toBe("Authorization");
  expect(local.secret?.headers).toEqual({ Authorization: "Bearer test-token" });
  expect(mcp.type).toBe("mcp");
  expect(ruleTypeLabel(mcp.type)).toBe("MCP");
});
