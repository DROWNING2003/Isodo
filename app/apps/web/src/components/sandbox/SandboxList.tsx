import type { SandboxInfo } from "@t3tools/shared/sandbox";
import { useAtomValue } from "@effect/atom-react";
import {
  AlertCircleIcon,
  CloudIcon,
  LoaderIcon,
  PlusIcon,
  RefreshCwIcon,
  Settings2Icon,
  Trash2Icon,
} from "lucide-react";
import * as Option from "effect/Option";
import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { BOUNDLY_SANDBOX_METADATA } from "@t3tools/shared/sandbox";

import { environmentCatalog } from "../../connection/catalog";
import { useSandboxCredentials } from "../../hooks/useSandboxCredentials";
import { useSandboxApi } from "../../hooks/useSandboxApi";
import { setActiveEnvironmentId } from "../../state/entities";
import { useAtomCommand } from "../../state/use-atom-command";
import {
  formatSandboxConnectionError,
  sandboxConnectionStageLabel,
  type SandboxConnectionStage,
} from "../../lib/sandboxConnection";
import { Button } from "../ui/button";
import {
  Empty,
  EmptyHeader,
  EmptyTitle,
  EmptyDescription,
  EmptyContent,
  EmptyMedia,
} from "../ui/empty";
import { SandboxCard, type SandboxAction } from "./SandboxCard";
import { sandboxIdFromUrl } from "./sandboxEnvironmentSync";
import { Toggle, ToggleGroup } from "../ui/toggle-group";
import { stackedThreadToast, toastManager } from "../ui/toast";
import {
  AlertDialog,
  AlertDialogClose,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogPopup,
  AlertDialogTitle,
} from "../ui/alert-dialog";

function getHttpBaseUrl(entry: { readonly profile: Option.Option<unknown> }): string | null {
  if (!Option.isSome(entry.profile)) return null;
  const httpBaseUrl = (entry.profile.value as { httpBaseUrl?: string }).httpBaseUrl;
  return httpBaseUrl ?? null;
}

interface Props {
  readonly onNavigateToSettings: () => void;
  readonly onNavigateToCreate: () => void;
}

export function SandboxList({ onNavigateToSettings, onNavigateToCreate }: Props) {
  const navigate = useNavigate();
  const creds = useSandboxCredentials();
  const { api, connect } = useSandboxApi(creds.credentials);
  const [sandboxes, setSandboxes] = useState<readonly SandboxInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<SandboxInfo | null>(null);
  const [pendingAction, setPendingAction] = useState<{
    readonly sandboxID: string;
    readonly action: SandboxAction;
  } | null>(null);
  const [connectionStage, setConnectionStage] = useState<SandboxConnectionStage | null>(null);
  const [scope, setScope] = useState<"boundly" | "all">("boundly");
  const catalog = useAtomValue(environmentCatalog.catalogValueAtom);
  const removeEnv = useAtomCommand(environmentCatalog.remove, "sandbox environment remove");

  const load = useCallback(async () => {
    if (!creds.hasRequired) {
      setLoading(false);
      return;
    }
    setLoading(true);
    setLoadError(false);
    try {
      const list = await api.list(
        scope === "boundly" ? { metadata: BOUNDLY_SANDBOX_METADATA } : undefined,
      );
      if (scope === "all") {
        const apiIds = new Set(list.map((s) => s.sandboxID));
        for (const [envId, entry] of catalog.entries) {
          const httpBaseUrl = getHttpBaseUrl(entry);
          if (!httpBaseUrl) continue;
          const sandboxID = sandboxIdFromUrl(httpBaseUrl);
          if (sandboxID && !apiIds.has(sandboxID)) {
            await removeEnv(envId);
          }
        }
      }
      setSandboxes(list);
    } catch {
      setLoadError(true);
    } finally {
      setLoading(false);
    }
  }, [api, creds.hasRequired, removeEnv, catalog.entries, scope]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleConnect = useCallback(
    async (sandbox: SandboxInfo) => {
      setPendingAction({ sandboxID: sandbox.sandboxID, action: "connect" });
      setConnectionStage(null);
      try {
        const connected = await api.connect(sandbox.sandboxID, 3600);
        const environmentId = await connect(connected, setConnectionStage);
        setActiveEnvironmentId(environmentId);
        void navigate({ to: "/" });
      } catch (error) {
        toastManager.add(
          stackedThreadToast({
            type: "error",
            title: "连接沙箱失败",
            description: formatSandboxConnectionError(error),
          }),
        );
      } finally {
        setPendingAction(null);
        setConnectionStage(null);
      }
    },
    [api, connect],
  );

  const handleRefresh = useCallback(
    async (sandboxID: string) => {
      try {
        await api.refresh(sandboxID, 3600);
        void load();
      } catch {
        /* ignore */
      }
    },
    [api, load],
  );

  const handlePause = useCallback(
    async (sandboxID: string) => {
      setPendingAction({ sandboxID, action: "pause" });
      try {
        await api.pause(sandboxID);
        await load();
      } catch {
        toastManager.add(
          stackedThreadToast({ type: "error", title: "暂停沙箱失败", description: "请重试。" }),
        );
      } finally {
        setPendingAction(null);
      }
    },
    [api, load],
  );

  const handleResume = useCallback(
    async (sandboxID: string) => {
      setPendingAction({ sandboxID, action: "resume" });
      try {
        await api.resume(sandboxID, 3600);
        await load();
      } catch {
        toastManager.add(
          stackedThreadToast({ type: "error", title: "恢复沙箱失败", description: "请重试。" }),
        );
      } finally {
        setPendingAction(null);
      }
    },
    [api, load],
  );

  const handleDelete = useCallback(
    async (sandboxID: string) => {
      setPendingAction({ sandboxID, action: "delete" });
      try {
        await api.delete(sandboxID);
        for (const [envId, entry] of catalog.entries) {
          const httpBaseUrl = getHttpBaseUrl(entry);
          if (httpBaseUrl && sandboxIdFromUrl(httpBaseUrl) === sandboxID) {
            await removeEnv(envId);
            break;
          }
        }
        setSandboxes((prev) => prev.filter((s) => s.sandboxID !== sandboxID));
      } catch {
        toastManager.add(
          stackedThreadToast({ type: "error", title: "删除沙箱失败", description: "请重试。" }),
        );
      } finally {
        setPendingAction(null);
      }
    },
    [api, removeEnv, catalog.entries],
  );

  if (!creds.isLoaded || (loading && sandboxes.length === 0)) {
    return (
      <Empty className="flex-1 gap-3">
        <LoaderIcon className="size-5 animate-spin text-muted-foreground" />
        <span className="text-sm text-muted-foreground">正在读取沙箱...</span>
      </Empty>
    );
  }

  if (!creds.hasRequired) {
    return (
      <Empty className="flex-1">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <Settings2Icon className="size-4.5" />
          </EmptyMedia>
          <EmptyTitle>凭证未配置</EmptyTitle>
          <EmptyDescription>需要配置沙箱 API 密钥才能使用沙箱</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button
            size="sm"
            variant="outline"
            onClick={onNavigateToSettings}
            className="border-primary/20 bg-primary/8 text-primary shadow-none hover:border-primary/30 hover:bg-primary/14 [transition:transform_160ms_cubic-bezier(0.23,1,0.32,1)] active:scale-[0.97]"
          >
            配置凭证
          </Button>
        </EmptyContent>
      </Empty>
    );
  }

  if (sandboxes.length === 0) {
    return (
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        <div className="mx-auto flex w-full max-w-5xl flex-1 flex-col gap-6 px-5 py-6 sm:px-8 sm:py-8">
          <SandboxListToolbar
            count={0}
            loading={loading}
            scope={scope}
            onRefresh={() => void load()}
            onScopeChange={setScope}
          />
          {loadError ? (
            <SandboxLoadError onRetry={() => void load()} />
          ) : (
            <Empty className="min-h-[18rem] flex-1 rounded-xl border border-dashed border-border/80 bg-card/25 py-12">
              <EmptyHeader>
                <EmptyMedia variant="icon" className="text-muted-foreground">
                  <CloudIcon className="size-5" />
                </EmptyMedia>
                <EmptyTitle className="text-lg">还没有沙箱</EmptyTitle>
                <EmptyDescription className="max-w-xs">创建一个沙箱开始开发。</EmptyDescription>
              </EmptyHeader>
              <EmptyContent>
                <Button
                  size="sm"
                  variant="outline"
                  className="border-primary/20 bg-primary/8 text-primary shadow-none hover:border-primary/30 hover:bg-primary/14 [transition:transform_160ms_cubic-bezier(0.23,1,0.32,1)] active:scale-[0.97]"
                  onClick={onNavigateToCreate}
                >
                  <PlusIcon className="size-4" />
                  创建沙箱
                </Button>
              </EmptyContent>
            </Empty>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
      <div className="mx-auto flex w-full max-w-5xl flex-col gap-6 px-5 py-6 sm:px-8 sm:py-8">
        <SandboxListToolbar
          count={sandboxes.length}
          loading={loading}
          scope={scope}
          onRefresh={() => void load()}
          onScopeChange={setScope}
        />
        {loadError ? <SandboxLoadError onRetry={() => void load()} /> : null}
        <div className="grid gap-3" aria-busy={loading}>
          {sandboxes.map((sandbox) => (
            <SandboxCard
              key={sandbox.sandboxID}
              sandbox={sandbox}
              pendingAction={
                pendingAction?.sandboxID === sandbox.sandboxID ? pendingAction.action : null
              }
              {...(pendingAction?.sandboxID === sandbox.sandboxID &&
              pendingAction.action === "connect" &&
              connectionStage
                ? { pendingLabel: sandboxConnectionStageLabel(connectionStage) }
                : {})}
              onConnect={() => void handleConnect(sandbox)}
              onPause={() => void handlePause(sandbox.sandboxID)}
              onResume={() => void handleResume(sandbox.sandboxID)}
              onRefresh={() => void handleRefresh(sandbox.sandboxID)}
              onDelete={() => setDeleteTarget(sandbox)}
            />
          ))}
        </div>
      </div>
      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open && pendingAction?.action !== "delete") setDeleteTarget(null);
        }}
      >
        <AlertDialogPopup>
          <AlertDialogHeader>
            <AlertDialogTitle>删除这个沙箱？</AlertDialogTitle>
            <AlertDialogDescription>
              {deleteTarget
                ? `${deleteTarget.alias || deleteTarget.sandboxID.slice(0, 8)} 将被永久删除，里面的文件和运行状态无法恢复。`
                : "这个操作无法恢复。"}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogClose
              render={<Button variant="ghost" disabled={pendingAction?.action === "delete"} />}
            >
              取消
            </AlertDialogClose>
            <Button
              variant="destructive"
              disabled={deleteTarget === null || pendingAction?.action === "delete"}
              onClick={() => {
                if (!deleteTarget) return;
                const target = deleteTarget;
                setDeleteTarget(null);
                void handleDelete(target.sandboxID);
              }}
            >
              {pendingAction?.action === "delete" ? (
                <LoaderIcon className="size-4 animate-spin" />
              ) : (
                <Trash2Icon className="size-4" />
              )}
              {pendingAction?.action === "delete" ? "删除中" : "删除沙箱"}
            </Button>
          </AlertDialogFooter>
        </AlertDialogPopup>
      </AlertDialog>
    </div>
  );
}

function SandboxListToolbar({
  count,
  loading,
  scope,
  onRefresh,
  onScopeChange,
}: {
  readonly count: number;
  readonly loading: boolean;
  readonly scope: "boundly" | "all";
  readonly onRefresh: () => void;
  readonly onScopeChange: (scope: "boundly" | "all") => void;
}) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-4">
      <div>
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <h1 className="text-xl font-semibold tracking-[-0.03em] text-foreground sm:text-2xl">
            沙箱
          </h1>
          <span className="text-xs text-muted-foreground">{count} 个环境</span>
        </div>
      </div>
      <div className="flex items-center gap-2">
        <SandboxScopeToggle scope={scope} onScopeChange={onScopeChange} />
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="刷新沙箱列表"
          title="刷新沙箱列表"
          disabled={loading}
          onClick={onRefresh}
          className="text-muted-foreground hover:bg-muted/60 hover:text-foreground [transition:transform_160ms_cubic-bezier(0.23,1,0.32,1)] active:scale-[0.97]"
        >
          <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} />
        </Button>
      </div>
    </div>
  );
}

function SandboxLoadError({ onRetry }: { readonly onRetry: () => void }) {
  return (
    <div className="flex items-center gap-3 rounded-lg border-l-2 border-destructive/50 bg-destructive/5 px-3 py-2.5 text-sm text-destructive-foreground">
      <AlertCircleIcon className="size-4 shrink-0" />
      <p className="min-w-0 flex-1">暂时无法读取沙箱列表，请检查凭证或网络连接。</p>
      <Button size="xs" variant="destructive-outline" onClick={onRetry}>
        重试
      </Button>
    </div>
  );
}

function SandboxScopeToggle({
  scope,
  onScopeChange,
}: {
  readonly scope: "boundly" | "all";
  readonly onScopeChange: (scope: "boundly" | "all") => void;
}) {
  return (
    <ToggleGroup
      aria-label="沙箱范围"
      size="xs"
      variant="outline"
      value={[scope]}
      onValueChange={(value) => {
        const next = value[0];
        if (next === "boundly" || next === "all") onScopeChange(next);
      }}
    >
      <Toggle value="boundly">本应用</Toggle>
      <Toggle value="all">全部</Toggle>
    </ToggleGroup>
  );
}
