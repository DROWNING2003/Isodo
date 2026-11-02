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

export function MCPFavoriteFields({
  value,
  onChange,
  disabled = false,
}: {
  value: string[];
  onChange: (ids: string[]) => void;
  disabled?: boolean;
}) {
  const market = useMCPMarket();
  const oauth = useMCPOAuth();
  const favorites = (market.data?.items ?? []).filter((item) => item.favorite);
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
            disabled={disabled || item.status !== "connected"}
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

export function SandboxMCPBindings({ sandboxId }: { sandboxId: string }) {
  const [ids, setIds] = useState<string[]>([]);
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const auth = useAuth();
  const bindings = useQuery({
    queryKey: [...mcpKey, "bindings", getAuthGeneration(), sandboxId],
    enabled: auth.status === "authenticated",
    queryFn: () =>
      isodoFetch<{ items: MCPBinding[] }>(`/v1/sandboxes/${encodeURIComponent(sandboxId)}/mcp`),
  });
  const apply = async (id: string, method: "PUT" | "DELETE" | "POST") => {
    setPending(true);
    setNotice(null);
    try {
      const result = await bindMCP(sandboxId, id, method);
      await bindings.refetch();
      setNotice(
        result.sync_status === "applied"
          ? "配置已应用。请重启当前 Codex 会话以加载；撤销会立即阻止调用。"
          : (result.message ?? "绑定已保存，配置尚未应用，请重试。"),
      );
    } catch (cause) {
      setNotice(cause instanceof Error ? cause.message : "MCP 操作失败");
    } finally {
      setPending(false);
    }
  };
  return (
    <section className="grid gap-3 rounded-xl border border-border p-4">
      <MCPFavoriteFields value={ids} onChange={setIds} disabled={pending} />
      <Button
        type="button"
        disabled={pending || !ids.length}
        onClick={() =>
          void (async () => {
            for (const id of ids) await apply(id, "PUT");
          })()
        }
      >
        绑定所选 MCP
      </Button>
      {bindings.isError ? (
        <p role="alert">
          绑定读取失败。<button onClick={() => void bindings.refetch()}>重新加载</button>
        </p>
      ) : null}
      {bindings.data?.items.map((item) => (
        <div key={item.sandbox_binding_id} className="flex flex-wrap items-center gap-2 text-sm">
          <span>
            GitHub · 已绑定沙箱 · {mcpStatus(item.status)} ·{" "}
            {item.sync_status === "applied" ? "配置已应用" : "配置待应用"}
          </span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={pending}
            onClick={() => void apply(item.market_mcp_id, "POST")}
          >
            应用配置
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={pending}
            onClick={() => void apply(item.market_mcp_id, "DELETE")}
          >
            移除
          </Button>
        </div>
      ))}
      {notice ? (
        <div className="grid gap-2">
          <p role="status" className="text-sm">
            {notice}
          </p>
          <Link className="text-sm underline" to="/sandboxes/$sandboxId" params={{ sandboxId }}>
            返回工作台，新建 Codex 任务加载配置
          </Link>
        </div>
      ) : null}
    </section>
  );
}
