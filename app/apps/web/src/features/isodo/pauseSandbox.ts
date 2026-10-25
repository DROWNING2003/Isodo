import { getSandbox, stopSandbox } from "../../api/public.generated";
import type { LocalSandboxItem } from "./sandboxQueries";

/** Wait for the owner to report a stopped VM before closing its transport. */
export async function pauseSandbox(sandboxId: string): Promise<void> {
  const local = sandboxId.startsWith("local-");
  const bridge = window.isodoDesktop;
  if (local && !bridge) throw new Error("本地运行时不可用，未暂停沙箱。");
  const state = async () => {
    if (local) {
      const result = (await bridge!.listLocalSandboxes()) as { items?: LocalSandboxItem[] };
      return result.items?.find((item) => item.id === sandboxId)?.state;
    }
    return (await getSandbox(sandboxId)).observed_state;
  };
  const initial = await state();
  if (!initial) throw new Error("找不到沙箱，未执行暂停。");
  if (["stopped", "paused"].includes(initial)) return;
  if (local) await bridge!.stopLocalSandbox(sandboxId);
  else await stopSandbox(sandboxId);
  const deadline = Date.now() + 60_000;
  for (;;) {
    const current = await state();
    if (current === "stopped" || current === "paused") return;
    if (!current || ["failed", "error", "deleted"].includes(current))
      throw new Error("沙箱暂停失败，请检查运行状态。");
    if (Date.now() >= deadline) throw new Error("等待暂停超时，请刷新确认沙箱状态。");
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
}
