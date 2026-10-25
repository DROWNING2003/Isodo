import { describe, expect, it, vi } from "vite-plus/test";
import type { Sandbox } from "../../api/public.generated";
import { createSandboxClient, waitForSandboxAction } from "./sandboxClient";

const snapshot = (patch: Partial<Sandbox> = {}): Sandbox => ({
  id: "sandbox-1",
  name: "test",
  metadata: {},
  desired_state: "running",
  observed_state: "starting",
  created_at: "2026-10-25T00:00:00Z",
  updated_at: "2026-10-25T00:00:00Z",
  revision: "2",
  last_action: {
    revision: "2",
    type: "start",
    status: "running",
    updated_at: "2026-10-25T00:00:00Z",
  },
  ...patch,
});

describe("unified SandboxClient", () => {
  it("uses the same routes for either transport, without exposing operations", async () => {
    const request = vi.fn().mockResolvedValue(snapshot());
    const client = createSandboxClient(request);
    await client.get("sandbox-1");
    await client.start("sandbox-1");
    expect(request.mock.calls[0]?.[0]).toBe("/v1/sandboxes/sandbox-1");
    expect(request.mock.calls[1]?.[0]).toBe("/v1/sandboxes/sandbox-1/start");
    expect(request.mock.calls[1]?.[1].method).toBe("POST");
  });
  it("waits for the accepted revision's action, not merely running VM state", async () => {
    const accepted = snapshot({ observed_state: "running" });
    const completed = snapshot({
      observed_state: "running",
      last_action: { ...accepted.last_action!, status: "succeeded" },
    });
    const get = vi.fn().mockResolvedValue(completed);
    expect(await waitForSandboxAction({ get }, accepted, { pollIntervalMs: 0 })).toEqual(completed);
    expect(get).toHaveBeenCalledOnce();
  });
  it("rejects a newer action even when its VM happens to be running", async () => {
    const get = vi.fn().mockResolvedValue(snapshot({ revision: "3", observed_state: "running" }));
    await expect(
      waitForSandboxAction({ get }, snapshot(), { pollIntervalMs: 0 }),
    ).rejects.toMatchObject({ code: "ACTION_SUPERSEDED" });
  });
  it("reports action failure independently of the VM state", async () => {
    const initial = snapshot();
    const failed = snapshot({
      observed_state: "running",
      last_action: {
        ...initial.last_action!,
        status: "failed",
        error: { code: "STOP_FAILED", message: "still running", retryable: false },
      },
    });
    await expect(waitForSandboxAction({ get: vi.fn() }, failed)).rejects.toMatchObject({
      code: "STOP_FAILED",
      targetRevision: "2",
    });
  });
  it("does not issue another mutation when waiting times out", async () => {
    const get = vi.fn();
    await expect(waitForSandboxAction({ get }, snapshot(), { timeoutMs: 0 })).rejects.toMatchObject(
      { code: "WAIT_TIMEOUT", targetRevision: "2" },
    );
    expect(get).not.toHaveBeenCalled();
  });
});

it("waits for delete cleanup even when the VM is already stopped", async () => {
  const initial = snapshot({
    observed_state: "stopped",
    desired_state: "stopped",
    deletion: { requested_at: "2026-10-25T00:00:00Z" },
    last_action: {
      revision: "2",
      type: "delete",
      status: "succeeded",
      updated_at: "2026-10-25T00:00:00Z",
    },
  });
  const complete = {
    ...initial,
    deletion: { ...initial.deletion!, completed_at: "2026-10-25T00:00:01Z" },
  };
  const get = vi.fn().mockResolvedValue(complete);
  expect(await waitForSandboxAction({ get }, initial, { pollIntervalMs: 0 })).toEqual(complete);
  expect(get).toHaveBeenCalledOnce();
});
it("rejects an inconsistent action type at the same revision", async () => {
  const initial = snapshot();
  const invalid = snapshot({
    last_action: { ...initial.last_action!, type: "stop", status: "succeeded" },
    observed_state: "stopped",
  });
  await expect(
    waitForSandboxAction({ get: vi.fn().mockResolvedValue(invalid) }, initial, {
      pollIntervalMs: 0,
    }),
  ).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
});
