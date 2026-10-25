import type { TemporaryAgent } from "./TemporaryAgentDialog";
import type { InjectionRule } from "../../api/public.generated";
import { SECRET_PLACEHOLDER, type AgentConfig } from "./agentConfigs";
import {
  fetchInjectionMaterials,
  localRuleFromPersistent,
  writeSandboxAgentConfig,
  type LocalInjectionRule,
} from "./injectionRules";
import type { CredentialSelection } from "./sandboxCredentialSelection";

export async function withCredentialTimeout<T>(
  operation: Promise<T>,
  label: string,
  timeoutMs = 20_000,
): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      operation,
      new Promise<never>((_resolve, reject) => {
        timer = setTimeout(
          () => reject(new Error(`${label}超时，请重新加载确认当前状态后重试。`)),
          timeoutMs,
        );
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

export function agentConfigurationFiles(
  config: AgentConfig,
  connection: InjectionRule,
): { path: string; content: string }[] {
  const placeholder = `isodo-credential-${connection.id}`;
  if (config.content.trim())
    return [
      {
        path: config.filePath,
        content: config.content.split(SECRET_PLACEHOLDER).join(placeholder),
      },
    ];
  const baseUrl =
    connection.base_url ||
    (connection.type === "anthropic" ? "https://api.anthropic.com" : "https://api.openai.com/v1");
  if (!config.defaultModel) throw new Error(`${config.name} 未指定默认模型，请在凭证库中补充。`);
  if (config.agent === "codex")
    return [
      {
        path: config.filePath,
        content: `model_provider = "isodo"\nmodel = ${JSON.stringify(config.defaultModel)}\n\n[model_providers.isodo]\nname = "Isodo"\nbase_url = ${JSON.stringify(baseUrl)}\nwire_api = "responses"\nrequires_openai_auth = true\n`,
      },
      {
        path: "~/.codex/auth.json",
        content: JSON.stringify({ OPENAI_API_KEY: placeholder }),
      },
    ];
  if (config.agent === "claude")
    return [
      {
        path: config.filePath,
        content: JSON.stringify(
          {
            model: config.defaultModel,
            env: { ANTHROPIC_BASE_URL: baseUrl, ANTHROPIC_API_KEY: placeholder },
          },
          null,
          2,
        ),
      },
    ];
  if (config.agent === "opencode")
    return [
      {
        path: config.filePath,
        content: JSON.stringify(
          {
            model: `isodo/${config.defaultModel}`,
            provider: {
              isodo: {
                npm:
                  connection.type === "anthropic"
                    ? "@ai-sdk/anthropic"
                    : "@ai-sdk/openai-compatible",
                name: connection.name,
                options: { baseURL: baseUrl, apiKey: placeholder },
                models: Object.fromEntries(config.modelNames.map((id) => [id, { name: id }])),
              },
            },
          },
          null,
          2,
        ),
      },
    ];
  throw new Error(`${config.name} 需要在凭证库中填写配置内容。`);
}

/** One explicit action binds credentials and installs the selected Agent files. */
export async function applySandboxCredentials(input: {
  sandboxId: string;
  selection: CredentialSelection;
  configs: readonly AgentConfig[];
  credentials: readonly InjectionRule[];
  oneOff: readonly LocalInjectionRule[];
  temporaryAgents?: readonly TemporaryAgent[];
  write: (rules: readonly LocalInjectionRule[]) => Promise<unknown>;
  onStage?: (stage: string) => void;
}): Promise<void> {
  const configs = input.selection.agentIds.map((id) => {
    const config = input.configs.find((item) => item.id === id);
    if (!config) throw new Error("选中的 Agent 配置已删除，请重新选择。");
    return config;
  });
  const ids = [
    ...new Set([...input.selection.credentialIds, ...configs.map((item) => item.connectionId)]),
  ];
  const connections = ids.map((id) => {
    const rule = input.credentials.find((item) => item.id === id);
    if (!rule) throw new Error("选中的凭证已删除，请重新选择。");
    return rule;
  });
  const files = configs.flatMap((config) =>
    agentConfigurationFiles(
      config,
      connections.find((item) => item.id === config.connectionId)!,
    ),
  );
  files.push(
    ...(input.temporaryAgents ?? []).flatMap((item) =>
      agentConfigurationFiles(item.config, item.connection),
    ),
  );
  const paths = files.map((file) => file.path);
  if (new Set(paths).size !== paths.length)
    throw new Error("所选 Agent 配置的文件路径重复，请为每个 Agent 只选择一份配置。");
  input.onStage?.("正在读取凭证…");
  const materials = await withCredentialTimeout(fetchInjectionMaterials(ids), "读取凭证");
  const rules = connections.map((rule) => {
    const material = materials.get(rule.id);
    if (!material) throw new Error("凭证材料不可用，请重新选择。");
    const localRule = localRuleFromPersistent(rule, material);
    if (
      configs.some((config) => config.connectionId === rule.id) &&
      !input.selection.credentialIds.includes(rule.id)
    ) {
      const header =
        rule.type === "anthropic"
          ? "x-api-key"
          : rule.type === "gemini"
            ? "x-goog-api-key"
            : rule.type === "http"
              ? rule.header_name || "Authorization"
              : "Authorization";
      const placeholder = `isodo-credential-${rule.id}`;
      const expected =
        header.toLowerCase() === "authorization" ? `Bearer ${placeholder}` : placeholder;
      const existing = Object.entries(rule.if_headers ?? {}).find(
        ([key]) => key.toLowerCase() === header.toLowerCase(),
      );
      if (existing && existing[1] !== expected)
        throw new Error(`${rule.name} 的认证 Header 条件与 Agent 配置不一致，请先在凭证库调整。`);
      localRule.spec = {
        ...localRule.spec,
        if_headers: { ...localRule.spec.if_headers, [header]: expected },
      };
    }
    return localRule;
  });
  input.onStage?.("正在保存凭证授权…");
  await withCredentialTimeout(
    input.write([
      ...rules,
      ...input.oneOff,
      ...(input.temporaryAgents ?? []).map((item) => item.rule),
    ]),
    "保存凭证授权",
  );
  try {
    for (const file of files) {
      input.onStage?.(`正在写入 ${file.path}…`);
      await withCredentialTimeout(
        writeSandboxAgentConfig(input.sandboxId, file),
        "写入 Agent 配置",
      );
    }
  } catch (cause) {
    throw new Error(
      `凭证授权已保存，但 Agent 配置未全部写入：${cause instanceof Error ? cause.message : "请重试。"}`,
      { cause },
    );
  }
}

export async function saveSandboxCredentialSelection(
  sandboxId: string,
  value: CredentialSelection,
  configs: readonly AgentConfig[] = [],
  temporary: {
    temporaryAgents: readonly TemporaryAgent[];
    oneOff: readonly LocalInjectionRule[];
  } = { temporaryAgents: [], oneOff: [] },
) {
  const { putCredentialDocument } = await import("./credentialDocuments");
  const models = configs
    .filter((config) => value.agentIds.includes(config.id))
    .map(({ id, agent, modelNames, defaultModel }) => ({ id, agent, modelNames, defaultModel }));
  await putCredentialDocument("sandboxes", sandboxId, {
    ...value,
    models: [
      ...models,
      ...temporary.temporaryAgents.map(({ config: { id, agent, modelNames, defaultModel } }) => ({
        id,
        agent,
        modelNames,
        defaultModel,
      })),
    ],
    ...temporary,
  });
}
