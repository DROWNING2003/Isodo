import { act, type ReactNode } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";
import { CustomMCPDialog } from "./CustomMCPDialog";
const fixture = vi.hoisted(() => ({
  create: vi.fn(),
  close: vi.fn(),
  created: vi.fn(),
  generation: 0,
}));
vi.mock("../../api/public.generated", () => ({
  createCustomMCP: (...args: unknown[]) => fixture.create(...args),
}));
vi.mock("./auth", () => ({ getAuthGeneration: () => fixture.generation }));
vi.mock("../../components/ui/input", () => ({
  Input: (props: React.ComponentProps<"input">) => <input {...props} />,
}));
vi.mock("../../components/ui/button", () => ({
  Button: (props: React.ComponentProps<"button">) => <button {...props} />,
}));
vi.mock("../../components/ui/dialog", () => {
  const Slot = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    Dialog: Slot,
    DialogPopup: Slot,
    DialogHeader: Slot,
    DialogTitle: Slot,
    DialogDescription: Slot,
    DialogPanel: Slot,
  };
});
let renderer: ReactTestRenderer;
beforeEach(async () => {
  vi.clearAllMocks();
  fixture.generation = 0;
  fixture.create.mockResolvedValue({
    market_mcp_id: "custom-test",
    name: "Docs",
    auth_kind: "none",
  });
  await act(async () => {
    renderer = create(<CustomMCPDialog onClose={fixture.close} onCreated={fixture.created} />);
  });
  const inputs = renderer.root.findAllByType("input");
  await act(async () => {
    inputs[0]!.props.onChange({ target: { value: "Docs" } });
    inputs[1]!.props.onChange({ target: { value: "https://mcp.example.org/mcp" } });
  });
});
afterEach(async () => {
  await act(async () => renderer.unmount());
});
const submit = () => renderer.root.findByType("form").props.onSubmit({ preventDefault: vi.fn() });
it("sends no token for an unauthenticated service", async () => {
  await act(async () => {
    submit();
  });
  expect(fixture.create.mock.calls[0]?.[0]).toEqual({
    name: "Docs",
    endpoint: "https://mcp.example.org/mcp",
    auth_kind: "none",
  });
  expect(fixture.created).toHaveBeenCalledOnce();
});
it("keeps Token in a password field and drops it when switching to no authentication", async () => {
  await act(async () =>
    renderer.root.findByType("select").props.onChange({ target: { value: "bearer" } }),
  );
  const password = renderer.root.findByProps({ type: "password" });
  expect(password.props.autoComplete).toBe("new-password");
  await act(async () => password.props.onChange({ target: { value: "fixture-only-token" } }));
  await act(async () =>
    renderer.root.findByType("select").props.onChange({ target: { value: "none" } }),
  );
  await act(async () => {
    submit();
  });
  expect(fixture.create.mock.calls[0]?.[0]).not.toHaveProperty("token");
});
it("rejects credentials embedded in the server URL before making a request", async () => {
  await act(async () =>
    renderer.root
      .findByProps({ type: "url" })
      .props.onChange({ target: { value: "https://user:secret@mcp.example.org/mcp" } }),
  );
  await act(async () => {
    submit();
  });
  expect(fixture.create).not.toHaveBeenCalled();
  expect(JSON.stringify(renderer.toJSON())).toContain("认证信息单独填写");
});
it("does not attach an old account response after the account changes", async () => {
  fixture.create.mockImplementation(async () => {
    fixture.generation++;
    return { market_mcp_id: "old-account-server" };
  });
  await act(async () => {
    submit();
  });
  expect(fixture.created).not.toHaveBeenCalled();
  expect(JSON.stringify(renderer.toJSON())).toContain("账号已切换");
});
