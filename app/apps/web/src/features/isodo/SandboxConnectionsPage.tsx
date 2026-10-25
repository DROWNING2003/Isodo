import { TemporaryAgentDialog, type TemporaryAgent } from "./TemporaryAgentDialog";
import { useEffect, useState } from "react";
import { LoaderIcon, PlusIcon, SettingsIcon, Trash2Icon, SaveIcon } from "lucide-react";
import { Button } from "../../components/ui/button";
import {
  Dialog,
  DialogDescription,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "../../components/ui/dialog";
import { CloudPage } from "./CloudPages";
import {
  ruleSecretPreview,
  ruleTarget,
  useInjectionRules,
  useSandboxInjectionWriter,
  useSandboxInjections,
  type LocalInjectionRule,
} from "./injectionRules";
import { useServerAgentConfigs, useServerSandboxCredentials } from "./credentialDocuments";
import {
  editorValuesToSpec,
  InjectionRuleDialog,
  type InjectionEditorValues,
} from "./InjectionRuleDialog";
import { useLocalRuntimeSandbox, usePlatformSandbox } from "./usePlatformSandbox";
import { randomUUID } from "../../lib/utils";
import {
  CredentialSelectionFields,
  EMPTY_CREDENTIAL_SELECTION,
  type CredentialSelection,
} from "./sandboxCredentialSelection";
import { applySandboxCredentials, saveSandboxCredentialSelection } from "./applySandboxCredentials";

export function SandboxConnectionsPage({ sandboxId }: { sandboxId: string }) {
  const localRuntime = useLocalRuntimeSandbox(sandboxId);
  const platform = usePlatformSandbox(localRuntime.record ? "" : sandboxId);
  const localSupported = Boolean(window.isodoDesktop) && Boolean(localRuntime.record);
  const rulesQuery = useInjectionRules();
  const configQuery = useSandboxInjections(sandboxId, localSupported);
  const write = useSandboxInjectionWriter(sandboxId);
  const selectionQuery = useServerSandboxCredentials();
  const agentConfigsQuery = useServerAgentConfigs();
  const configs = agentConfigsQuery.data ?? [];
  const [selection, setSelection] = useState<CredentialSelection>(EMPTY_CREDENTIAL_SELECTION);
  const [temporaryAgents, setTemporaryAgents] = useState<TemporaryAgent[]>([]);
  const [agentDialog, setAgentDialog] = useState<{ initial?: TemporaryAgent } | null>(null);
  const [oneOff, setOneOff] = useState<readonly LocalInjectionRule[]>([]);
  const [initialized, setInitialized] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<LocalInjectionRule>();
  const [deleting, setDeleting] = useState<LocalInjectionRule | null>(null);
  const [pending, setPending] = useState(false);
  const [stage, setStage] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    if (
      initialized ||
      !configQuery.data ||
      !agentConfigsQuery.isSuccess ||
      !selectionQuery.isSuccess
    )
      return;
    const attached = configQuery.data.rules.flatMap((rule) =>
      rule.persistent_id ? [rule.persistent_id] : [],
    );
    const recorded = selectionQuery.data?.[sandboxId];
    setSelection(
      recorded
        ? {
            agentIds: recorded.agentIds.filter((id) =>
              configs.some((config) => config.id === id && attached.includes(config.connectionId)),
            ),
            credentialIds: attached.filter(
              (id) =>
                !configs.some(
                  (config) => recorded.agentIds.includes(config.id) && config.connectionId === id,
                ),
            ),
          }
        : { agentIds: [], credentialIds: attached },
    );
    const temporary = recorded?.temporaryAgents ?? [];
    setTemporaryAgents(temporary);
    setOneOff(
      configQuery.data.rules
        .filter(
          (rule) =>
            rule.kind === "one-off" && !temporary.some((item) => item.rule.key === rule.key),
        )
        .map((rule) => {
          const saved = recorded?.oneOff?.find((item) => item.key === rule.key);
          return saved?.secret ? { ...rule, secret: saved.secret } : rule;
        }),
    );
    setInitialized(true);
  }, [
    configQuery.data,
    configs,
    initialized,
    sandboxId,
    agentConfigsQuery.isSuccess,
    selectionQuery.isSuccess,
    selectionQuery.data,
  ]);

  const save = async (
    nextOneOff: readonly LocalInjectionRule[] = oneOff,
    nextAgents: readonly TemporaryAgent[] = temporaryAgents,
  ) => {
    if (pending) return;
    if (!initialized || !agentConfigsQuery.isSuccess || !selectionQuery.isSuccess) {
      setError("配置尚未加载完成，请稍后重试。");
      return;
    }
    setPending(true);
    setError(null);
    setNotice(null);
    try {
      await applySandboxCredentials({
        sandboxId,
        selection,
        configs,
        credentials: rulesQuery.data ?? [],
        oneOff: nextOneOff,
        temporaryAgents: nextAgents,
        write,
        onStage: setStage,
      });
      await saveSandboxCredentialSelection(sandboxId, selection, configs, {
        temporaryAgents: nextAgents,
        oneOff: nextOneOff,
      });
      setTemporaryAgents([...nextAgents]);
      setOneOff(nextOneOff);
      setNotice(
        selection.agentIds.length ? "已保存。新启动的 Agent 将使用所选配置。" : "凭证授权已保存。",
      );
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "保存失败，请重试。");
      throw cause;
    } finally {
      setPending(false);
      setStage("");
    }
  };
  const submitOneOff = async (values: InjectionEditorValues) => {
    const value = values.secret.trim();
    const next: LocalInjectionRule = {
      key: editing?.key ?? `one-off:${randomUUID()}`,
      name: values.name,
      kind: "one-off",
      spec: editorValuesToSpec(values),
      ...(value
        ? {
            secret:
              values.type === "http"
                ? { headers: { [values.headerName.trim() || "Authorization"]: value } }
                : values.type === "github"
                  ? { token: value }
                  : { api_key: value },
            secret_hint: `••••${value.slice(-4)}`,
          }
        : editing?.secret_hint
          ? { secret_hint: editing.secret_hint }
          : {}),
    };
    await save(
      editing ? oneOff.map((item) => (item.key === editing.key ? next : item)) : [...oneOff, next],
    );
    setEditing(undefined);
  };
  const supported =
    localSupported &&
    initialized &&
    !rulesQuery.isLoading &&
    !rulesQuery.isError &&
    !configQuery.isError;
  if (localRuntime.loading || (!localRuntime.record && platform.loading))
    return (
      <CloudPage title="安全连接">
        <p className="text-sm text-muted-foreground">正在读取沙箱…</p>
      </CloudPage>
    );
  return (
    <CloudPage title="安全连接">
      {!localSupported ? (
        <p className="text-sm text-muted-foreground">当前仅支持为本地沙箱配置凭证。</p>
      ) : null}
      <section className="grid gap-4">
        <div>
          <h1 className="text-lg font-semibold">使用凭证</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            选择凭证库中的配置，保存后在当前沙箱使用。
          </p>
        </div>
        {rulesQuery.isLoading || (localSupported && !initialized && !configQuery.isError) ? (
          <p role="status" className="text-sm text-muted-foreground">
            正在读取凭证…
          </p>
        ) : null}
        {rulesQuery.isError ||
        configQuery.isError ||
        agentConfigsQuery.isError ||
        selectionQuery.isError ? (
          <div
            role="alert"
            className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-destructive/25 p-3"
          >
            <p className="text-sm text-destructive">
              {(rulesQuery.error ??
                configQuery.error ??
                agentConfigsQuery.error ??
                selectionQuery.error) instanceof Error
                ? (
                    (rulesQuery.error ??
                      configQuery.error ??
                      agentConfigsQuery.error ??
                      selectionQuery.error) as Error
                  ).message
                : "凭证读取失败。"}
            </p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void rulesQuery.refetch();
                void configQuery.refetch();
                void agentConfigsQuery.refetch();
                void selectionQuery.refetch();
              }}
            >
              重新加载
            </Button>
          </div>
        ) : null}
        <CredentialSelectionFields
          configs={configs}
          credentials={rulesQuery.data ?? []}
          value={selection}
          onChange={(value) => {
            setSelection(value);
            setNotice(null);
          }}
          disabled={!supported || pending}
        />
        <div className="grid gap-2">
          {error ? (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          ) : null}
          {notice ? (
            <p role="status" className="text-sm text-muted-foreground">
              {notice}
            </p>
          ) : null}
          {stage ? (
            <span role="status" className="text-sm text-muted-foreground">
              {stage}
            </span>
          ) : null}
        </div>
      </section>
      <section className="grid gap-3 border-t border-border pt-5">
        <div className="flex items-center justify-between">
          <h2 className="text-base font-semibold">临时 Agent 配置</h2>
          <Button
            size="icon"
            variant="ghost"
            aria-label="添加临时 Agent 配置"
            disabled={!supported || pending}
            onClick={() => setAgentDialog({})}
          >
            <PlusIcon className="size-4" />
          </Button>
        </div>
        <p className="text-sm text-muted-foreground">仅用于当前沙箱，不加入凭证库。</p>
        {temporaryAgents.map((item) => (
          <article
            key={item.config.id}
            className="flex items-start justify-between gap-3 rounded-lg border border-border p-3"
          >
            <div className="min-w-0 text-sm">
              <p className="font-medium">{item.config.name}</p>
              <p className="mt-1 text-xs text-muted-foreground">
                {item.config.agent} · {item.config.defaultModel}
              </p>
            </div>
            <div className="flex gap-1">
              <Button
                size="icon"
                variant="ghost"
                aria-label={`编辑临时 Agent ${item.config.name}`}
                disabled={pending}
                onClick={() => setAgentDialog({ initial: item })}
              >
                <SettingsIcon className="size-4" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                aria-label={`删除临时 Agent ${item.config.name}`}
                disabled={pending}
                onClick={() => {
                  if (window.confirm(`撤销当前沙箱的临时 Agent「${item.config.name}」？`))
                    void save(
                      oneOff,
                      temporaryAgents.filter((value) => value.config.id !== item.config.id),
                    ).catch(() => {});
                }}
              >
                <Trash2Icon className="size-4" />
              </Button>
            </div>
          </article>
        ))}
        {!temporaryAgents.length ? (
          <p className="text-sm text-muted-foreground">还没有临时 Agent 配置。</p>
        ) : null}
      </section>
      {agentDialog ? (
        <TemporaryAgentDialog
          {...(agentDialog.initial ? { initial: agentDialog.initial } : {})}
          onClose={() => setAgentDialog(null)}
          onSave={async (item) => {
            await save(oneOff, [
              ...temporaryAgents.filter((value) => value.config.id !== item.config.id),
              item,
            ]);
            setAgentDialog(null);
          }}
        />
      ) : null}
      <section className="grid gap-3 border-t border-border pt-5">
        <div className="flex items-center justify-between">
          <h2 className="text-base font-semibold">临时 HTTP 凭证</h2>
          <Button
            size="icon"
            variant="ghost"
            aria-label="添加临时凭证"
            disabled={!supported || pending}
            onClick={() => {
              setEditing(undefined);
              setDialogOpen(true);
            }}
          >
            <PlusIcon className="size-4" />
          </Button>
        </div>
        {oneOff.map((rule) => (
          <article
            key={rule.key}
            className="flex items-start justify-between gap-3 rounded-lg border border-border p-3"
          >
            <div className="min-w-0 text-sm">
              <p className="font-medium">{rule.name}</p>
              <p className="mt-1 break-all text-xs text-muted-foreground">
                {ruleTarget(rule)} · {ruleSecretPreview(rule)}
              </p>
            </div>
            <div className="flex gap-1">
              <Button
                size="icon"
                variant="ghost"
                aria-label={`编辑 ${rule.name}`}
                disabled={pending}
                onClick={() => {
                  setEditing(rule);
                  setDialogOpen(true);
                }}
              >
                <SettingsIcon className="size-4" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                aria-label={`删除 ${rule.name}`}
                disabled={pending}
                onClick={() => setDeleting(rule)}
              >
                <Trash2Icon className="size-4" />
              </Button>
            </div>
          </article>
        ))}
        {!oneOff.length ? <p className="text-sm text-muted-foreground">还没有临时凭证。</p> : null}
      </section>
      <div className="fixed bottom-6 right-6 z-20 rounded-lg bg-background p-1 shadow-md">
        <Button
          size="icon"
          aria-label="保存沙箱凭证配置"
          title={pending ? "保存中…" : "保存配置"}
          disabled={!supported || pending}
          onClick={() => void save().catch(() => {})}
        >
          {pending ? (
            <LoaderIcon className="size-4 animate-spin" />
          ) : (
            <SaveIcon className="size-4" />
          )}
        </Button>
      </div>
      {dialogOpen ? (
        <InjectionRuleDialog
          open
          onOpenChange={setDialogOpen}
          mode="one-off"
          initial={editing}
          onSubmit={submitOneOff}
        />
      ) : null}
      <Dialog
        open={Boolean(deleting)}
        onOpenChange={(next) => !next && !pending && setDeleting(null)}
      >
        <DialogPopup className="max-w-sm">
          <DialogHeader>
            <DialogTitle>删除临时凭证？</DialogTitle>
            <DialogDescription>从当前沙箱撤销“{deleting?.name}”。</DialogDescription>
          </DialogHeader>
          <DialogPanel>
            {error ? (
              <p role="alert" className="mb-3 text-sm text-destructive">
                {error}
              </p>
            ) : null}
            <div className="flex justify-end gap-2">
              <Button variant="outline" disabled={pending} onClick={() => setDeleting(null)}>
                取消
              </Button>
              <Button
                variant="destructive"
                disabled={pending}
                onClick={() => {
                  void save(oneOff.filter((item) => item.key !== deleting?.key))
                    .then(() => setDeleting(null))
                    .catch(() => {});
                }}
              >
                删除
              </Button>
            </div>
          </DialogPanel>
        </DialogPopup>
      </Dialog>
    </CloudPage>
  );
}
