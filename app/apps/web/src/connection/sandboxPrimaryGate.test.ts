import { describe, expect, it } from "@effect/vitest";

import { sandboxIdFromHostname, shouldProbeSandboxEnvironment } from "./sandboxPrimaryGate";

describe("sandbox primary gate", () => {
  it("extracts the sandbox id from a public sandbox hostname", () => {
    expect(sandboxIdFromHostname("8080-ibwyy0n4wkcgw12qaqezt.e2b.eirture.cn")).toBe(
      "ibwyy0n4wkcgw12qaqezt",
    );
    expect(sandboxIdFromHostname("localhost")).toBeNull();
  });

  it("allows descriptor discovery for a running sandbox", () => {
    expect(
      shouldProbeSandboxEnvironment("sandbox-1", [{ sandboxID: "sandbox-1", state: "running" }]),
    ).toBe(true);
  });

  it("skips descriptor discovery for a paused or missing sandbox", () => {
    expect(
      shouldProbeSandboxEnvironment("sandbox-1", [{ sandboxID: "sandbox-1", state: "paused" }]),
    ).toBe(false);
    expect(shouldProbeSandboxEnvironment("sandbox-1", [])).toBe(false);
  });

  it("does not gate non-sandbox origins", () => {
    expect(shouldProbeSandboxEnvironment(null, [])).toBe(true);
  });
});
