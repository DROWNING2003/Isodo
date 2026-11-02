import { Link } from "@tanstack/react-router";
import { getAuthGeneration, useAuth } from "./auth";
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  GithubIcon,
  StarIcon,
  PlusIcon,
  SearchIcon,
  ServerIcon,
  ArrowLeftIcon,
  ExternalLinkIcon,
} from "lucide-react";
import { Button } from "../../components/ui/button";
import { isodoFetch } from "../../api/transport";
import { CloudPage } from "./CloudPages";
import { CustomMCPDialog } from "./CustomMCPDialog";
import { deleteCustomMCP, type MCPCatalogEntry } from "../../api/public.generated";
import { Input } from "../../components/ui/input";
import {
  bindMCP,
  mcpKey,
  mcpStatus,
  useMCPMarket,
  useMCPOAuth,
  type MCPBinding,
} from "./mcpMarket";

export function MCPMarketPage() {
  const market = useMCPMarket();
  const client = useQueryClient();
  const oauth = useMCPOAuth();
  const [personal, setPersonal] = useState(false);
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const all = market.data?.items ?? [];
  const mine = all.filter((item) => item.favorite || item.source === "custom");
  const selectedItem = all.find((item) => item.market_mcp_id === selected);
  const query = search.trim().toLocaleLowerCase();
  const visible = all.filter(
    (item) =>
      (personal ? item.favorite || item.source === "custom" : item.source !== "custom") &&
      `${item.name} ${item.description} ${item.provider}`.toLocaleLowerCase().includes(query),
  );
  const refresh = () => client.invalidateQueries({ queryKey: mcpKey });
  const mutate = async (path: string, method: string) => {
    setPending(true);
    setError(null);
    try {
      await isodoFetch(path, { method });
      await refresh();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "操作失败");
    } finally {
      setPending(false);
    }
  };
  const removeCustom = async (item: MCPCatalogEntry) => {
    if (
      !item.custom_server_id ||
      !window.confirm(`删除「${item.name}」并撤销它在所有沙箱中的访问？`)
    )
      return;
    setPending(true);
    setError(null);
    try {
      await deleteCustomMCP(item.custom_server_id);
      await refresh();
      setSelected(null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "删除失败");
    } finally {
      setPending(false);
    }
  };
  const favorite = (item: MCPCatalogEntry) => (
    <Button
      variant="outline"
      size="sm"
      disabled={pending}
      onClick={() =>
        void mutate(`/v1/mcp/favorites/${item.market_mcp_id}`, item.favorite ? "DELETE" : "PUT")
      }
    >
      <StarIcon className="size-4" />
      {item.favorite ? "取消收藏" : "收藏"}
    </Button>
  );
  return (
    <CloudPage title="MCP 市场">
      <div className="mx-auto flex w-full max-w-6xl gap-10">
        <aside className="hidden w-44 shrink-0 space-y-4 lg:block" aria-label="我的 MCP">
          <p className="text-xs font-medium text-muted-foreground">我的 MCP</p>
          <div className="grid gap-1">
            {mine.map((item) => (
              <button
                key={item.market_mcp_id}
                type="button"
                onClick={() => setSelected(item.market_mcp_id)}
                className={`flex items-center gap-2 rounded-lg px-3 py-2 text-left text-sm ${selected === item.market_mcp_id ? "bg-muted" : "hover:bg-muted/60"}`}
              >
                {item.source === "custom" ? (
                  <ServerIcon className="size-4 shrink-0" />
                ) : (
                  <GithubIcon className="size-4 shrink-0" />
                )}
                <span className="truncate">{item.name}</span>
              </button>
            ))}
            {!mine.length ? (
              <p className="text-xs text-muted-foreground">收藏或添加服务后显示在这里。</p>
            ) : null}
          </div>
        </aside>
        <main className="min-w-0 flex-1 space-y-7">
          <header className="flex flex-wrap items-start justify-between gap-4">
            <div>
              <h1 className="text-2xl font-semibold tracking-tight">MCP</h1>
              <p className="mt-2 text-sm text-muted-foreground">
                连接你的工具和数据源，按需授权给沙箱。
              </p>
            </div>
            <Button onClick={() => setAdding(true)} disabled={pending}>
              <PlusIcon className="size-4" />
              添加 MCP
            </Button>
          </header>
          {selectedItem ? (
            <>
              <button
                type="button"
                className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground"
                onClick={() => setSelected(null)}
              >
                <ArrowLeftIcon className="size-4" />
                返回 MCP 列表
              </button>
              <div className="flex items-center gap-4">
                {selectedItem.source === "custom" ? (
                  <ServerIcon className="size-10" />
                ) : (
                  <GithubIcon className="size-10" />
                )}
                <div>
                  <h2 className="text-xl font-semibold">{selectedItem.name}</h2>
                  <p className="mt-1 text-sm text-muted-foreground">
                    {selectedItem.description || "你的私有远程 MCP 服务"}
                  </p>
                </div>
              </div>
              <section
                className="space-y-4 rounded-xl border border-border p-5"
                aria-label="连接设置"
              >
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <h3 className="text-sm font-medium">
                      {selectedItem.source === "custom" ? "服务器连接" : "GitHub 账号"}
                    </h3>
                    <p role="status" className="mt-1 text-sm text-muted-foreground">
                      {oauth.pending ? "正在授权，请在浏览器完成" : mcpStatus(selectedItem.status)}
                    </p>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {selectedItem.source === "custom" ? (
                      <Button
                        variant="outline"
                        disabled={pending}
                        onClick={() => void removeCustom(selectedItem)}
                      >
                        删除 MCP
                      </Button>
                    ) : (
                      <>
                        <Button
                          disabled={pending || oauth.pending || !selectedItem.oauth_configured}
                          onClick={() => void oauth.connect()}
                        >
                          {selectedItem.status === "connected" ? "重新授权账号" : "连接 GitHub"}
                        </Button>
                        {selectedItem.status !== "disconnected" ? (
                          <Button
                            variant="outline"
                            disabled={pending || oauth.pending}
                            onClick={() => void mutate("/v1/mcp/connections/github", "DELETE")}
                          >
                            断开连接
                          </Button>
                        ) : null}
                        {favorite(selectedItem)}
                      </>
                    )}
                  </div>
                </div>
                {selectedItem.source === "custom" ? (
                  <div className="border-t border-border pt-4 text-sm">
                    <p className="break-all text-muted-foreground">{selectedItem.endpoint}</p>
                    <p className="mt-2 text-xs text-muted-foreground">
                      {selectedItem.auth_kind === "bearer"
                        ? "Bearer Token · 已加密保存"
                        : "无需认证"}{" "}
                      · 仅当前账号可见
                    </p>
                  </div>
                ) : (
                  <div className="flex flex-wrap items-start justify-between gap-4 border-t border-border pt-4">
                    <div>
                      <h3 className="text-sm font-medium">仓库访问</h3>
                      <p className="mt-2 max-w-md text-xs leading-relaxed text-muted-foreground">
                        在 GitHub 的已安装应用中找到 Isodo，点击 Configure
                        重新选择仓库。修改后返回即可，无需断开账号连接。
                      </p>
                    </div>
                    <a
                      href="https://github.com/settings/installations"
                      target="_blank"
                      rel="noopener noreferrer"
                      className="flex items-center gap-1.5 text-sm font-medium underline underline-offset-4"
                    >
                      在 GitHub 配置
                      <ExternalLinkIcon className="size-3.5" />
                    </a>
                  </div>
                )}
              </section>
              <p className="text-xs text-muted-foreground">
                在沙箱的“安全连接”中选择要启用的 MCP，再统一保存。
              </p>
            </>
          ) : (
            <>
              <div className="flex flex-wrap items-center justify-between gap-4">
                <div className="flex gap-2" aria-label="MCP 分类">
                  <Button
                    variant={!personal ? "secondary" : "ghost"}
                    onClick={() => setPersonal(false)}
                  >
                    市场
                  </Button>
                  <Button
                    variant={personal ? "secondary" : "ghost"}
                    onClick={() => setPersonal(true)}
                  >
                    我的 MCP
                  </Button>
                </div>
                <label className="relative w-full sm:w-64">
                  <SearchIcon className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted-foreground" />
                  <Input
                    aria-label="搜索 MCP"
                    placeholder="搜索 MCP"
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                    className="pl-9"
                  />
                </label>
              </div>
              {market.isLoading ? <p role="status">正在加载 MCP…</p> : null}
              {market.isError ? (
                <div role="alert">
                  MCP 市场加载失败。
                  <Button variant="outline" onClick={() => void market.refetch()}>
                    重新加载
                  </Button>
                </div>
              ) : null}
              {!market.isLoading && !market.isError && !visible.length ? (
                <p className="py-8 text-sm text-muted-foreground">
                  {query
                    ? "没有找到符合条件的 MCP。"
                    : personal
                      ? "还没有收藏或添加的 MCP。"
                      : "暂无可用的市场服务。"}
                </p>
              ) : null}
              <div className="grid gap-2 xl:grid-cols-2">
                {visible.map((item) => (
                  <article
                    key={item.market_mcp_id}
                    className="flex items-center gap-3 rounded-xl p-4 transition-colors hover:bg-muted/60"
                  >
                    <button
                      type="button"
                      aria-label={`查看 ${item.name} MCP`}
                      className="flex min-w-0 flex-1 items-center gap-3 text-left"
                      onClick={() => setSelected(item.market_mcp_id)}
                    >
                      {item.source === "custom" ? (
                        <ServerIcon className="size-8 shrink-0" />
                      ) : (
                        <GithubIcon className="size-8 shrink-0" />
                      )}
                      <span className="min-w-0">
                        <span className="block text-sm font-medium">{item.name}</span>
                        <span className="mt-1 block truncate text-xs text-muted-foreground">
                          {item.description || "自定义远程 MCP"}
                        </span>
                        <span className="mt-1 block text-xs text-muted-foreground">
                          {mcpStatus(item.status)}
                          {item.source === "custom" ? " · 私有" : " · 只读"}
                        </span>
                      </span>
                    </button>
                    {item.source === "custom" ? null : favorite(item)}
                  </article>
                ))}
              </div>
            </>
          )}
          {oauth.pending ? (
            <Button variant="outline" onClick={oauth.cancel}>
              取消授权
            </Button>
          ) : null}
          {error || oauth.error ? (
            <p role="alert" className="text-sm text-destructive">
              {error || oauth.error}
            </p>
          ) : null}
        </main>
      </div>
      {adding ? (
        <CustomMCPDialog
          onClose={() => setAdding(false)}
          onCreated={async (item) => {
            await refresh();
            setPersonal(true);
            setSelected(item.market_mcp_id);
          }}
        />
      ) : null}
    </CloudPage>
  );
}

function bindingSummary(binding: MCPBinding | undefined, selected: boolean): string {
  if (!binding) return selected ? "待启用" : "未启用";
  if (!selected) return "待停用";
  return binding.sync_status === "applied" ? "已启用" : "配置待同步，保存以重试";
}

export function MCPFavoriteFields({
  value,
  onChange,
  disabled = false,
  bindings,
}: {
  value: string[];
  onChange: (ids: string[]) => void;
  disabled?: boolean;
  bindings?: readonly MCPBinding[];
}) {
  const market = useMCPMarket();
  const oauth = useMCPOAuth();
  const favorites = (market.data?.items ?? []).filter(
    (item) =>
      item.favorite || bindings?.some((binding) => binding.market_mcp_id === item.market_mcp_id),
  );
  return (
    <div className="grid gap-2">
      <h3 className="text-sm font-medium">MCP</h3>
      {market.isLoading ? <p>正在加载收藏…</p> : null}
      {market.isError ? (
        <p role="alert">
          收藏读取失败。
          <button type="button" onClick={() => void market.refetch()}>
            重试
          </button>
        </p>
      ) : null}
      {!market.isLoading && !market.isError && !favorites.length ? (
        <p className="text-sm text-muted-foreground">还没有收藏的 MCP，请先到 MCP 市场收藏。</p>
      ) : null}
      {favorites.map((item) => (
        <div
          key={item.market_mcp_id}
          className="flex items-center gap-3 rounded-lg border border-border p-3"
        >
          <input
            aria-label={`启用 ${item.name} MCP`}
            type="checkbox"
            checked={value.includes(item.market_mcp_id)}
            disabled={
              disabled || (item.status !== "connected" && !value.includes(item.market_mcp_id))
            }
            onChange={(e) =>
              onChange(
                e.target.checked
                  ? [...value, item.market_mcp_id]
                  : value.filter((id) => id !== item.market_mcp_id),
              )
            }
          />
          <span className="flex-1 text-sm">
            {item.name}
            <span className="block text-xs text-muted-foreground">
              {item.source === "custom" ? "自定义 MCP" : "GitHub 官方 MCP"} ·{" "}
              {mcpStatus(item.status)}
              {bindings
                ? ` · ${bindingSummary(
                    bindings.find((binding) => binding.market_mcp_id === item.market_mcp_id),
                    value.includes(item.market_mcp_id),
                  )}`
                : null}
            </span>
          </span>
          {item.market_mcp_id === "github" && item.status !== "connected" ? (
            <Button
              type="button"
              size="sm"
              disabled={disabled || oauth.pending || !item.oauth_configured}
              onClick={() => void oauth.connect()}
            >
              连接
            </Button>
          ) : null}
        </div>
      ))}
      {oauth.pending ? (
        <p role="status">
          正在系统浏览器授权…{" "}
          <button type="button" onClick={oauth.cancel}>
            取消
          </button>
        </p>
      ) : null}
      {oauth.error ? (
        <p role="alert" className="text-sm text-destructive">
          {oauth.error}
        </p>
      ) : null}
    </div>
  );
}

export function useSandboxMCPSelection(sandboxId: string, enabled = true) {
  const [draft, setDraft] = useState<{ scope: string; ids: string[] } | null>(null);
  const auth = useAuth();
  const bindings = useQuery({
    queryKey: [...mcpKey, "bindings", getAuthGeneration(), sandboxId],
    enabled: enabled && auth.status === "authenticated",
    queryFn: () =>
      isodoFetch<{ items: MCPBinding[] }>(`/v1/sandboxes/${encodeURIComponent(sandboxId)}/mcp`),
  });
  const scope = `${getAuthGeneration()}:${sandboxId}`;
  const items = bindings.data?.items ?? [];
  const boundIds = items.map((item) => item.market_mcp_id);
  const ids = draft?.scope === scope ? draft.ids : boundIds;
  const changed =
    ids.some((id) => !boundIds.includes(id)) || boundIds.some((id) => !ids.includes(id));
  const update = async (changes: readonly (readonly [string, "PUT" | "DELETE" | "POST"])[]) => {
    try {
      for (const [id, method] of changes) {
        if (`${getAuthGeneration()}:${sandboxId}` !== scope)
          throw new Error("账号已切换，请重新加载");
        const result = await bindMCP(sandboxId, id, method);
        if (result.sync_status !== "applied")
          throw new Error(result.message ?? "MCP 绑定已保存，配置尚未同步，请重新保存重试。");
      }
      return changes.length;
    } finally {
      if (changes.length) await bindings.refetch({ throwOnError: true });
    }
  };
  return {
    items,
    ids,
    changed,
    loading: bindings.isLoading,
    failed: bindings.isError,
    setIds: (next: string[]) => setDraft({ scope, ids: next }),
    refetch: () => bindings.refetch({ throwOnError: true }),
    resetDraft: () => setDraft(null),
    revokeRemoved: () =>
      update(boundIds.filter((id) => !ids.includes(id)).map((id) => [id, "DELETE"] as const)),
    applyAdded: () =>
      update(
        ids.flatMap<readonly [string, "PUT" | "POST"]>((id) => {
          const existing = items.find((item) => item.market_mcp_id === id);
          return !existing
            ? [[id, "PUT"] as const]
            : existing.sync_status !== "applied"
              ? [[id, "POST"] as const]
              : [];
        }),
      ),
  };
}

export function SandboxMCPBindings({
  state,
  disabled = false,
}: {
  state: ReturnType<typeof useSandboxMCPSelection>;
  disabled?: boolean;
}) {
  return (
    <section className="grid gap-3">
      <MCPFavoriteFields
        value={state.ids}
        onChange={state.setIds}
        bindings={state.items}
        disabled={disabled || state.loading || state.failed}
      />
      {state.failed ? (
        <p role="alert">
          MCP 读取失败。
          <button type="button" onClick={() => void state.refetch().catch(() => {})}>
            重新加载
          </button>
        </p>
      ) : null}
    </section>
  );
}
