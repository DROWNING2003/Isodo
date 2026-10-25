import { describe, expect, it } from "vite-plus/test";

import { shouldBundleCliDependency } from "./vite.config";

describe("CLI bundle dependencies", () => {
  it("bundles JavaScript runtime dependencies needed by the standalone sandbox server", () => {
    expect(shouldBundleCliDependency("@effect/platform-node/NodeRuntime")).toBe(true);
    expect(shouldBundleCliDependency("@opencode-ai/sdk/v2")).toBe(true);
    expect(shouldBundleCliDependency("@anthropic-ai/claude-agent-sdk")).toBe(true);
    expect(shouldBundleCliDependency("yaml")).toBe(true);
  });

  it("keeps native packages external", () => {
    expect(shouldBundleCliDependency("@ff-labs/fff-node")).toBe(false);
    expect(shouldBundleCliDependency("ffi-rs")).toBe(false);
    expect(shouldBundleCliDependency("node-pty")).toBe(false);
  });
});
