import { randomUUID } from "../../lib/utils";
import type {
  CreateSandbox,
  PatchSandbox,
  Sandbox,
  SandboxAppLink,
  SandboxList,
  TemplateList,
} from "../../api/public.generated";
export type SandboxTransport = (path: string, init?: RequestInit) => Promise<unknown>;
export interface PageParams {
  limit?: number;
  offset?: number;
}
export interface ActionOptions {
  idempotencyKey?: string;
  signal?: AbortSignal;
}
export interface SandboxClient {
  list(params?: PageParams): Promise<SandboxList>;
  get(id: string, options?: { signal?: AbortSignal }): Promise<Sandbox>;
  create(input: CreateSandbox, options?: ActionOptions): Promise<Sandbox>;
  update(id: string, input: PatchSandbox): Promise<Sandbox>;
  start(id: string, options?: ActionOptions): Promise<Sandbox>;
  stop(id: string, options?: ActionOptions): Promise<Sandbox>;
  delete(id: string, options?: ActionOptions): Promise<Sandbox | undefined>;
  listTemplates(params?: PageParams): Promise<TemplateList>;
  createAppLink(id: string): Promise<SandboxAppLink>;
}
export class SandboxClientError extends Error {
  constructor(
    readonly code: string,
    message: string,
    readonly targetRevision?: string,
    readonly snapshot?: Sandbox,
  ) {
    super(message);
  }
}
function decodeSandbox(value: unknown): Sandbox {
  if (
    typeof value !== "object" ||
    value === null ||
    !("id" in value) ||
    typeof value.id !== "string" ||
    !("name" in value) ||
    typeof value.name !== "string" ||
    !("desired_state" in value) ||
    !["running", "stopped"].includes(String(value.desired_state)) ||
    !("observed_state" in value) ||
    ![
      "requested",
      "preparing",
      "starting",
      "running",
      "stopping",
      "stopped",
      "error",
      "unknown",
    ].includes(String(value.observed_state))
  )
    throw new SandboxClientError("INVALID_RESPONSE", "沙箱服务返回了无效的资源。");
  return value as Sandbox;
}
function decodePage(value: unknown): {
  items: unknown[];
  total: number;
  limit: number;
  offset: number;
} {
  if (
    typeof value !== "object" ||
    value === null ||
    !("items" in value) ||
    !Array.isArray(value.items) ||
    !("total" in value) ||
    !Number.isInteger(value.total) ||
    !("limit" in value) ||
    !Number.isInteger(value.limit) ||
    !("offset" in value) ||
    !Number.isInteger(value.offset)
  )
    throw new SandboxClientError("INVALID_RESPONSE", "服务返回了无效的分页结果。");
  return value as { items: unknown[]; total: number; limit: number; offset: number };
}
function pageQuery(params: PageParams = {}) {
  return new URLSearchParams({
    limit: String(params.limit ?? 100),
    offset: String(params.offset ?? 0),
  });
}
function actionInit(options: ActionOptions = {}): RequestInit {
  return {
    method: "POST",
    headers: { "Idempotency-Key": options.idempotencyKey ?? randomUUID() },
    ...(options.signal ? { signal: options.signal } : {}),
  };
}
/** IPC and HTTP share routes and DTOs; only the supplied transport differs. */
export function createSandboxClient(request: SandboxTransport): SandboxClient {
  const path = (id: string) => `/v1/sandboxes/${encodeURIComponent(id)}`;
  return {
    list: async (params) => {
      const page = decodePage(await request(`/v1/sandboxes?${pageQuery(params)}`));
      return { ...page, items: page.items.map(decodeSandbox) };
    },
    get: async (id, options) => decodeSandbox(await request(path(id), options)),
    create: async (input, options) =>
      decodeSandbox(
        await request("/v1/sandboxes", { ...actionInit(options), body: JSON.stringify(input) }),
      ),
    update: async (id, input) =>
      decodeSandbox(await request(path(id), { method: "PATCH", body: JSON.stringify(input) })),
    start: async (id, options) =>
      decodeSandbox(await request(`${path(id)}/start`, actionInit(options))),
    stop: async (id, options) =>
      decodeSandbox(await request(`${path(id)}/stop`, actionInit(options))),
    delete: async (id, options) => {
      const value = await request(path(id), { ...actionInit(options), method: "DELETE" });
      return value == null ? undefined : decodeSandbox(value);
    },
    listTemplates: async (params) =>
      decodePage(
        await request(`/v1/templates?scope=available&${pageQuery(params)}`),
      ) as TemplateList,
    createAppLink: async (id) => {
      const value = await request(`${path(id)}/app-link`, { method: "POST" });
      if (
        typeof value !== "object" ||
        value === null ||
        !("sandbox_id" in value) ||
        value.sandbox_id !== id ||
        !("pairing_url" in value) ||
        typeof value.pairing_url !== "string" ||
        !("url" in value) ||
        typeof value.url !== "string"
      )
        throw new SandboxClientError("INVALID_RESPONSE", "沙箱没有返回有效的工作台入口。");
      return value as SandboxAppLink;
    },
  };
}
function delay(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    signal.throwIfAborted();
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", abort);
      resolve();
    }, ms);
    const abort = () => {
      clearTimeout(timer);
      reject(signal.reason);
    };
    signal.addEventListener("abort", abort, { once: true });
  });
}
/** A wait never resubmits mutations; a different revision cannot satisfy it. */
export async function waitForSandboxAction(
  client: Pick<SandboxClient, "get">,
  accepted: Sandbox,
  options: { timeoutMs?: number; pollIntervalMs?: number; signal?: AbortSignal } = {},
): Promise<Sandbox> {
  const revision = accepted.revision;
  if (!revision || !/^[1-9][0-9]*$/.test(revision) || !accepted.last_action)
    throw new SandboxClientError(
      "INVALID_RESPONSE",
      "沙箱动作没有返回 revision。",
      revision,
      accepted,
    );
  let latest = accepted;
  const fail = (code: string, message: string) =>
    new SandboxClientError(code, message, revision, latest);
  const timeoutMs = options.timeoutMs ?? 300_000;
  if (timeoutMs <= 0) throw fail("WAIT_TIMEOUT", "等待沙箱超时，后台动作仍可能继续。");
  const timeout = AbortSignal.timeout(timeoutMs);
  const signal = options.signal ? AbortSignal.any([timeout, options.signal]) : timeout;
  try {
    for (;;) {
      signal.throwIfAborted();
      if (latest.revision !== revision)
        throw fail("ACTION_SUPERSEDED", "此沙箱动作已被后续操作替代。");
      const action = latest.last_action;
      if (!action || action.revision !== revision || action.type !== accepted.last_action!.type)
        throw fail("INVALID_RESPONSE", "沙箱动作与 revision 不一致。");
      if (action.status === "superseded")
        throw fail("ACTION_SUPERSEDED", "此沙箱动作已被后续操作替代。");
      if (action.status === "failed")
        throw fail(
          action.error?.code ?? "ACTION_FAILED",
          action.error?.message ?? "沙箱动作执行失败。",
        );
      const reached =
        action.type === "delete"
          ? Boolean(latest.deletion?.completed_at)
          : latest.observed_state === (action.type === "stop" ? "stopped" : "running");
      if (action.status === "succeeded" && reached) return latest;
      await delay(options.pollIntervalMs ?? 1000, signal);
      latest = await client.get(accepted.id, { signal });
    }
  } catch (error) {
    if (options.signal?.aborted) throw options.signal.reason;
    if (timeout.aborted) throw fail("WAIT_TIMEOUT", "等待沙箱超时，后台动作仍可能继续。");
    throw error;
  }
}
