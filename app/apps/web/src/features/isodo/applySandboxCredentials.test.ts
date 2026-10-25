import { temporaryAgentConnection } from "./TemporaryAgentDialog";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";
vi.mock("./injectionRules", () => ({
  fetchInjectionMaterials: vi.fn(),
  localRuleFromPersistent: (rule: { id: string }) => ({ key: rule.id, spec: { type: "openai" } }),
  writeSandboxAgentConfig: vi.fn(),
}));
import { fetchInjectionMaterials, writeSandboxAgentConfig } from "./injectionRules";
import {
  agentConfigurationFiles,
  applySandboxCredentials,
  withCredentialTimeout,
} from "./applySandboxCredentials";
import type { AgentConfig } from "./agentConfigs";
import type { InjectionRule } from "../../api/public.generated";
const config: AgentConfig = {
  id: "agent",
  name: "Codex",
  agent: "codex",
  connectionId: "credential",
  modelNames: ["custom-model"],
  defaultModel: "custom-model",
  filePath: "~/.codex/config.toml",
  content: "",
  updatedAt: "",
};
const credential: InjectionRule = {
  id: "credential",
  name: "Relay",
  type: "openai",
  base_url: "https://relay.example/v1",
  secret_set: true,
  secret_hint: "••••key",
  created_at: "",
  updated_at: "",
};
beforeEach(() => {
  vi.mocked(fetchInjectionMaterials)
    .mockReset()
    .mockResolvedValue(
      new Map([[credential.id, { id: credential.id, type: "openai", api_key: "real-key" }]]),
    );
  vi.mocked(writeSandboxAgentConfig)
    .mockReset()
    .mockResolvedValue({ path: config.filePath, bytes: 1, sha256: "", requires_restart: true });
});
afterEach(() => {
  vi.useRealTimers();
});
it("one action binds the required credential and writes generated Codex configuration and placeholder auth", async () => {
  const write = vi.fn().mockResolvedValue({});
  await applySandboxCredentials({
    sandboxId: "sbx",
    selection: { agentIds: [config.id], credentialIds: [] },
    configs: [config],
    credentials: [credential],
    oneOff: [],
    write,
  });
  expect(write).toHaveBeenCalledWith([
    expect.objectContaining({
      key: credential.id,
      spec: expect.objectContaining({
        if_headers: { Authorization: "Bearer isodo-credential-credential" },
      }),
    }),
  ]);
  expect(writeSandboxAgentConfig).toHaveBeenCalledTimes(2);
  expect(JSON.stringify(vi.mocked(writeSandboxAgentConfig).mock.calls)).toContain("custom-model");
  expect(JSON.stringify(vi.mocked(writeSandboxAgentConfig).mock.calls)).not.toContain("real-key");
  expect(vi.mocked(writeSandboxAgentConfig).mock.calls[1]?.[1].content).toContain(
    "isodo-credential-credential",
  );
});
it("does not grant credentials when two configs conflict on a file path", async () => {
  const write = vi.fn();
  await expect(
    applySandboxCredentials({
      sandboxId: "sbx",
      selection: { agentIds: ["agent", "other"], credentialIds: [] },
      configs: [config, { ...config, id: "other" }],
      credentials: [credential],
      oneOff: [],
      write,
    }),
  ).rejects.toThrow("文件路径重复");
  expect(write).not.toHaveBeenCalled();
});
it("reports partial success if files fail after authorization was saved", async () => {
  vi.mocked(writeSandboxAgentConfig).mockRejectedValue(new Error("guest offline"));
  await expect(
    applySandboxCredentials({
      sandboxId: "sbx",
      selection: { agentIds: [config.id], credentialIds: [] },
      configs: [config],
      credentials: [credential],
      oneOff: [],
      write: vi.fn().mockResolvedValue({}),
    }),
  ).rejects.toThrow("凭证授权已保存，但 Agent 配置未全部写入");
});
it("turns a stalled operation into visible timeout feedback", async () => {
  vi.useFakeTimers();
  const result = expect(withCredentialTimeout(new Promise(() => {}), "保存", 20)).rejects.toThrow(
    "保存超时",
  );
  await vi.advanceTimersByTimeAsync(21);
  await result;
});
it("refuses empty configurations for unsupported generators", () => {
  expect(() => agentConfigurationFiles({ ...config, agent: "cursor" }, credential)).toThrow(
    "填写配置内容",
  );
});

it("applies temporary Agent files using only its sandbox rule and a placeholder", async () => {
  const write = vi.fn().mockResolvedValue({});
  const temporary = {
    config,
    connection: credential,
    rule: {
      key: "temporary",
      name: "Temporary",
      kind: "one-off" as const,
      spec: { type: "openai" as const },
      secret: { api_key: "temporary-secret" },
    },
  };
  await applySandboxCredentials({
    sandboxId: "sandbox",
    selection: { agentIds: [], credentialIds: [] },
    configs: [],
    credentials: [],
    oneOff: [],
    temporaryAgents: [temporary],
    write,
  });
  expect(fetchInjectionMaterials).toHaveBeenCalledWith([]);
  expect(write).toHaveBeenCalledWith([temporary.rule]);
  expect(writeSandboxAgentConfig).toHaveBeenCalledTimes(2);
  const files = JSON.stringify(vi.mocked(writeSandboxAgentConfig).mock.calls);
  expect(files).not.toContain("temporary-secret");
  expect(files).toContain("isodo-credential-credential");
});

it("conditions temporary Agent injection on its unique placeholder", () => {
  const temporary = temporaryAgentConnection(
    {
      name: "Temporary",
      type: "anthropic",
      base_url: "https://relay.example",
      secret: "temporary-secret",
    },
    "temp-id",
  );
  expect(temporary.rule.kind).toBe("one-off");
  expect(temporary.rule.spec.if_headers).toEqual({ "x-api-key": "isodo-credential-temp-id" });
  expect(temporary.rule.persistent_id).toBeUndefined();
});
