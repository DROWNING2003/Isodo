import { waitForSandboxAction } from "./sandboxClient";
import { sandboxClientFor } from "./sandboxTransports";

/** Wait for the accepted stop revision before closing the workspace transport. */
export async function pauseSandbox(sandboxId: string): Promise<void> {
  const client = sandboxClientFor(sandboxId.startsWith("local-") ? "local" : "cloud");
  const current = await client.get(sandboxId);
  if (current.observed_state === "stopped" && current.desired_state === "stopped") return;
  const accepted = await client.stop(sandboxId);
  await waitForSandboxAction(client, accepted, { timeoutMs: 60_000, pollIntervalMs: 500 });
}
