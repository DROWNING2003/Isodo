/** Only the configured Isodo preview hosts receive gateway cookies. */
function isPreviewURL(url: URL): boolean {
  const domain = import.meta.env.VITE_ISODO_PREVIEW_DOMAIN?.trim().toLowerCase();
  if (!domain || url.username || url.password || !["http:", "https:"].includes(url.protocol))
    return false;
  const suffix = `.${domain}`;
  return (
    url.hostname.endsWith(suffix) &&
    /^\d+-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
      url.hostname.slice(0, -suffix.length),
    )
  );
}

export const previewFetch: typeof globalThis.fetch = (input, init) => {
  const value = input instanceof Request ? input.url : String(input);
  const url = new URL(value, globalThis.location?.href ?? "http://localhost");
  return globalThis.fetch(input, isPreviewURL(url) ? { ...init, credentials: "include" } : init);
};

/** Establish the HttpOnly gateway cookie before T3 descriptor/session discovery. */
export async function bootstrapSandboxPreview(pairingURL: string): Promise<void> {
  const url = new URL(pairingURL);
  if (!url.pathname.startsWith("/__isodo/auth/")) return;
  if (!isPreviewURL(url)) throw new Error("预览网关域名与 App 配置不匹配。");
  url.hash = "";
  url.search = new URLSearchParams({ next: "/__isodo/ready" }).toString();
  const response = await previewFetch(url.toString(), {
    credentials: "include",
    cache: "no-store",
    referrerPolicy: "no-referrer",
    signal: AbortSignal.timeout(15_000),
  });
  if (response.status !== 204) throw new Error("预览网关认证未完成，请重试连接。");
}
