import { Link } from "@tanstack/react-router";
import { getAuthGeneration, useAuth } from "./auth";
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { GithubIcon, StarIcon } from "lucide-react";
import { Button } from "../../components/ui/button";
import { isodoFetch } from "../../api/transport";
import { CloudPage } from "./CloudPages";
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
  const [favorites, setFavorites] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const mutate = async (path: string, method: string) => {
    setPending(true);
    setError(null);
    try {
      await isodoFetch(path, { method });
      await client.invalidateQueries({ queryKey: mcpKey });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "操作失败");
    } finally {
      setPending(false);
    }
  };
  const items = (market.data?.items ?? []).filter((item) => !favorites || item.favorite);
  return (
    <CloudPage title="MCP 市场">
      <div className="flex gap-2">
        <Button variant={favorites ? "outline" : "default"} onClick={() => setFavorites(false)}>
          市场
        </Button>
        <Button variant={favorites ? "default" : "outline"} onClick={() => setFavorites(true)}>
          我的收藏
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">
        收藏服务后，连接第三方账号，再选择允许使用它的沙箱。
      </p>
      {market.isLoading ? <p role="status">正在加载 MCP 市场…</p> : null}
      {market.isError ? (
        <div role="alert">
          MCP 市场加载失败。
          <Button variant="outline" onClick={() => void market.refetch()}>
            重新加载
          </Button>
        </div>
      ) : null}
      {!market.isLoading && !market.isError && !items.length ? (
        <p>还没有收藏的 MCP。在市场收藏 GitHub 后会显示在这里。</p>
      ) : null}
      {items.map((item) => (
        <article
          key={item.market_mcp_id}
          className="grid max-w-2xl gap-4 rounded-xl border border-border bg-background p-5"
        >
          <div className="flex gap-3">
            <GithubIcon className="size-9" />
            <div>
              <h2 className="font-semibold">{item.name}</h2>
              <p className="text-xs text-muted-foreground">
                {item.provider} · Streamable HTTP · OAuth 2.0 · 只读
              </p>
            </div>
          </div>
          <p className="text-sm">{item.description}</p>
          <p className="text-xs text-muted-foreground">{item.endpoint}</p>
          <p role="status" className="text-sm">
            {oauth.pending ? "正在授权，请在系统浏览器完成" : mcpStatus(item.status)} ·{" "}
            {item.favorite ? "已收藏" : "未收藏"}
          </p>
          {!item.oauth_configured ? (
            <p className="text-sm text-muted-foreground">
              管理员尚未配置 GitHub App 和 HTTPS 回调。可以先收藏。
            </p>
          ) : null}
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              disabled={pending}
              onClick={() =>
                void mutate(
                  `/v1/mcp/favorites/${item.market_mcp_id}`,
                  item.favorite ? "DELETE" : "PUT",
                )
              }
            >
              <StarIcon className="size-4" />
              {item.favorite ? "取消收藏" : "收藏"}
            </Button>
            <Button
              disabled={pending || oauth.pending || !item.oauth_configured}
              onClick={() => void oauth.connect()}
            >
              {item.status === "connected" ? "重新授权" : "连接 GitHub"}
            </Button>
            {item.status !== "disconnected" ? (
              <Button
                variant="outline"
                disabled={pending || oauth.pending}
                onClick={() => void mutate("/v1/mcp/connections/github", "DELETE")}
              >
                断开连接
              </Button>
            ) : null}
            {oauth.pending ? (
              <Button variant="outline" onClick={oauth.cancel}>
                取消授权
              </Button>
            ) : null}
          </div>
        </article>
      ))}
      {error || oauth.error ? (
        <p role="alert" className="text-sm text-destructive">
          {error || oauth.error}
        </p>
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
              GitHub 官方 MCP · {mcpStatus(item.status)}
              {bindings
                ? ` · ${bindingSummary(
                    bindings.find((binding) => binding.market_mcp_id === item.market_mcp_id),
                    value.includes(item.market_mcp_id),
                  )}`
                : null}
            </span>
          </span>
          {item.status !== "connected" ? (
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
