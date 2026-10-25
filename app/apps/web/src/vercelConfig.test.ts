import { afterEach, describe, expect, it, vi } from "vite-plus/test";

afterEach(() => {
  vi.unstubAllEnvs();
  vi.resetModules();
});

describe("Vercel configuration", () => {
  it("enables Git deployments for the fork integration branch", async () => {
    const { config } = await import("../vercel");

    expect(config.git?.deploymentEnabled).toEqual({
      "personal/*": true,
      "*": false,
    });
  });

  it("keeps non-fork branches opt-in", async () => {
    const { config } = await import("../vercel");

    expect(config.git?.deploymentEnabled).toMatchObject({ "*": false });
  });
});
