import { updateLocalSandboxCache } from "./sandboxQueries";
import { Link, useNavigate } from "@tanstack/react-router";
import { CloudIcon, LaptopIcon, LoaderIcon, ShieldCheckIcon } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { EnvironmentId } from "@t3tools/contracts";
import { Button } from "../../components/ui/button";
import { SidebarInset, SidebarTrigger } from "../../components/ui/sidebar";
import { setSandboxEnvironmentConnected } from "./sandboxConnectionCommands";
import { connectPairing } from "../../connection/onboarding";
import { useEnvironments } from "../../state/environments";
import {
  environmentIdForSandbox,
  workspaceTargetForEnvironment,
  rememberSandboxEnvironment,
  setSandboxEnvironmentDisconnected,
} from "./sandboxEnvironmentMap";
import { setActiveEnvironmentId } from "../../state/entities";
import { useAtomCommand } from "../../state/use-atom-command";
import { cloud, useCloudState } from "./cloud";
import {
  platformSandboxView,
  resolvePairingUrl,
  useLocalRuntimeSandbox,
  usePlatformSandbox,
} from "./usePlatformSandbox";

export const CLOUD_WORKSPACE_PANES = ["files", "connections", "snapshots", "security"] as const;
export type CloudWorkspacePane = (typeof CLOUD_WORKSPACE_PANES)[number];

export function isCloudWorkspacePane(value: unknown): value is CloudWorkspacePane {
  return typeof value === "string" && CLOUD_WORKSPACE_PANES.some((pane) => pane === value);
}

type ConnectionState =
  | { readonly kind: "connecting" }
  | { readonly kind: "failed"; readonly message: string };

/**
 * Entering a sandbox connects the app to the runtime that actually serves it.
 * Local sandboxes pair through the desktop runtime, cloud sandboxes through the
 * control plane; both end at the same real workbench. Nothing here renders
 * placeholder tasks, files, or connections.
 */
export function CloudWorkspace({ sandboxId }: { sandboxId: string }) {
  const state = useCloudState();
  const mockSandbox = state.sandboxes.find((sandbox) => sandbox.id === sandboxId);
  // Desktop-owned sandboxes live only in the local runtime, so ask it first.
  const localRuntime = useLocalRuntimeSandbox(mockSandbox ? "" : sandboxId);
  const platform = usePlatformSandbox(mockSandbox || localRuntime.record ? "" : sandboxId);
  const localRecord = localRuntime.record;
  // Memoized: a fresh object every render would retrigger the connect effect.
  const platformRecord = platform.record;
  const sandbox = useMemo(() => {
    if (mockSandbox) return mockSandbox;
    if (localRecord) return platformSandboxView(localRecord);
    return platformRecord ? platformSandboxView(platformRecord) : undefined;
  }, [mockSandbox, localRecord, platformRecord]);
  const isLocalSandbox = Boolean(localRecord) || Boolean(mockSandbox);
  const connectPairingEnv = useAtomCommand(connectPairing, { reportFailure: false });
  const setConnected = useAtomCommand(setSandboxEnvironmentConnected, { reportFailure: false });
  const navigate = useNavigate();
  const { environments, isReady: catalogReady } = useEnvironments();
  const pairedEnvironmentId = environmentIdForSandbox(
    sandboxId,
    environments.map((entry) => entry.environmentId),
  );
  const [connection, setConnection] = useState<ConnectionState>({ kind: "connecting" });
  const [starting, setStarting] = useState(false);
  const autoConnectFor = useRef<string | null>(null);
  const mounted = useRef(false);
  const currentSandboxId = useRef(sandboxId);
  currentSandboxId.current = sandboxId;
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const connect = useCallback(async () => {
    if (!mounted.current || currentSandboxId.current !== sandboxId) return;
    setConnection({ kind: "connecting" });
    try {
      // Local VM state does not guarantee that its T3 HTTP service survived.
      // Asking for a fresh link makes Node repair T3 before reusing a pairing.
      const localPairingUrl = isLocalSandbox
        ? await resolvePairingUrl(sandboxId, { local: true })
        : null;
      if (!mounted.current || currentSandboxId.current !== sandboxId) return;
      if (pairedEnvironmentId) {
        const resumed = await setConnected({ environmentId: pairedEnvironmentId, connected: true });
        if (resumed._tag !== "Success") throw new Error("连接没有恢复，请重试");
        setSandboxEnvironmentDisconnected(pairedEnvironmentId, false);
        if (!mounted.current || currentSandboxId.current !== sandboxId) return;
        setActiveEnvironmentId(pairedEnvironmentId);
        await navigate(workspaceTargetForEnvironment(pairedEnvironmentId));
        return;
      }
      const pairingUrl = localPairingUrl ?? (await resolvePairingUrl(sandboxId, { local: false }));
      if (!mounted.current || currentSandboxId.current !== sandboxId) return;
      const result = await connectPairingEnv({ pairingUrl });
      if (result._tag !== "Success") {
        throw new Error("配对没有完成");
      }
      const environmentId = result.value as EnvironmentId;
      rememberSandboxEnvironment(environmentId, sandboxId);
      setSandboxEnvironmentDisconnected(environmentId, false);
      if (!mounted.current || currentSandboxId.current !== sandboxId) return;
      setActiveEnvironmentId(environmentId);
      // Pairing has registered the environment. Keep the active selection in
      // memory while the index route waits for shell bootstrap and opens a draft.
      await navigate({ to: "/workspace", replace: true });
    } catch (reason) {
      if (!mounted.current || currentSandboxId.current !== sandboxId) return;
      setConnection({
        kind: "failed",
        message: reason instanceof Error ? reason.message : "无法连接到这个沙箱",
      });
    }
  }, [connectPairingEnv, isLocalSandbox, navigate, sandboxId, pairedEnvironmentId, setConnected]);

  useEffect(() => {
    if (localRuntime.loading || (!localRecord && platform.loading)) return;
    if (!catalogReady || sandbox?.status !== "running") return;
    // One automatic attempt per sandbox; retries stay user-initiated so a failure
    // cannot spin forever.
    if (autoConnectFor.current === sandboxId) return;
    autoConnectFor.current = sandboxId;
    void connect();
  }, [
    connect,
    sandbox?.status,
    sandboxId,
    catalogReady,
    pairedEnvironmentId,
    localRuntime.loading,
    localRecord,
    platform.loading,
  ]);

  if (!sandbox) {
    return (
      <SidebarInset className="isodo-cloud-page h-dvh min-h-0 overflow-hidden bg-background text-foreground">
        <div className="grid flex-1 place-items-center p-10 text-sm text-muted-foreground">
          {platform.loading || localRuntime.loading ? "正在读取沙箱…" : "沙箱不存在"}
        </div>
      </SidebarInset>
    );
  }

  return (
    <SidebarInset className="isodo-cloud-page isodo-workspace h-dvh min-h-0 overflow-hidden bg-background text-foreground">
      <header className="workspace-topbar flex shrink-0 items-center gap-3 border-b border-border px-4 sm:px-5">
        <SidebarTrigger className="-ml-2 size-8" aria-label="切换侧边栏" />
        <div className="flex min-w-0 flex-1 items-center gap-2 truncate text-sm">
          {isLocalSandbox ? (
            <LaptopIcon className="size-4 shrink-0 text-muted-foreground" />
          ) : (
            <CloudIcon className="size-4 shrink-0 text-muted-foreground" />
          )}
          <span className="truncate font-medium">{sandbox.name}</span>
          <span className="text-muted-foreground">/</span>
          <span className="shrink-0 text-muted-foreground">工作台</span>
        </div>
        <span className="protected hidden shrink-0 items-center gap-1.5 sm:flex">
          <ShieldCheckIcon className="size-3.5" />
          安全隔离
        </span>
      </header>

      <div className="flex min-h-12 shrink-0 items-center gap-1.5 border-b border-border px-4 py-1.5 sm:px-5">
        <span className="flex items-center gap-2 text-xs text-muted-foreground">
          <span
            className={`size-1.5 rounded-full ${sandbox.status === "running" ? "bg-emerald-600" : "bg-muted-foreground"}`}
          />
          {sandbox.status === "running" ? "运行中" : "已停止"}
        </span>
      </div>

      <div className="grid min-h-0 flex-1 place-items-center px-6 py-10">
        {connection.kind === "failed" ? (
          <div className="max-w-md space-y-4 text-center">
            <h1 className="text-lg font-semibold">无法连接到沙箱</h1>
            <p className="break-words text-sm text-muted-foreground">{connection.message}</p>
            <div className="flex justify-center gap-2">
              <Button onClick={() => void connect()}>重试</Button>
              <Button variant="outline" render={<Link to="/sandboxes" />}>
                返回沙箱列表
              </Button>
            </div>
          </div>
        ) : sandbox.status === "running" ? (
          <div className="flex items-center gap-2 text-sm text-muted-foreground" role="status">
            <LoaderIcon className="size-4 animate-spin" />
            正在连接沙箱…
          </div>
        ) : (
          <div className="max-w-md space-y-4 text-center">
            <h1 className="text-lg font-semibold">沙箱已停止</h1>
            <p className="text-sm text-muted-foreground">启动后即可继续在这个沙箱里工作。</p>
            <div className="flex justify-center gap-2">
              {mockSandbox ? (
                <Button
                  onClick={() => {
                    cloud.setStatus(mockSandbox.id, "running");
                    void connect();
                  }}
                >
                  启动沙箱
                </Button>
              ) : localRecord ? (
                <Button
                  disabled={starting}
                  onClick={() => {
                    const bridge = window.isodoDesktop;
                    if (!bridge) return;
                    setStarting(true);
                    void bridge
                      .startLocalSandbox(localRecord.id)
                      .then(async () => {
                        // Start is asynchronous: wait for the runtime to report
                        // the sandbox running before pairing.
                        const deadline = Date.now() + 120_000;
                        for (;;) {
                          const listed = (await bridge.listLocalSandboxes()) as {
                            items?: { id: string; state?: string }[];
                          };
                          const current = listed.items?.find((item) => item.id === localRecord.id);
                          if (current?.state === "running") {
                            autoConnectFor.current = sandboxId;
                            updateLocalSandboxCache(() => listed.items ?? []);
                            break;
                          }
                          if (current?.state === "failed" || Date.now() > deadline) {
                            throw new Error("沙箱没有启动成功");
                          }
                          await new Promise((resolve) => setTimeout(resolve, 1000));
                        }
                        await connect();
                      })
                      .catch((reason: unknown) => {
                        setConnection({
                          kind: "failed",
                          message:
                            reason instanceof Error ? reason.message : "启动沙箱失败，请稍后重试",
                        });
                      })
                      .finally(() => setStarting(false));
                  }}
                >
                  {starting ? "正在启动…" : "启动沙箱"}
                </Button>
              ) : null}
              <Button variant="outline" render={<Link to="/sandboxes" />}>
                返回沙箱列表
              </Button>
            </div>
          </div>
        )}
      </div>
    </SidebarInset>
  );
}
