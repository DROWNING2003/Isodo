/** Restrict renderer access to the management API; sidecar credentials stay in main. */
export function validateSandboxRequest(
  path: unknown,
  method: unknown,
  body: unknown,
  idempotencyKey?: unknown,
): {
  path: string;
  method: string;
  body?: string;
  idempotencyKey?: string;
} {
  if (typeof path !== "string" || typeof method !== "string")
    throw new Error("Invalid sandbox request");
  const url = new URL(path, "http://sidecar.invalid");
  if (url.origin !== "http://sidecar.invalid" || !path.startsWith("/v1/") || url.hash)
    throw new Error("Invalid sandbox route");
  const resource = /^\/v1\/sandboxes\/[a-zA-Z0-9_-]+$/.test(url.pathname);
  const directory = /^\/v1\/sandboxes\/[a-zA-Z0-9_-]+\/directories$/.test(url.pathname);
  const action = /^\/v1\/sandboxes\/[a-zA-Z0-9_-]+\/(start|stop|app-link)$/.test(url.pathname);
  const allowed =
    (method === "GET" &&
      (resource || url.pathname === "/v1/sandboxes" || url.pathname === "/v1/templates")) ||
    (method === "POST" && (action || directory || url.pathname === "/v1/sandboxes")) ||
    ((method === "PATCH" || method === "DELETE") && resource);
  if (!allowed) throw new Error("Unsupported sandbox request");
  for (const key of url.searchParams.keys())
    if (key !== "limit" && key !== "offset") throw new Error("Invalid sandbox query");
  if (body !== undefined && (typeof body !== "string" || Buffer.byteLength(body) > 65536))
    throw new Error("Invalid sandbox body");
  if (
    idempotencyKey !== undefined &&
    (typeof idempotencyKey !== "string" || !/^[A-Za-z0-9._:-]{1,128}$/.test(idempotencyKey))
  )
    throw new Error("Invalid idempotency key");
  return {
    ...(typeof idempotencyKey === "string" ? { idempotencyKey } : {}),
    path: url.pathname + url.search,
    method,
    ...(body === undefined ? {} : { body }),
  };
}
