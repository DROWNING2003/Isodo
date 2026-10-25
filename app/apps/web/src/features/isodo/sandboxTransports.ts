import { isodoFetch } from "../../api/transport";
import { createSandboxClient, SandboxClientError, type SandboxClient } from "./sandboxClient";

export const remoteSandboxClient: SandboxClient = createSandboxClient(isodoFetch);
export const localSandboxClient: SandboxClient = createSandboxClient(async (path, init) => {
  const bridge = window.isodoDesktop;
  if (!bridge?.sandboxRequest)
    throw new SandboxClientError("LOCAL_UNAVAILABLE", "本地运行时不可用，请更新桌面应用。");
  init?.signal?.throwIfAborted();
  const result = await bridge.sandboxRequest(
    path,
    init?.method ?? "GET",
    typeof init?.body === "string" ? init.body : undefined,
    new Headers(init?.headers).get("Idempotency-Key") ?? undefined,
  );
  init?.signal?.throwIfAborted();
  return result;
});
export function sandboxClientFor(location: "local" | "cloud"): SandboxClient {
  return location === "local" ? localSandboxClient : remoteSandboxClient;
}
