import { sandboxClientFor } from "./sandboxTransports";
import { beginWorkspaceEntry } from "./workspaceEntryTrace";
import { waitForSandboxAction } from "./sandboxClient";
import { toLocalSandboxItem } from "./sandboxQueries";
import { useServerAgentConfigs, useAgentConfigMutations } from "./credentialDocuments";
import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useState, type ReactNode } from "react";
import {
  BoxIcon,
  CloudIcon,
  Code2Icon,
  KeyRoundIcon,
  LaptopIcon,
  GithubIcon,
  GlobeIcon,
  LayersIcon,
  PlayIcon,
  PlusIcon,
  SearchIcon,
  SettingsIcon,
  ShieldCheckIcon,
  SparklesIcon,
  SquareIcon,
  Trash2Icon,
} from "lucide-react";
import { type InjectionRule, type Sandbox as PlatformSandbox } from "../../api/public.generated";
import { Button } from "../../components/ui/button";
import { Card, CardHeader, CardTitle, CardPanel, CardFooter } from "../../components/ui/card";
import { toastManager } from "../../components/ui/toast";
import { Input } from "../../components/ui/input";
import {
  Dialog,
  DialogDescription,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "../../components/ui/dialog";
import { ScrollArea } from "../../components/ui/scroll-area";
import { SidebarInset, SidebarTrigger } from "../../components/ui/sidebar";
import icon from "../../assets/isodo/icon.png";
import { cloud, useCloudState } from "./cloud";
import { refreshAuth, signIn as startSignIn, useAuth } from "./auth";
import {
  type LocalSandboxItem,
  sandboxQueryClient,
  useLocalSandboxes,
  usePlatformSandboxes,
  updateLocalSandboxCache,
} from "./sandboxQueries";
import { useSandboxConnections } from "./useSandboxConnections";
import {
  localRuleFromPersistent,
  ruleTypeLabel,
  useInjectionRuleMutations,
  useInjectionRules,
  useInjectionSummary,
} from "./injectionRules";
import { InjectionRuleDialog, type InjectionEditorValues } from "./InjectionRuleDialog";
import { AgentConfigDialog } from "./AgentConfigDialog";
import { AGENT_LABELS, type AgentConfig } from "./agentConfigs";
import "./isodo.css";

export function CloudPage({
  title,
  children,
  action,
}: {
  title: string;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <SidebarInset className="isodo-cloud-page h-dvh min-h-0 overflow-hidden bg-background text-foreground">
      <header className="workspace-topbar flex shrink-0 items-center gap-3 border-b border-border px-4 sm:px-6">
        <SidebarTrigger className="-ml-2 size-8" aria-label="切换侧边栏" />
        <span className="text-sm font-medium">{title}</span>
        {action ? <div className="ml-auto">{action}</div> : null}
      </header>
      <ScrollArea className="min-h-0 flex-1">
        <div className="isodo-page-main w-full space-y-6">{children}</div>
      </ScrollArea>
    </SidebarInset>
  );
}

export function CloudOverview() {
  const connections = useSandboxConnections();
  const state = useCloudState();
  const auth = useAuth();
  const localQuery = useLocalSandboxes();
  const platformQuery = usePlatformSandboxes();
  const localSandboxes = localQuery.data ?? null;
  const platformSandboxes = platformQuery.data ?? null;
  const setLocalSandboxes = updateLocalSandboxCache;
  const setPlatformSandboxes = (
    update: (items: PlatformSandbox[] | null) => PlatformSandbox[] | null,
  ) => {
    void sandboxQueryClient.cancelQueries({ queryKey: platformQuery.queryKey });
    sandboxQueryClient.setQueryData<PlatformSandbox[]>(
      platformQuery.queryKey,
      (items) => update(items ?? null) ?? undefined,
    );
  };
  const [platformError, setPlatformError] = useState<string | null>(null);
  const [scope, setScope] = useState<"all" | "local" | "cloud">("all");
  const [deleting, setDeleting] = useState<string | null>(null);
  const [lifecyclePending, setLifecyclePending] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const navigate = useNavigate();
  const showPlatformSandboxes = auth.status === "authenticated";
  const localCards = (localSandboxes ?? []).map((sandbox) => ({
    id: sandbox.id,
    name: sandbox.name || sandbox.id.slice(0, 8),
    template: "blank" as const,
    description: "本地沙箱",
    agent: "本地运行时",
    status:
      sandbox.state === "running"
        ? ("running" as const)
        : ["creating", "starting"].includes(sandbox.state ?? "")
          ? ("starting" as const)
          : sandbox.state === "failed"
            ? ("failed" as const)
            : ("stopped" as const),
    runtime: "local-runtime" as const,
    observedState: null as string | null,
    resourceSummary: "",
    taskCount: 0,
    snapshotCount: 0,
  }));
  const cards = showPlatformSandboxes
    ? (platformSandboxes ?? []).map((sandbox) => ({
        id: sandbox.id,
        name: sandbox.name,
        template: "blank" as const,
        description: sandbox.template_id ? "模板沙箱" : "Agent 工作台",
        agent: "Agent 工作台",
        status:
          sandbox.observed_state === "running"
            ? ("running" as const)
            : ["requested", "preparing", "starting"].includes(sandbox.observed_state)
              ? ("starting" as const)
              : ["failed", "error"].includes(sandbox.observed_state)
                ? ("failed" as const)
                : ("stopped" as const),
        runtime: "platform" as const,
        observedState: sandbox.observed_state,
        resourceSummary: "",
        taskCount: 0,
        snapshotCount: 0,
      }))
    : state.sandboxes.map((sandbox) => ({
        id: sandbox.id,
        name: sandbox.name,
        template: sandbox.template,
        description:
          sandbox.template === "dev"
            ? "个人网站与日常开发"
            : sandbox.template === "daily"
              ? "整理资料，留一点时间给生活"
              : "随时开始一个新想法",
        agent:
          sandbox.template === "dev"
            ? "Claude Code"
            : sandbox.template === "daily"
              ? "OpenCode"
              : "Codex",
        status: sandbox.status,
        runtime: "mock" as const,
        observedState: null as string | null,
        resourceSummary:
          sandbox.cpu + " CPU · " + sandbox.memoryGB + " GB RAM · " + sandbox.diskGB + " GB",
        taskCount: state.tasks.filter((task) => task.sandboxId === sandbox.id).length,
        snapshotCount: state.snapshots.filter((snapshot) => snapshot.sourceSandboxId === sandbox.id)
          .length,
      }));
  const allCards = [...localCards, ...cards];
  const filteredSandboxes = allCards.filter((sandbox) => {
    const isLocal = sandbox.runtime === "local-runtime";
    const matchesScope = scope === "all" || (scope === "local" ? isLocal : !isLocal);
    return (
      matchesScope && sandbox.name.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())
    );
  });

  return (
    <CloudPage title="我的沙箱">
      {connections.error && (
        <p role="alert" className="text-sm text-destructive">
          {connections.error}
        </p>
      )}
      {(platformError || platformQuery.isError || localQuery.isError) && (
        <p role="alert" className="text-sm text-destructive">
          {platformError ??
            (allCards.length
              ? "部分沙箱列表暂时无法更新，保留上次结果"
              : "沙箱列表暂时无法加载，请稍后重试")}
        </p>
      )}
      <div className="isodo-overview-toolbar">
        <div className="isodo-overview-tabs" role="tablist" aria-label="沙箱位置">
          {(
            [
              ["all", "全部"],
              ["local", "本地"],
              ["cloud", "云端"],
            ] as const
          ).map(([value, label]) => (
            <button
              key={value}
              type="button"
              role="tab"
              aria-selected={scope === value}
              className={scope === value ? "isodo-overview-tab is-active" : "isodo-overview-tab"}
              onClick={() => setScope(value)}
            >
              {label}
            </button>
          ))}
        </div>
        <label className="isodo-overview-search">
          <SearchIcon className="size-4 shrink-0" />
          <Input
            type="search"
            className="h-8 !border-0 !bg-transparent !shadow-none !ring-0 focus-visible:!ring-0"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索沙箱…"
            aria-label="搜索沙箱"
          />
        </label>
      </div>
      {allCards.length === 0 && (localQuery.isFetching || platformQuery.isFetching) ? (
        <p className="isodo-overview-empty">正在加载沙箱…</p>
      ) : filteredSandboxes.length ? (
        <div className="isodo-overview-grid">
          {filteredSandboxes.map((sandbox) => {
            const isPlatformSandbox = sandbox.runtime === "platform";
            const isLocalSandbox = sandbox.runtime === "local-runtime";
            const pairedEnvironment = connections.environmentForSandbox(sandbox.id);
            const template = sandbox.template;
            const Icon =
              template === "dev" ? Code2Icon : template === "daily" ? SparklesIcon : BoxIcon;
            // Opening a sandbox always lands on its workspace; the workspace
            // owns the T3 session so the list never navigates away from the app.
            const openSandbox = () => {
              beginWorkspaceEntry(sandbox.id);
              if (!isPlatformSandbox && sandbox.status === "paused") {
                cloud.setStatus(sandbox.id, "running");
              }
              void navigate({ to: "/sandboxes/$sandboxId", params: { sandboxId: sandbox.id } });
            };
            const requestLifecycle = (action: "start" | "stop") => {
              setLifecyclePending(sandbox.id);
              const client = sandboxClientFor(isLocalSandbox ? "local" : "cloud");
              const request =
                action === "start" ? client.start(sandbox.id) : client.stop(sandbox.id);
              void request
                .then((accepted) => waitForSandboxAction(client, accepted, { timeoutMs: 120_000 }))
                .then(async (updated) => {
                  if (action === "stop") await connections.disconnect(sandbox.id);
                  if (isLocalSandbox) {
                    setLocalSandboxes((items) => [
                      ...(items ?? []).filter((item) => item.id !== updated.id),
                      toLocalSandboxItem(updated),
                    ]);
                  } else {
                    setPlatformSandboxes(
                      (items) =>
                        items?.map((item) => (item.id === updated.id ? updated : item)) ?? items,
                    );
                  }
                  setPlatformError(null);
                })
                .catch((reason: unknown) =>
                  setPlatformError(
                    reason instanceof Error ? reason.message : "沙箱状态变更失败，请稍后重试",
                  ),
                )
                .finally(() => setLifecyclePending(null));
            };

            return (
              <Card
                key={sandbox.id}
                className="isodo-overview-card gap-0 overflow-hidden shadow-none"
              >
                <CardHeader
                  className="isodo-overview-card-header"
                  role="link"
                  tabIndex={0}
                  aria-label={`${pairedEnvironment ? "进入" : "连接"}沙箱 ${sandbox.name}`}
                  onClick={openSandbox}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") {
                      event.preventDefault();
                      openSandbox();
                    }
                  }}
                >
                  <span className="isodo-overview-icon">
                    <Icon className="size-5" />
                  </span>
                  <div className="min-w-0">
                    <div className="isodo-overview-card-title-line">
                      <CardTitle className="truncate text-base">{sandbox.name}</CardTitle>
                      <span className="isodo-location-badge">
                        {sandbox.runtime === "local-runtime" ? (
                          <>
                            <LaptopIcon className="size-3.5" /> 本地
                          </>
                        ) : (
                          <>
                            <CloudIcon className="size-3.5" /> 云端
                          </>
                        )}
                      </span>
                    </div>
                    <p className="isodo-overview-card-description">{sandbox.description}</p>
                  </div>
                </CardHeader>
                <CardPanel className="isodo-overview-card-details" onClick={openSandbox}>
                  <span className="isodo-overview-status">
                    <span
                      className={
                        sandbox.status === "running"
                          ? "isodo-status-dot is-running"
                          : "isodo-status-dot"
                      }
                    />
                    {sandbox.status === "running"
                      ? "运行中"
                      : sandbox.status === "starting"
                        ? "创建中"
                        : sandbox.status === "failed"
                          ? "启动失败"
                          : sandbox.status === "stopped"
                            ? "已停止"
                            : "已休眠"}
                  </span>
                  {isPlatformSandbox ? (
                    <p>{sandbox.description}</p>
                  ) : (
                    <>
                      <p>{sandbox.resourceSummary}</p>
                      <div className="isodo-overview-card-meta">
                        <span>
                          <LaptopIcon className="size-4" /> {sandbox.agent}
                        </span>
                        <span>
                          {sandbox.taskCount} 个任务 · {sandbox.snapshotCount} 个快照
                        </span>
                      </div>
                    </>
                  )}
                </CardPanel>
                <CardFooter className="isodo-overview-card-footer">
                  {(isPlatformSandbox || isLocalSandbox) &&
                  (sandbox.status === "stopped" || sandbox.status === "failed") ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={`启动沙箱 ${sandbox.name}`}
                      disabled={lifecyclePending === sandbox.id}
                      onClick={() => requestLifecycle("start")}
                    >
                      <PlayIcon className="size-4" />
                    </Button>
                  ) : null}
                  {(isPlatformSandbox || isLocalSandbox) && sandbox.status === "running" ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={`停止沙箱 ${sandbox.name}`}
                      disabled={lifecyclePending === sandbox.id}
                      onClick={() => requestLifecycle("stop")}
                    >
                      <SquareIcon className="size-4" />
                    </Button>
                  ) : null}
                  {sandbox.runtime !== "mock" ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={`删除沙箱 ${sandbox.name}`}
                      disabled={deleting === sandbox.id}
                      onClick={() => {
                        if (
                          !window.confirm(
                            `删除沙箱「${sandbox.name}」？这会停止并移除它的运行环境。`,
                          )
                        ) {
                          return;
                        }
                        setDeleting(sandbox.id);
                        const localSandbox =
                          sandbox.runtime === "local-runtime"
                            ? (localSandboxes?.find((item) => item.id === sandbox.id) ?? null)
                            : null;
                        const localIndex = localSandbox
                          ? (localSandboxes?.findIndex((item) => item.id === sandbox.id) ?? -1)
                          : -1;
                        const platformSandbox =
                          sandbox.runtime === "platform"
                            ? (platformSandboxes?.find((item) => item.id === sandbox.id) ?? null)
                            : null;
                        const platformIndex = platformSandbox
                          ? (platformSandboxes?.findIndex((item) => item.id === sandbox.id) ?? -1)
                          : -1;
                        const client = sandboxClientFor(
                          sandbox.runtime === "local-runtime" ? "local" : "cloud",
                        );
                        if (sandbox.runtime === "local-runtime") {
                          setLocalSandboxes(
                            (current) =>
                              current?.filter((item) => item.id !== sandbox.id) ?? current,
                          );
                        } else {
                          setPlatformSandboxes(
                            (current) =>
                              current?.filter((item) => item.id !== sandbox.id) ?? current,
                          );
                        }
                        void (async () => {
                          try {
                            const accepted = await client.delete(sandbox.id);
                            if (accepted) {
                              await waitForSandboxAction(client, accepted, { timeoutMs: 120_000 });
                            }
                            await connections.removeForSandbox(sandbox.id);
                            if (sandbox.runtime !== "local-runtime") setPlatformError(null);
                          } catch (cause) {
                            if (localSandbox) {
                              setLocalSandboxes((current) => {
                                const items = current ?? [];
                                if (items.some((item) => item.id === sandbox.id)) return items;
                                const restored = [...items];
                                restored.splice(
                                  localIndex < 0
                                    ? restored.length
                                    : Math.min(localIndex, restored.length),
                                  0,
                                  localSandbox,
                                );
                                return restored;
                              });
                            }
                            if (platformSandbox) {
                              setPlatformSandboxes((current) => {
                                const items = current ?? [];
                                if (items.some((item) => item.id === sandbox.id)) return items;
                                const restored = [...items];
                                restored.splice(
                                  platformIndex < 0
                                    ? restored.length
                                    : Math.min(platformIndex, restored.length),
                                  0,
                                  platformSandbox,
                                );
                                return restored;
                              });
                            }
                            toastManager.add({
                              type: "error",
                              title: "删除沙箱失败",
                              description: cause instanceof Error ? cause.message : "请稍后重试。",
                            });
                          } finally {
                            setDeleting(null);
                          }
                        })();
                      }}
                    >
                      <Trash2Icon className="size-4" />
                    </Button>
                  ) : null}
                </CardFooter>
              </Card>
            );
          })}
        </div>
      ) : (
        <p className="isodo-overview-empty">
          {scope === "local" && !query ? "暂无本地沙箱" : "没有符合条件的沙箱"}
        </p>
      )}
    </CloudPage>
  );
}

export function CloudLogin() {
  const [english, setEnglish] = useState(false);
  const navigate = useNavigate();

  const copy = (chinese: string, englishText: string) => (english ? englishText : chinese);
  const auth = useAuth();
  const loginError = new URLSearchParams(window.location.search).has("login_error");
  useEffect(() => {
    void refreshAuth();
  }, []);
  useEffect(() => {
    if (auth.status === "authenticated") void navigate({ to: "/sandboxes", replace: true });
  }, [auth.status, navigate]);

  return (
    <main className="isodo-login-page">
      <header className="isodo-login-header">
        <Link to="/sandboxes" className="isodo-login-logo">
          <img src={icon} alt="" />
          <strong>isodo</strong>
        </Link>
        <Button variant="ghost" size="sm" onClick={() => setEnglish((value) => !value)}>
          <GlobeIcon className="size-4" />
          {english ? "中文" : "English"}
        </Button>
      </header>
      <section className="isodo-login-layout">
        <div className="isodo-login-copy">
          <span className="isodo-login-eyebrow">YOUR AGENTS. YOUR SPACE.</span>
          <h1>{copy("让想法开始行动。", "Make room for what’s next.")}</h1>
          <p>
            {copy(
              "给你的 AI 助手一个安全的工作空间。\n在本地开始，在云端继续。",
              "A secure workspace for your AI agents.\nStart locally. Continue in the cloud.",
            )}
          </p>
          <div className="isodo-login-illustration" aria-hidden="true">
            <div className="isodo-login-space">
              <LaptopIcon />
              <span>{copy("本地沙箱", "Local sandbox")}</span>
              <i />
            </div>
            <div className="isodo-login-transfer">
              — <LayersIcon /> —
            </div>
            <div className="isodo-login-space">
              <CloudIcon />
              <span>{copy("云端沙箱", "Cloud sandbox")}</span>
              <i />
            </div>
          </div>
          <div className="isodo-login-features">
            <span>
              <ShieldCheckIcon />
              {copy("隔离运行", "Isolated by design")}
            </span>
            <span>
              <KeyRoundIcon />
              {copy("凭证安全连接", "Protected credentials")}
            </span>
            <span>
              <LayersIcon />
              {copy("快照随行", "Portable snapshots")}
            </span>
          </div>
        </div>
        <Card className="isodo-login-card shadow-none">
          <div className="isodo-login-mark">
            <img src={icon} alt="" />
          </div>
          <CardHeader className="p-0">
            <CardTitle className="text-xl">{copy("欢迎来到 isodo", "Welcome to isodo")}</CardTitle>
            <p className="mt-2 text-sm text-muted-foreground">
              {copy("登录，进入你的沙箱工作空间。", "Sign in to your sandbox workspace.")}
            </p>
          </CardHeader>
          <CardPanel className="space-y-2 p-0 pt-6">
            {auth.status === "error" ? (
              <>
                <p role="alert" className="text-sm text-destructive">
                  {copy("暂时无法连接登录服务。", "Unable to connect to the sign-in service.")}
                </p>
                <Button className="w-full" variant="outline" onClick={() => void refreshAuth()}>
                  {copy("重新连接", "Try again")}
                </Button>
              </>
            ) : (
              <Button
                className="isodo-login-github-button w-full"
                disabled={auth.status === "loading"}
                onClick={startSignIn}
              >
                <GithubIcon className="size-4" />
                {auth.status === "loading"
                  ? copy("正在确认登录状态…", "Checking session…")
                  : copy("使用 GitHub 继续", "Continue with GitHub")}
              </Button>
            )}
            <Button className="w-full" variant="outline" disabled>
              <GlobeIcon className="size-4" />
              {copy("Google 登录暂未开放", "Google sign-in is unavailable")}
            </Button>
            {loginError && (
              <p role="alert" className="text-sm text-destructive">
                {copy("登录未完成，请重新尝试。", "Sign-in was not completed. Please try again.")}
              </p>
            )}
            <div className="isodo-login-demo-note">
              <PlayIcon />
              {copy(
                "目前仅支持 GitHub 登录，认证由 Isodo 身份服务处理。",
                "GitHub is currently the only sign-in option. Authentication is handled by Isodo.",
              )}
            </div>
            <small className="isodo-login-plan-note">
              {copy(
                "从 Free 开始。模型使用你自己的连接。",
                "Start on Free. Bring your own model connection.",
              )}
            </small>
          </CardPanel>
        </Card>
      </section>
      <footer className="isodo-login-footer">
        <span>isodo · {copy("你的智能体，自在工作。", "A place for your agents to work.")}</span>
        <span>{copy("本地优先，安全相伴", "Local first. Secure together.")}</span>
      </footer>
    </main>
  );
}

function CredentialEmptyState({ kind }: { kind: "agent" | "http" }) {
  const Icon = kind === "agent" ? SparklesIcon : KeyRoundIcon;
  return (
    <Card className="shadow-none md:col-span-full">
      <CardPanel className="py-10 text-center text-sm text-muted-foreground">
        <Icon className="mx-auto mb-3 size-6" />
        <p>{kind === "agent" ? "还没有 Agent 配置" : "还没有 HTTP 凭证"}</p>
      </CardPanel>
    </Card>
  );
}

export function CloudCredentials() {
  const rules = useInjectionRules();
  const summary = useInjectionSummary();
  const mutations = useInjectionRuleMutations();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [credentialType, setCredentialType] = useState<"http" | "mcp">("http");
  const [editing, setEditing] = useState<InjectionRule | null>(null);
  const [deleting, setDeleting] = useState<InjectionRule | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [agentDialogOpen, setAgentDialogOpen] = useState(false);
  const [deletingAgent, setDeletingAgent] = useState<AgentConfig | null>(null);
  const [agentDeleteError, setAgentDeleteError] = useState<string | null>(null);
  const [initialConnectionId, setInitialConnectionId] = useState<string | undefined>();
  const [editingAgent, setEditingAgent] = useState<AgentConfig | undefined>();
  const configsQuery = useServerAgentConfigs();
  const agentConfigs = configsQuery.data ?? [];
  const configMutations = useAgentConfigMutations();
  const saveAgentConfig = async (config: AgentConfig) => {
    await configMutations.save(config);
    setEditingAgent(undefined);
  };
  const removeAgentConfig = async () => {
    if (!deletingAgent) return;
    setAgentDeleteError(null);
    try {
      await configMutations.remove(deletingAgent.id);
      setDeletingAgent(null);
    } catch {
      setAgentDeleteError("删除失败，请重试。");
    }
  };

  const standaloneCredentials = configsQuery.isSuccess
    ? rules.data?.filter(
        (rule) =>
          rule.type !== "mcp" && !agentConfigs.some((config) => config.connectionId === rule.id),
      )
    : undefined;

  const openCreate = () => {
    setCredentialType("http");
    setEditing(null);
    setDialogOpen(true);
  };
  const openEdit = (rule: InjectionRule) => {
    setCredentialType(rule.type === "mcp" ? "mcp" : "http");
    setEditing(rule);
    setDialogOpen(true);
  };

  const submit = async (values: InjectionEditorValues) => {
    const base = {
      name: values.name,
      type: values.type,
      ...(values.baseUrl.trim() ? { base_url: values.baseUrl.trim() } : {}),
      ...((values.type === "http" || values.type === "mcp") && values.headerName.trim()
        ? { header_name: values.headerName.trim() }
        : {}),
      ...(Object.keys(values.ifHeaders).length > 0 ? { if_headers: values.ifHeaders } : {}),
      ...(Object.keys(values.ifQueries).length > 0 ? { if_queries: values.ifQueries } : {}),
    };
    if (editing) {
      const { type: _type, ...patch } = base;
      await mutations.update(editing.id, {
        ...patch,
        base_url: values.baseUrl.trim(),
        if_headers: values.ifHeaders,
        if_queries: values.ifQueries,
        ...(values.secret.trim() ? { secret: values.secret.trim() } : {}),
      });
    } else {
      await mutations.create({ ...base, secret: values.secret.trim() });
    }
  };

  const remove = async () => {
    if (!deleting) return;
    setDeleteError(null);
    try {
      await mutations.remove(deleting.id);
      setDeleting(null);
    } catch (cause) {
      setDeleteError(cause instanceof Error ? cause.message : "删除失败，请重试。");
    }
  };

  return (
    <CloudPage title="凭证库">
      <section aria-labelledby="agent-configs-heading" className="grid gap-4">
        <div className="flex items-center justify-between gap-3">
          <h2 id="agent-configs-heading" className="text-base font-semibold">
            Agent 配置
          </h2>
          <Button
            size="icon"
            variant="ghost"
            aria-label="添加 Agent 配置"
            onClick={() => {
              setEditingAgent(undefined);
              setInitialConnectionId(undefined);
              setAgentDialogOpen(true);
            }}
          >
            <PlusIcon className="size-4" />
          </Button>
        </div>

        <div className="grid gap-4 md:grid-cols-2">
          {agentConfigs.map((config) => {
            const connection = rules.data?.find((rule) => rule.id === config.connectionId);
            return (
              <Card key={config.id} className="shadow-none">
                <CardHeader className="flex flex-row items-start justify-between gap-3">
                  <div>
                    <CardTitle>{config.name}</CardTitle>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {AGENT_LABELS[config.agent]}
                    </p>
                  </div>
                  <div className="flex gap-1">
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label={`编辑 ${config.name}`}
                      onClick={() => {
                        setEditingAgent(config);
                        setAgentDialogOpen(true);
                      }}
                    >
                      <SettingsIcon className="size-4" />
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label={`删除 ${config.name}`}
                      onClick={() => {
                        setAgentDeleteError(null);
                        setDeletingAgent(config);
                      }}
                    >
                      <Trash2Icon className="size-4" />
                    </Button>
                  </div>
                </CardHeader>
                <CardPanel className="text-sm text-muted-foreground">
                  <p className="break-all">
                    {connection?.base_url ||
                      (connection ? "默认 API 地址" : "凭证已删除，请重新设置")}
                  </p>
                  <p className="mt-2">{config.defaultModel || "未指定默认模型"}</p>
                  <p className="mt-1 text-xs">
                    {config.modelNames.length} 个模型 ·{" "}
                    {connection
                      ? `已授权 ${summary.data?.[connection.id] ?? 0} 个沙箱`
                      : "需要重新配置凭证"}
                  </p>
                </CardPanel>
              </Card>
            );
          })}
          {configsQuery.isPending ? (
            <p role="status">正在加载 Agent 配置…</p>
          ) : configsQuery.isError ? (
            <p role="alert" className="text-sm text-destructive">
              Agent 配置加载失败，请重试。
            </p>
          ) : agentConfigs.length === 0 ? (
            <CredentialEmptyState kind="agent" />
          ) : null}
        </div>
      </section>
      <section aria-labelledby="mcp-credentials-heading" className="grid gap-4">
        <div className="flex items-center justify-between gap-3">
          <h2 id="mcp-credentials-heading" className="text-base font-semibold">
            MCP 凭证
          </h2>
          <Button
            size="icon"
            variant="ghost"
            aria-label="添加 MCP 凭证"
            onClick={() => {
              setCredentialType("mcp");
              setEditing(null);
              setDialogOpen(true);
            }}
          >
            <PlusIcon className="size-4" />
          </Button>
        </div>
        <div className="grid gap-4 md:grid-cols-3">
          {rules.data
            ?.filter((rule) => rule.type === "mcp")
            .map((rule) => (
              <Card key={rule.id} className="shadow-none">
                <CardHeader className="flex flex-row items-start justify-between gap-3">
                  <CardTitle>{rule.name}</CardTitle>
                  <div className="flex gap-1">
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label={`编辑 ${rule.name}`}
                      onClick={() => openEdit(rule)}
                    >
                      <SettingsIcon className="size-4" />
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label={`删除 ${rule.name}`}
                      onClick={() => {
                        setDeleteError(null);
                        setDeleting(rule);
                      }}
                    >
                      <Trash2Icon className="size-4" />
                    </Button>
                  </div>
                </CardHeader>
                <CardPanel className="text-sm text-muted-foreground">
                  <p className="break-all">{rule.base_url}</p>
                  <p className="mt-2">Streamable HTTP · {rule.header_name || "Authorization"}</p>
                  <p className="mt-2 font-mono text-xs">{rule.secret_hint}</p>
                  <p className="mt-2">已授权 {summary.data?.[rule.id] ?? 0} 个沙箱</p>
                </CardPanel>
              </Card>
            ))}
          {rules.isSuccess && !rules.data?.some((rule) => rule.type === "mcp") ? (
            <p className="text-sm text-muted-foreground">还没有 MCP 凭证。</p>
          ) : null}
        </div>
      </section>
      <section aria-labelledby="http-credentials-heading" className="grid gap-4">
        <div className="flex items-center justify-between gap-3">
          <h2 id="http-credentials-heading" className="text-base font-semibold">
            API 凭证
          </h2>
          <Button size="icon" variant="ghost" aria-label="添加 HTTP 凭证" onClick={openCreate}>
            <PlusIcon className="size-4" />
          </Button>
        </div>
        {rules.isError ? (
          <div
            role="alert"
            className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-destructive/25 px-4 py-3"
          >
            <p className="text-sm text-destructive">
              {rules.error instanceof Error ? rules.error.message : "凭证加载失败，请重试。"}
            </p>
            <Button
              size="sm"
              variant="outline"
              disabled={rules.isFetching}
              onClick={() => void rules.refetch()}
            >
              重新加载
            </Button>
          </div>
        ) : null}
        <div className="grid gap-4 md:grid-cols-3">
          {standaloneCredentials?.map((rule) => (
            <Card key={rule.id} className="shadow-none">
              <CardHeader className="flex flex-row items-start justify-between gap-3">
                <div className="flex items-center gap-2">
                  <KeyRoundIcon className="size-5 text-muted-foreground" />
                  <CardTitle>{rule.name}</CardTitle>
                </div>
                <div className="flex gap-1">
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label={`编辑 ${rule.name}`}
                    onClick={() => openEdit(rule)}
                  >
                    <SettingsIcon className="size-4" />
                  </Button>
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label={`删除 ${rule.name}`}
                    onClick={() => {
                      setDeleteError(null);
                      setDeleting(rule);
                    }}
                  >
                    <Trash2Icon className="size-4" />
                  </Button>
                </div>
              </CardHeader>
              <CardPanel className="text-sm text-muted-foreground">
                <p>
                  {ruleTypeLabel(rule.type)}
                  {rule.base_url ? ` · ${rule.base_url}` : ""}
                </p>
                <p className="mt-2 font-mono text-xs">{rule.secret_hint || "••••"}</p>
                <p className="mt-2">已授权 {summary.data?.[rule.id] ?? 0} 个沙箱</p>
                <Button
                  className="mt-3"
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setEditingAgent(undefined);
                    setInitialConnectionId(rule.id);
                    setAgentDialogOpen(true);
                  }}
                >
                  配置 Agent
                </Button>
              </CardPanel>
            </Card>
          ))}
          {rules.isLoading ? (
            <Card className="shadow-none">
              <CardPanel className="text-sm text-muted-foreground">正在加载凭证…</CardPanel>
            </Card>
          ) : null}
          {!rules.isError && standaloneCredentials?.length === 0 ? (
            <CredentialEmptyState kind="http" />
          ) : null}
        </div>
      </section>

      <p className="text-xs text-muted-foreground">在本地沙箱的“安全连接”中选择要使用的凭证。</p>

      {agentDialogOpen ? (
        <AgentConfigDialog
          open
          onOpenChange={(next) => {
            setAgentDialogOpen(next);
            if (!next) setEditingAgent(undefined);
          }}
          connections={rules.data?.filter((rule) => rule.type !== "mcp") ?? []}
          {...(editingAgent ? { initial: editingAgent } : {})}
          {...(initialConnectionId ? { initialConnectionId } : {})}
          onCreateConnection={mutations.create}
          onUpdateConnection={mutations.update}
          onSave={saveAgentConfig}
        />
      ) : null}

      {dialogOpen ? (
        <InjectionRuleDialog
          open
          onOpenChange={setDialogOpen}
          mode="persistent"
          fixedType={credentialType === "mcp" ? "mcp" : undefined}
          initial={
            editing
              ? localRuleFromPersistent(editing, { id: editing.id, type: editing.type })
              : undefined
          }
          onSubmit={submit}
        />
      ) : null}

      <Dialog
        open={Boolean(deletingAgent)}
        onOpenChange={(next) => !next && setDeletingAgent(null)}
      >
        <DialogPopup className="max-w-sm" bottomStickOnMobile={false}>
          <DialogHeader>
            <DialogTitle>删除 Agent 配置？</DialogTitle>
            <DialogDescription>
              将“{deletingAgent?.name}”从凭证库移除，关联的 API 凭证保留。
            </DialogDescription>
          </DialogHeader>
          <DialogPanel>
            {agentDeleteError ? (
              <p role="alert" className="mb-3 text-sm text-destructive">
                {agentDeleteError}
              </p>
            ) : null}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setDeletingAgent(null)}>
                取消
              </Button>
              <Button variant="destructive" onClick={() => void removeAgentConfig()}>
                删除
              </Button>
            </div>
          </DialogPanel>
        </DialogPopup>
      </Dialog>

      <Dialog open={Boolean(deleting)} onOpenChange={(next) => !next && setDeleting(null)}>
        <DialogPopup className="max-w-sm" bottomStickOnMobile={false}>
          <DialogHeader>
            <DialogTitle>删除凭证？</DialogTitle>
            <DialogDescription>
              删除“{deleting?.name}”后，使用它的沙箱将无法继续通过它访问 API。
            </DialogDescription>
          </DialogHeader>
          <DialogPanel>
            {deleteError ? (
              <p role="alert" className="mb-3 text-sm text-destructive">
                {deleteError}
              </p>
            ) : null}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setDeleting(null)}>
                取消
              </Button>
              <Button variant="destructive" onClick={() => void remove()}>
                删除
              </Button>
            </div>
          </DialogPanel>
        </DialogPopup>
      </Dialog>
    </CloudPage>
  );
}
