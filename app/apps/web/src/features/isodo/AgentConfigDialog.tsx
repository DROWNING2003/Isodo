import { useMemo, useState } from "react";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { Textarea } from "../../components/ui/textarea";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "../../components/ui/dialog";
import {
  AGENT_CONFIGS_STORAGE_KEY,
  AGENT_DEFAULT_PATHS,
  AGENT_LABELS,
  createAgentConfig,
  detectAndRedactConfig,
  type AgentConfig,
  type AgentConfigDraft,
  type SupportedAgent,
} from "./agentConfigs";
import type {
  CreateInjectionRule,
  InjectionRule,
  PatchInjectionRule,
} from "../../api/public.generated";
import { fetchInjectionMaterials } from "./injectionRules";
import { discoverAgentModels, validModelEndpoint, type ModelProtocol } from "./agentModelDiscovery";

const AGENTS = Object.keys(AGENT_LABELS) as SupportedAgent[];
const selectClass = "h-9 w-full rounded-md border border-input bg-background px-3 text-sm";

export function AgentConfigDialog({
  open,
  onOpenChange,
  connections,
  initial,
  initialConnectionId,
  temporary = false,
  resolveConnectionMaterial,
  onSave,
  onCreateConnection,
  onUpdateConnection,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  connections: InjectionRule[];
  initial?: AgentConfig;
  initialConnectionId?: string;
  temporary?: boolean;
  resolveConnectionMaterial?: () => Promise<
    { api_key?: string; token?: string; headers?: Record<string, string> } | undefined
  >;
  onSave: (config: AgentConfig) => void | Promise<void>;
  onCreateConnection: (input: CreateInjectionRule) => Promise<InjectionRule>;
  onUpdateConnection?: (id: string, input: PatchInjectionRule) => Promise<InjectionRule>;
}) {
  const initialConnection = connections.find(
    (item) => item.id === (initial?.connectionId ?? initialConnectionId),
  );
  const [draft, setDraft] = useState<AgentConfigDraft>(() => ({
    name: initial?.name ?? initialConnection?.name ?? "",
    agent: initial?.agent ?? "codex",
    connectionId: initial?.connectionId ?? initialConnectionId ?? "",
    modelNames: initial?.modelNames ?? [],
    defaultModel: initial?.defaultModel ?? "",
    filePath: initial?.filePath ?? AGENT_DEFAULT_PATHS.codex,
    content: initial?.content ?? "",
  }));
  const [endpoint, setEndpoint] = useState(initialConnection?.base_url ?? "");
  const [protocol, setProtocol] = useState<ModelProtocol>(
    initialConnection?.type === "anthropic" ? "anthropic" : "openai",
  );
  const [apiKey, setApiKey] = useState("");
  const [showSaved, setShowSaved] = useState(false);
  const [createdConnection, setCreatedConnection] = useState<InjectionRule>();
  const [models, setModels] = useState<string[]>(initial?.modelNames ?? []);
  const [modelSearch, setModelSearch] = useState("");
  const [manualModel, setManualModel] = useState("");
  const [loadingModels, setLoadingModels] = useState(false);
  const [saving, setSaving] = useState(false);
  const [modelError, setModelError] = useState<string | null>(null);
  const [showAdvanced, setShowAdvanced] = useState(Boolean(initial?.content));
  const [error, setError] = useState<string | null>(null);
  const connection =
    createdConnection ?? connections.find((item) => item.id === draft.connectionId);
  const busy = saving || loadingModels;
  const filteredModels = useMemo(
    () => models.filter((model) => model.toLowerCase().includes(modelSearch.toLowerCase())),
    [modelSearch, models],
  );
  const update = <K extends keyof AgentConfigDraft>(key: K, value: AgentConfigDraft[K]) =>
    setDraft((current) => ({ ...current, [key]: value }));
  const selectModel = (model: string, selected: boolean) =>
    setDraft((current) => {
      const modelNames = selected
        ? [...new Set([...current.modelNames, model])]
        : current.modelNames.filter((item) => item !== model);
      return {
        ...current,
        modelNames,
        defaultModel: modelNames.includes(current.defaultModel)
          ? current.defaultModel
          : (modelNames[0] ?? ""),
      };
    });
  const chooseConnection = (id: string) => {
    const next = connections.find((item) => item.id === id);
    update("connectionId", id);
    setCreatedConnection(undefined);
    setApiKey("");
    setEndpoint(next?.base_url ?? "");
    setProtocol(next?.type === "anthropic" ? "anthropic" : "openai");
    setModelError(null);
  };
  const discoverModels = async () => {
    if (!validModelEndpoint(endpoint.trim())) {
      setModelError("请填写有效的 API 地址，例如 https://relay.example.com/v1。");
      return;
    }
    if (!apiKey.trim() && !connection) {
      setModelError("请先填写 API Key，也可以直接手填模型名。");
      return;
    }
    setLoadingModels(true);
    setModelError(null);
    try {
      const material = apiKey.trim()
        ? connection?.type === "http"
          ? { headers: { [connection.header_name || "Authorization"]: apiKey.trim() } }
          : { api_key: apiKey.trim() }
        : resolveConnectionMaterial
          ? await resolveConnectionMaterial()
          : (await fetchInjectionMaterials([connection!.id])).get(connection!.id);
      if (!material) throw new Error("凭证已删除，请重新填写 API Key");
      const discovered = await discoverAgentModels(
        endpoint,
        connection?.type ?? protocol,
        material,
      );
      setModels((current) => [...new Set([...current, ...discovered])]);
    } catch (cause) {
      setModelError(
        cause instanceof Error && cause.message.startsWith("HTTP ")
          ? `获取模型失败（${cause.message}），请检查地址和 API Key，也可手填模型名。`
          : "未能获取模型，请检查地址、网络和接口跨域设置，也可手填模型名。",
      );
    } finally {
      setLoadingModels(false);
    }
  };
  const save = async () => {
    setError(null);
    const name = draft.name.trim();
    if (!name) {
      setError("请填写配置名称。");
      return;
    }
    if (!validModelEndpoint(endpoint.trim())) {
      setError("请填写有效的 API 地址。");
      return;
    }
    if (!connection && !apiKey.trim()) {
      setError("请填写 API Key。");
      return;
    }
    if (!draft.modelNames.length) {
      setError("请添加或选择至少一个模型。");
      return;
    }
    const redacted = detectAndRedactConfig(
      detectAndRedactConfig(draft.content, { secret_hint: "", secret: apiKey.trim() }).content,
      { secret_hint: "" },
    );
    setSaving(true);
    try {
      let savedConnection = connection;
      if (!savedConnection) {
        savedConnection = await onCreateConnection({
          name: name.slice(0, 64),
          type: protocol,
          base_url: endpoint.trim().replace(/\/+$/, ""),
          secret: apiKey.trim(),
        });
        setCreatedConnection(savedConnection);
      } else if (endpoint.trim() !== savedConnection.base_url || apiKey.trim()) {
        if (!onUpdateConnection) throw new Error("当前无法更新凭证，请重试。");
        savedConnection = await onUpdateConnection(savedConnection.id, {
          base_url: endpoint.trim().replace(/\/+$/, ""),
          ...(apiKey.trim() ? { secret: apiKey.trim() } : {}),
        });
        setCreatedConnection(savedConnection);
      }
      setApiKey("");
      update("content", redacted.content);
      const values = {
        ...draft,
        name,
        connectionId: savedConnection.id,
        defaultModel: draft.defaultModel || draft.modelNames[0]!,
        filePath: draft.filePath.trim() || AGENT_DEFAULT_PATHS[draft.agent],
        content: redacted.content,
      };
      await onSave(
        initial
          ? { ...initial, ...values, updatedAt: new Date().toISOString() }
          : createAgentConfig(values),
      );
      onOpenChange(false);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "保存失败，请重试。");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogPopup className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {temporary ? "临时 Agent 配置" : initial ? "编辑 Agent 配置" : "添加 Agent 配置"}
          </DialogTitle>
          <DialogDescription>
            {temporary
              ? "仅用于当前沙箱，保存后立即应用。"
              : "填写模型接口，选择要在沙箱中使用的模型。"}
          </DialogDescription>
        </DialogHeader>
        <DialogPanel>
          <fieldset disabled={busy} className="grid min-w-0 gap-5">
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="grid gap-1.5">
                <label htmlFor="agent-config-name" className="text-sm font-medium">
                  名称
                </label>
                <Input
                  id="agent-config-name"
                  value={draft.name}
                  onChange={(e) => update("name", e.target.value)}
                  placeholder="例如：我的 Codex"
                  autoFocus
                />
              </div>
              <div className="grid gap-1.5">
                <label htmlFor="agent-kind" className="text-sm font-medium">
                  Agent
                </label>
                <select
                  id="agent-kind"
                  className={selectClass}
                  value={draft.agent}
                  onChange={(e) => {
                    const agent = e.target.value as SupportedAgent;
                    update("agent", agent);
                    update("filePath", AGENT_DEFAULT_PATHS[agent]);
                    if (!connection) setProtocol(agent === "claude" ? "anthropic" : "openai");
                  }}
                >
                  {AGENTS.map((agent) => (
                    <option key={agent} value={agent}>
                      {AGENT_LABELS[agent]}
                    </option>
                  ))}
                </select>
              </div>
            </div>
            {connections.length ? (
              <div className="grid gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="h-auto justify-start px-0 text-muted-foreground"
                  aria-expanded={showSaved}
                  onClick={() => setShowSaved((value) => !value)}
                >
                  从已保存凭证填入
                </Button>
                {showSaved ? (
                  <select
                    id="agent-connection"
                    aria-label="已保存的凭证"
                    className={selectClass}
                    value={connection?.id ?? ""}
                    onChange={(e) => chooseConnection(e.target.value)}
                  >
                    <option value="">填写新凭证</option>
                    {connections.map((item) => (
                      <option key={item.id} value={item.id}>
                        {item.name} · {item.base_url || item.type}
                      </option>
                    ))}
                  </select>
                ) : null}
              </div>
            ) : null}
            <div className="grid gap-1.5">
              <label htmlFor="agent-endpoint" className="text-sm font-medium">
                API 地址 / Endpoint
              </label>
              <Input
                id="agent-endpoint"
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
                placeholder="https://relay.example.com/v1"
                autoComplete="off"
                spellCheck={false}
              />
              <p className="text-xs text-muted-foreground">填写官方 API 或中转站提供的地址。</p>
            </div>
            <div className="grid gap-1.5">
              <label htmlFor="agent-protocol" className="text-sm font-medium">
                API 协议
              </label>
              <select
                id="agent-protocol"
                className={selectClass}
                value={protocol}
                disabled={Boolean(connection)}
                onChange={(e) => setProtocol(e.target.value as ModelProtocol)}
              >
                <option value="openai">OpenAI 兼容</option>
                <option value="anthropic">Anthropic</option>
              </select>
            </div>
            <div className="grid gap-1.5">
              <label htmlFor="agent-api-key" className="text-sm font-medium">
                API Key
              </label>
              <Input
                id="agent-api-key"
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder={connection ? "已保存；留空保留当前密钥" : "粘贴 API Key"}
                autoComplete="new-password"
                spellCheck={false}
              />
              {connection ? (
                <p className="text-xs text-muted-foreground">
                  修改此凭证会影响引用它的其他 Agent 配置。
                </p>
              ) : null}
            </div>
            <div className="grid gap-3 border-t border-border pt-4">
              <div className="flex items-center justify-between">
                <label htmlFor="agent-model-search" className="text-sm font-medium">
                  模型
                </label>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => void discoverModels()}
                  disabled={busy || !endpoint.trim()}
                >
                  {loadingModels ? "获取中…" : "获取模型"}
                </Button>
              </div>
              {models.length ? (
                <Input
                  id="agent-model-search"
                  value={modelSearch}
                  onChange={(e) => setModelSearch(e.target.value)}
                  placeholder="搜索模型"
                />
              ) : (
                <p className="text-sm text-muted-foreground">获取可用模型，或手动添加模型 ID。</p>
              )}
              <div className="grid max-h-40 gap-2 overflow-auto">
                {filteredModels.map((model) => (
                  <label key={model} className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={draft.modelNames.includes(model)}
                      onChange={(e) => selectModel(model, e.target.checked)}
                    />
                    <span className="min-w-0 break-all">{model}</span>
                  </label>
                ))}
              </div>
              <div className="flex gap-2">
                <Input
                  id="agent-manual-model"
                  aria-label="模型 ID"
                  value={manualModel}
                  onChange={(e) => setManualModel(e.target.value)}
                  placeholder="手动填写模型 ID"
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      const model = manualModel.trim();
                      if (model) {
                        setModels((current) => [...new Set([...current, model])]);
                        selectModel(model, true);
                        setManualModel("");
                      }
                    }
                  }}
                />
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => {
                    const model = manualModel.trim();
                    if (model) {
                      setModels((current) => [...new Set([...current, model])]);
                      selectModel(model, true);
                      setManualModel("");
                    }
                  }}
                >
                  添加
                </Button>
              </div>
              {modelError ? (
                <p role="alert" className="text-xs text-destructive">
                  {modelError}
                </p>
              ) : null}
              {draft.modelNames.length ? (
                <div className="grid gap-1.5">
                  <label htmlFor="agent-default-model" className="text-sm font-medium">
                    默认模型
                  </label>
                  <select
                    id="agent-default-model"
                    className={selectClass}
                    value={draft.defaultModel}
                    onChange={(e) => update("defaultModel", e.target.value)}
                  >
                    {draft.modelNames.map((model) => (
                      <option key={model} value={model}>
                        {model}
                      </option>
                    ))}
                  </select>
                </div>
              ) : null}
            </div>
            <div className="grid gap-3 border-t border-border pt-3">
              <Button
                type="button"
                variant="ghost"
                className="h-auto justify-start px-0 text-muted-foreground"
                aria-expanded={showAdvanced}
                onClick={() => setShowAdvanced((value) => !value)}
              >
                {showAdvanced ? "收起高级设置" : "高级设置"}
              </Button>
              {showAdvanced ? (
                <>
                  <div className="grid gap-1.5">
                    <label htmlFor="agent-config-path" className="text-sm font-medium">
                      配置文件路径
                    </label>
                    <Input
                      id="agent-config-path"
                      value={draft.filePath}
                      onChange={(e) => update("filePath", e.target.value)}
                    />
                  </div>
                  <div className="grid gap-1.5">
                    <label htmlFor="agent-config-content" className="text-sm font-medium">
                      配置内容（可选）
                    </label>
                    <Textarea
                      id="agent-config-content"
                      value={draft.content}
                      onChange={(e) => update("content", e.target.value)}
                      rows={8}
                      placeholder="粘贴已有配置；留空时自动生成"
                    />
                    <p className="text-xs text-muted-foreground">
                      保存时会将识别到的密钥替换为占位符。
                    </p>
                  </div>
                </>
              ) : null}
            </div>
          </fieldset>
        </DialogPanel>
        <DialogFooter className="flex-col sm:flex-col sm:items-stretch">
          {error ? (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          ) : null}
          <div className="flex justify-end gap-2">
            <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              取消
            </Button>
            <Button disabled={busy} onClick={() => void save()}>
              {saving ? "保存中…" : "保存 Agent 配置"}
            </Button>
          </div>
        </DialogFooter>
      </DialogPopup>
    </Dialog>
  );
}
export const agentConfigStorageKey = AGENT_CONFIGS_STORAGE_KEY;
