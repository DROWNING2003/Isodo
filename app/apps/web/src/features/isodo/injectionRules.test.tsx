import { act } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vite-plus/test";
const fixture = vi.hoisted(() => ({
  client: { setQueryData: vi.fn(), invalidateQueries: vi.fn(() => new Promise(() => {})) },
}));
vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => fixture.client,
  useQuery: vi.fn(),
}));
vi.mock("./auth", () => ({ getAuthGeneration: () => 0, useAuth: vi.fn() }));
import { useSandboxInjectionWriter } from "./injectionRules";
let renderer: ReactTestRenderer;
afterEach(async () => {
  await act(async () => renderer?.unmount());
  vi.unstubAllGlobals();
});
it("finishes a successful write without waiting for a stalled background refresh", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const result = { rules: [], enabled: false, stats: { injected: 0 } };
  vi.stubGlobal("window", {
    isodoDesktop: { setSandboxInjections: vi.fn().mockResolvedValue(result) },
  });
  let write!: ReturnType<typeof useSandboxInjectionWriter>;
  function Harness() {
    write = useSandboxInjectionWriter("local-sandbox");
    return null;
  }
  await act(async () => {
    renderer = create(<Harness />);
  });
  expect(
    await Promise.race([
      write([]),
      new Promise((resolve) => setTimeout(() => resolve("stalled"), 100)),
    ]),
  ).toEqual(result);
  expect(fixture.client.setQueryData).toHaveBeenCalledWith(
    ["isodo", "sandbox-injections", "local-sandbox"],
    result,
  );
});
