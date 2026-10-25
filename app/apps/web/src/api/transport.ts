export class IsodoAPIError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
  ) {
    super(message);
  }
}
// Shared same-origin transport for the generated OpenAPI client.
let getCSRF: () => string | null = () => null;
let onUnauthorized: () => void = () => {};
export function configureSessionTransport(csrf: () => string | null, unauthorized: () => void) {
  getCSRF = csrf;
  onUnauthorized = unauthorized;
}
export async function isodoFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (typeof init.body === "string" && !headers.has("Content-Type"))
    headers.set("Content-Type", "application/json");
  if (init.body instanceof FormData) headers.delete("Content-Type");
  const csrf = getCSRF();
  if (init.method && init.method !== "GET" && csrf) headers.set("X-CSRF-Token", csrf);
  const response = await fetch(path, {
    ...init,
    headers,
    credentials: "same-origin",
    cache: "no-store",
  });
  if (response.status === 401) {
    onUnauthorized();
    throw new Error("登录已过期，请重新登录");
  }
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    const code = body?.error?.code;
    const serverMessage = typeof body?.error?.message === "string" ? body.error.message : "";
    const message =
      response.status === 404 && path.split("?")[0] === "/v1/injection-rules"
        ? "凭证接口不存在（HTTP 404），请更新并重启 Isodo API 服务。"
        : code === "API_KEY_LIMIT"
          ? "最多保留 10 个未撤销密钥，请先撤销旧密钥。"
          : code === "INVALID_REQUEST"
            ? "请检查名称、访问权限和到期时间。"
            : code === "INVALID_CSRF"
              ? "页面已失效，请刷新后重试。"
              : code === "INJECTION_RULE_EXISTS"
                ? "同名安全连接已存在，请换一个名称。"
                : code === "INVALID_INJECTION_RULE"
                  ? serverMessage || "安全连接配置不完整，请检查目标地址和密钥。"
                  : code === "SESSION_REQUIRED"
                    ? "请使用交互式登录后再管理安全连接。"
                    : code === "INJECTIONS_UNAVAILABLE"
                      ? "安全连接服务暂时不可用，请稍后重试。"
                      : "操作失败，请稍后重试。";
    throw new IsodoAPIError(message, response.status, code ?? "UNKNOWN");
  }
  if (response.status === 204) return undefined as T;
  if (response.headers.get("Content-Type")?.split(";")[0] === "application/octet-stream")
    return response.blob() as Promise<T>;
  return response.json() as Promise<T>;
}
