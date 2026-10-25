import type { EnvironmentId } from "@t3tools/contracts";
import { useNavigate } from "@tanstack/react-router";
import { LoaderIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { createSandbox as createPlatformSandbox } from "../../api/public.generated";
import { loadCloudAppTemplates } from "./cloudAppTemplates";
import { connectPairing } from "../../connection/onboarding";
import { isElectron } from "../../env";
import { setActiveEnvironmentId } from "../../state/entities";
import { useAtomCommand } from "../../state/use-atom-command";
import { Button } from "../../components/ui/button";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "../../components/ui/dialog";
import { Input } from "../../components/ui/input";
import {
  Select,
  SelectItem,
  SelectPopup,
  SelectTrigger,
  SelectValue,
} from "../../components/ui/select";
import { invalidateSandboxLists, updateLocalSandboxCache } from "./sandboxQueries";
import { rememberSandboxEnvironment } from "./sandboxEnvironmentMap";
import { useAuth } from "./auth";
import { useServerAgentConfigs } from "./credentialDocuments";
import { useInjectionRules } from "./injectionRules";
import {
  CredentialSelectionFields,
  EMPTY_CREDENTIAL_SELECTION,
  type CredentialSelection,
} from "./sandboxCredentialSelection";
import { applySandboxCredentials, saveSandboxCredentialSelection } from "./applySandboxCredentials";

/** Every published application template inherits the base runtime. */
const BASE_TEMPLATE: TemplateOption = { id: "base", label: "base", ready: true };
const LOCAL_BASE_TEMPLATE: TemplateOption = { id: "base", label: "base", ready: true };
const LOCAL_OPERATION_TIMEOUT_MS = 5 * 60 * 1000;
const LOCAL_TEMPLATE_BUILD_TIMEOUT_MS = 20 * 60 * 1000;

type Runtime = "local" | "cloud";

interface TemplateOption {
  readonly id: string;
  readonly label: string;
  readonly ready: boolean;
}

interface LocalTemplate {
  readonly id: string;
  readonly name?: string;
  readonly ready?: boolean;
  readonly building?: boolean;
}

/** Electron prefixes IPC failures with transport noise; keep the cause only. */
function describeSandboxError(reason: unknown): string {
  const message = reason instanceof Error ? reason.message : String(reason);
  const cleaned = message
    .replace(/^Error invoking remote method '[^']+':\s*/i, "")
    .replace(/^(Error|Error:)\s*/i, "");
  return cleaned || "创建沙箱失败，请稍后重试。";
}

interface LocalSandboxAccepted {
  readonly sandbox_id: string;
  // The local API serializes protobuf structs with encoding/json, so field
  // names stay snake_case and enum states stay numeric.
  readonly operation: {
    readonly operation_id?: string;
    readonly operationId?: string;
    readonly state?: number;
  };
}

/** Platform operation states: 3 succeeded, 4 failed. */
const OPERATION_SUCCEEDED = 3;
const OPERATION_FAILED = 4;

export function CreateSandboxDialog() {
  const navigate = useNavigate();
  const authState = useAuth();
  const connectPairingEnv = useAtomCommand(connectPairing, { reportFailure: false });
  const credentials = useInjectionRules();
  const agentConfigsQuery = useServerAgentConfigs();
  const agentConfigs = agentConfigsQuery.data ?? [];
  const [credentialSelection, setCredentialSelection] = useState<CredentialSelection>(
    EMPTY_CREDENTIAL_SELECTION,
  );
  const [credentialStage, setCredentialStage] = useState("");
  const acceptedLocal = useRef<LocalSandboxAccepted | null>(null);
  const [runtime, setRuntime] = useState<Runtime>(isElectron ? "local" : "cloud");
  const [templateID, setTemplateID] = useState(BASE_TEMPLATE.id);
  const [templates, setTemplates] = useState<readonly TemplateOption[]>([BASE_TEMPLATE]);
  const [templatesLoaded, setTemplatesLoaded] = useState(false);
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const close = () => void navigate({ to: "/sandboxes" });
  const local = runtime === "local";

  useEffect(() => {
    setTemplateID(local ? LOCAL_BASE_TEMPLATE.id : BASE_TEMPLATE.id);
  }, [local]);

  useEffect(() => {
    let active = true;
    setTemplatesLoaded(false);
    if (local) {
      const bridge = window.isodoDesktop;
      if (!bridge) {
        setTemplates([LOCAL_BASE_TEMPLATE]);
        setTemplatesLoaded(true);
        return;
      }
      void bridge
        .listLocalTemplates()
        .then((result) => {
          if (!active) return;
          const items = (result as { items?: LocalTemplate[] }).items ?? [];
          setTemplates(
            items.length === 0
              ? [LOCAL_BASE_TEMPLATE]
              : items.map((item) => ({
                  id: item.id,
                  label: item.name ?? item.id,
                  ready: item.ready !== false && item.building !== true,
                })),
          );
        })
        .catch(() => {
          if (active) setTemplates([LOCAL_BASE_TEMPLATE]);
        })
        .finally(() => {
          if (active) setTemplatesLoaded(true);
        });
      return () => {
        active = false;
      };
    }
    if (authState.status !== "authenticated") {
      setTemplates([BASE_TEMPLATE]);
      setTemplatesLoaded(true);
      return;
    }
    setTemplates([]);
    setTemplateID("");
    void loadCloudAppTemplates()
      .then((options) => {
        if (!active) return;
        setTemplates(options);
        setTemplateID(options[0]?.id ?? "");
      })
      .catch(() => {
        if (active) setError("云端模板读取失败，请关闭后重试。");
      })
      .finally(() => {
        if (active) setTemplatesLoaded(true);
      });
    return () => {
      active = false;
    };
  }, [local, authState.status]);

  const selectedTemplate = templates.find((template) => template.id === templateID);
  const templateHint = local
    ? !templatesLoaded
      ? "正在读取模板…"
      : selectedTemplate && !selectedTemplate.ready
        ? "首次使用会先构建模板，可能需要几分钟。"
        : `共 ${templates.length} 个本地模板。`
    : authState.status !== "authenticated"
      ? "未登录时仅提供 base 模板。"
      : templatesLoaded
        ? templates.length > 0
          ? `共 ${templates.length} 个可用模板。`
          : "暂无已发布的可用模板，请先发布 base 模板。"
        : "正在读取模板…";

  const createLocal = async (): Promise<void> => {
    const bridge = window.isodoDesktop;
    if (!bridge) throw new Error("本地沙箱只能在桌面应用中创建");
    if (!acceptedLocal.current && selectedTemplate && !selectedTemplate.ready) {
      await bridge.buildLocalTemplate(selectedTemplate.id);
      const buildDeadline = Date.now() + LOCAL_TEMPLATE_BUILD_TIMEOUT_MS;
      for (;;) {
        if (Date.now() > buildDeadline) throw new Error("模板构建超时");
        await new Promise((resolve) => setTimeout(resolve, 2000));
        const listed = (await bridge.listLocalTemplates()) as { items?: LocalTemplate[] };
        const current = listed.items?.find((item) => item.id === selectedTemplate.id);
        if (current?.ready) break;
        if (current && current.building !== true) break;
      }
    }
    const accepted =
      acceptedLocal.current ??
      ((await bridge.createLocalSandbox({
        name: name.trim(),
        ...(selectedTemplate ? { template: selectedTemplate.id } : {}),
      })) as LocalSandboxAccepted);
    acceptedLocal.current = accepted;
    const operationID = accepted.operation?.operation_id ?? accepted.operation?.operationId;
    if (!operationID) throw new Error("本地沙箱创建没有返回 operation ID");
    let state = accepted.operation.state ?? 0;
    const deadline = Date.now() + LOCAL_OPERATION_TIMEOUT_MS;
    while (state !== OPERATION_SUCCEEDED && state !== OPERATION_FAILED) {
      if (Date.now() > deadline) throw new Error("本地沙箱创建超时");
      await new Promise((resolve) => setTimeout(resolve, 1000));
      const operation = (await bridge.getLocalOperation(operationID)) as { state?: number };
      state = operation.state ?? state;
    }
    if (state === OPERATION_FAILED) throw new Error("本地沙箱创建失败");
    if (credentialSelection.agentIds.length || credentialSelection.credentialIds.length) {
      await applySandboxCredentials({
        sandboxId: accepted.sandbox_id,
        selection: credentialSelection,
        configs: agentConfigs,
        credentials: credentials.data ?? [],
        oneOff: [],
        write: (rules) => bridge.setSandboxInjections(accepted.sandbox_id, rules),
        onStage: setCredentialStage,
      });
      await saveSandboxCredentialSelection(accepted.sandbox_id, credentialSelection, agentConfigs);
      setCredentialStage("");
    }
    const link = (await bridge.getLocalSandboxApp(accepted.sandbox_id)) as { url: string };
    const connected = await connectPairingEnv({ pairingUrl: link.url });
    if (connected._tag !== "Success") throw new Error("本地 T3 连接失败");
    rememberSandboxEnvironment(connected.value as EnvironmentId, accepted.sandbox_id);
    updateLocalSandboxCache((items) => [
      ...(items ?? []).filter((item) => item.id !== accepted.sandbox_id),
      { id: accepted.sandbox_id, name: name.trim(), state: "running" },
    ]);
    invalidateSandboxLists();
    setActiveEnvironmentId(connected.value as EnvironmentId);
    await navigate({ to: "/sandboxes" });
  };

  const createCloud = async (): Promise<void> => {
    if (authState.status === "unauthenticated") {
      // Signed-out visitors keep the demo cloud path; it never calls the API.
      const sandbox = await import("./cloud").then(({ cloud }) =>
        cloud.createSandbox({ name: name.trim(), template: "dev" }),
      );
      await navigate({ to: "/sandboxes/$sandboxId", params: { sandboxId: sandbox.id } });
      return;
    }
    if (authState.status !== "authenticated") {
      throw new Error("登录状态尚未确认，无法创建云端沙箱");
    }
    if (!templatesLoaded || !selectedTemplate?.ready) {
      throw new Error("请选择已发布的 Ready 模板");
    }
    await createPlatformSandbox({ name: name.trim(), template: selectedTemplate.id });
    invalidateSandboxLists();
    await navigate({ to: "/sandboxes" });
  };

  const submit = async () => {
    if (creating) return;
    setCreating(true);
    setError(null);
    try {
      if (local) await createLocal();
      else await createCloud();
    } catch (reason) {
      setError(
        `${acceptedLocal.current ? "沙箱已创建，后续配置或连接失败；重试会继续使用此沙箱。" : ""}${describeSandboxError(reason)}`,
      );
      setCredentialStage("");
      setCreating(false);
    }
  };

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next && !creating) close();
      }}
    >
      <DialogPopup className="max-w-lg">
        <DialogHeader>
          <DialogTitle>创建沙箱</DialogTitle>
          <DialogDescription>选择运行位置和模板，Agent 会在沙箱内准备。</DialogDescription>
        </DialogHeader>
        <DialogPanel>
          <form
            className="grid gap-5"
            onSubmit={(event) => {
              event.preventDefault();
              void submit();
            }}
          >
            <div className="grid gap-1.5">
              <span className="text-sm font-medium">运行位置</span>
              <div className="grid grid-cols-2 gap-2">
                <Button
                  type="button"
                  variant={local ? "default" : "outline"}
                  disabled={!isElectron || creating || Boolean(acceptedLocal.current)}
                  aria-pressed={local}
                  onClick={() => setRuntime("local")}
                >
                  本地沙箱
                </Button>
                <Button
                  type="button"
                  variant={local ? "outline" : "default"}
                  disabled={creating || Boolean(acceptedLocal.current)}
                  aria-pressed={!local}
                  onClick={() => setRuntime("cloud")}
                >
                  云端沙箱
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                {isElectron
                  ? "本地沙箱在你的电脑上运行，云端沙箱在服务端运行。"
                  : "本地沙箱需要在桌面应用中使用。"}
              </p>
            </div>

            <div className="grid gap-1.5">
              <label className="text-sm font-medium" htmlFor="sandbox-template">
                模板
              </label>
              <Select
                value={templateID}
                onValueChange={(next) => setTemplateID(String(next))}
                disabled={creating}
              >
                <SelectTrigger id="sandbox-template" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectPopup align="start">
                  {templates.map((template) => (
                    <SelectItem key={template.id} value={template.id}>
                      {template.label}
                    </SelectItem>
                  ))}
                </SelectPopup>
              </Select>
              <p className="text-xs text-muted-foreground">{templateHint}</p>
            </div>

            <div className="grid gap-1.5">
              <label className="text-sm font-medium" htmlFor="sandbox-name">
                沙箱名称
              </label>
              <Input
                id="sandbox-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                maxLength={48}
                placeholder={local ? "我的本地工作空间" : "我的云端工作空间"}
                disabled={creating}
                autoFocus
              />
            </div>

            <details className="group border-t border-border pt-4">
              <summary className="cursor-pointer text-sm font-medium">更多选项</summary>
              <div className="mt-4 grid gap-3">
                <p className="text-sm text-muted-foreground">
                  选择要在沙箱中使用的凭证，也可以稍后添加。
                </p>
                {!local ? (
                  <p className="text-sm text-muted-foreground">云端暂不支持配置凭证。</p>
                ) : credentials.isError ? (
                  <p role="alert" className="text-sm text-destructive">
                    {credentials.error instanceof Error
                      ? credentials.error.message
                      : "凭证读取失败，请稍后重试。"}
                  </p>
                ) : credentials.isLoading ? (
                  <p className="text-sm text-muted-foreground">正在读取凭证…</p>
                ) : (
                  <CredentialSelectionFields
                    configs={agentConfigs}
                    credentials={credentials.data ?? []}
                    value={credentialSelection}
                    onChange={setCredentialSelection}
                    disabled={creating}
                  />
                )}
              </div>
            </details>
          </form>
        </DialogPanel>
        <DialogFooter className="flex-wrap">
          {error ? (
            <p role="alert" className="w-full text-sm text-destructive">
              {error}
            </p>
          ) : null}
          {creating ? (
            <span
              className="mr-auto flex items-center gap-2 text-sm text-muted-foreground"
              role="status"
            >
              <LoaderIcon className="size-4 animate-spin" />
              {credentialStage || (local ? "正在创建本地沙箱…" : "正在创建云端沙箱…")}
            </span>
          ) : null}
          {authState.status === "error" && !local ? (
            <p className="mr-auto text-sm text-destructive">登录服务暂时不可用。</p>
          ) : null}
          <Button variant="outline" onClick={close} disabled={creating}>
            取消
          </Button>
          <Button
            onClick={() => void submit()}
            disabled={
              creating ||
              (!local &&
                authState.status === "authenticated" &&
                (!templatesLoaded || !selectedTemplate?.ready))
            }
          >
            {creating
              ? "正在创建…"
              : acceptedLocal.current
                ? "重试配置并连接"
                : local
                  ? "创建本地沙箱"
                  : "创建云端沙箱"}
          </Button>
        </DialogFooter>
      </DialogPopup>
    </Dialog>
  );
}
