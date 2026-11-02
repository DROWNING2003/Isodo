import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { isodoFetch } from "../../api/transport";
import {
  listMCPCatalog,
  listSandboxMCPBindings,
  type MCPCatalogEntry,
  bindSandboxMCP,
  unbindSandboxMCP,
  syncSandboxMCP,
} from "../../api/public.generated";
import { ensureLocalApi } from "../../localApi";
import { getAuthGeneration, useAuth } from "./auth";

export type { MCPBinding, MCPSync } from "../../api/public.generated";
export type MarketMCP = MCPCatalogEntry;
export const mcpKey = ["isodo", "mcp-market"] as const;
export function useMCPMarket() {
  const auth = useAuth();
  return useQuery({
    queryKey: [...mcpKey, getAuthGeneration()],
    enabled: auth.status === "authenticated",
    queryFn: () => listMCPCatalog(),
    refetchInterval: 10_000,
  });
}
export function mcpStatus(status: string) {
  return (
    (
      {
        connected: "已连接",
        expired: "授权已过期",
        disconnected: "未连接",
        failed: "授权失败",
      } as Record<string, string>
    )[status] ?? "未连接"
  );
}
export async function bindMCP(
  sandbox: string,
  market: string,
  method: "PUT" | "DELETE" | "POST" = "PUT",
) {
  if (method === "PUT") return bindSandboxMCP(sandbox, market);
  if (method === "DELETE") return unbindSandboxMCP(sandbox, market);
  return syncSandboxMCP(sandbox, market);
}
export function useMCPOAuth() {
  const client = useQueryClient();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  const cancel = () => controller.current?.abort();
  const connect = async (provider: string = "github") => {
    if (controller.current) return;
    const abort = new AbortController();
    controller.current = abort;
    setPending(true);
    setError(null);
    const generation = getAuthGeneration();
    let transaction: string | undefined;
    let popup: Window | null = null;
    try {
      // Reserve the window synchronously during the click so browsers do not
      // block it after asynchronous OAuth discovery. Electron uses the OS browser.
      if (!window.desktopBridge) {
        popup = window.open("about:blank", "_blank");
        if (!popup) throw new Error("浏览器阻止了授权弹窗，请允许弹窗后重试。");
        popup.opener = null;
      }
      const started = await isodoFetch<{ transaction_id: string; authorization_url: string }>(
        `/v1/mcp/connections/${provider}/authorize`,
        {
          method: "POST",
          signal: abort.signal,
        },
      );
      transaction = started.transaction_id;
      if (getAuthGeneration() !== generation) throw new Error("账号已切换，请重新授权");
      const url = new URL(started.authorization_url);
      if (!validMCPOAuthURL(provider, url)) throw new Error("授权地址校验失败");
      if (popup) popup.location.replace(url.toString());
      else await ensureLocalApi().shell.openExternal(url.toString());
      for (let i = 0; i < 150; i++) {
        await new Promise<void>((resolve, reject) => {
          const timer = setTimeout(() => {
            abort.signal.removeEventListener("abort", cancelled);
            resolve();
          }, 2000);
          const cancelled = () => {
            clearTimeout(timer);
            reject(new Error("已取消授权"));
          };
          if (abort.signal.aborted) cancelled();
          else abort.signal.addEventListener("abort", cancelled, { once: true });
        });
        if (getAuthGeneration() !== generation) throw new Error("账号已切换，请重新授权");
        const result = await isodoFetch<{ status: string }>(
          `/v1/mcp/oauth/transactions/${transaction}`,
          { signal: abort.signal },
        );
        if (result.status === "connected") {
          transaction = undefined;
          await client.invalidateQueries({ queryKey: mcpKey });
          return;
        }
        if (!["pending", "processing"].includes(result.status))
          throw new Error("MCP 授权未完成，请重新连接");
      }
      throw new Error("MCP 授权已超时");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "授权失败");
    } finally {
      popup?.close();
      if (transaction && getAuthGeneration() === generation)
        await isodoFetch(`/v1/mcp/oauth/transactions/${transaction}`, { method: "DELETE" }).catch(
          () => {},
        );
      controller.current = null;
      setPending(false);
    }
  };
  return { connect, cancel, pending, error };
}

/** Reapply existing bindings after an explicit Agent config replacement. */
export async function restoreBoundMCP(sandboxId: string): Promise<void> {
  const bindings = await listSandboxMCPBindings(sandboxId);
  for (const binding of bindings.items) {
    const result = await syncSandboxMCP(sandboxId, binding.market_mcp_id);
    if (result.sync_status !== "applied")
      throw new Error("Agent 配置已保存，但 MCP 配置恢复失败，请在安全连接中重新应用。");
  }
}

export function validMCPOAuthURL(provider: string, url: URL): boolean {
  if (url.username || url.password || url.hash) return false;
  if (provider === "github")
    return url.origin === "https://github.com" && url.pathname === "/login/oauth/authorize";
  if (provider === "notion")
    return url.origin === "https://mcp.notion.com" && url.pathname === "/authorize";
  return false;
}
