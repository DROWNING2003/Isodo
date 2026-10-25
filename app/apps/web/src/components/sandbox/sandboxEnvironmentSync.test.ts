import { afterEach, describe, expect, it, vi } from "vite-plus/test";

import type { EnvironmentId } from "@t3tools/contracts";
import type { SandboxInfo } from "@t3tools/shared/sandbox";

import { sandboxEnvironmentIdsToRemove, sandboxIdFromUrl } from "./sandboxEnvironmentSync";

afterEach(() => vi.unstubAllGlobals());

const environment = (environmentId: string, displayUrl: string) => ({
  environmentId: environmentId as EnvironmentId,
  displayUrl,
});

const sandbox = (sandboxID: string, state: SandboxInfo["state"]): SandboxInfo => ({
  sandboxID,
  domain: "e2b.example.com",
  state,
  templateID: "template",
  startedAt: "2026-01-01T00:00:00.000Z",
  endAt: "2026-01-02T00:00:00.000Z",
  cpuCount: 2,
  memoryMB: 512,
  diskSizeMB: 1024,
});

describe("sandboxEnvironmentSync", () => {
  it("extracts sandbox ids from proxied sandbox URLs", () => {
    expect(
      sandboxIdFromUrl("https://8080-sbx-abc-123.e2b.example.cn/.well-known/t3/environment"),
    ).toBe("sbx-abc-123");
    expect(sandboxIdFromUrl("https://example.com/.well-known/t3/environment")).toBeNull();
  });

  it("selects only paused sandbox environments", () => {
    const paused = environment(
      "paused-environment",
      "https://8080-sbx-paused.e2b.example.cn/.well-known/t3/environment",
    );
    const running = environment(
      "running-environment",
      "https://8080-sbx-running.e2b.example.cn/.well-known/t3/environment",
    );
    const local = environment("local-environment", "http://localhost:5733");

    expect(
      sandboxEnvironmentIdsToRemove(
        [paused, running, local],
        [sandbox("sbx-paused", "paused"), sandbox("sbx-running", "running")],
      ),
    ).toEqual(["paused-environment"]);
  });

  it("does not evict an Isodo pairing absent from the legacy sandbox API", () => {
    vi.stubGlobal("localStorage", {
      getItem: () => JSON.stringify({ "isodo-environment": "sbx-isodo" }),
    });
    const paired = environment(
      "isodo-environment",
      "https://8080-sbx-isodo.e2b.example.cn/.well-known/t3/environment",
    );
    expect(sandboxEnvironmentIdsToRemove([paired], [])).toEqual([]);
  });

  it("selects environments whose sandbox no longer exists", () => {
    const missing = environment(
      "missing-environment",
      "https://8080-sbx-missing.e2b.example.cn/.well-known/t3/environment",
    );

    expect(sandboxEnvironmentIdsToRemove([missing], [])).toEqual(["missing-environment"]);
  });
});
