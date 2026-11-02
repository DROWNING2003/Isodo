import { act, type ReactNode } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { MCPFavoriteFields, MCPMarketPage } from "./MCPMarketPage";
const fixture = vi.hoisted(() => ({
  items: [] as {
    market_mcp_id: string;
    name: string;
    provider: string;
    description: string;
    endpoint: string;
    favorite: boolean;
    status: string;
    oauth_configured: boolean;
  }[],
  connect: vi.fn(),
  mutate: vi.fn(),
}));
vi.mock("./auth", () => ({
  getAuthGeneration: () => 0,
  useAuth: () => ({ status: "authenticated" }),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
}));
vi.mock("./mcpMarket", () => ({
  useMCPMarket: () => ({ data: { items: fixture.items }, isLoading: false, isError: false }),
  useMCPOAuth: () => ({ connect: fixture.connect, pending: false, error: null, cancel: vi.fn() }),
  mcpStatus: (s: string) => (s === "connected" ? "已连接" : "未连接"),
  mcpKey: ["test-mcp"],
  bindMCP: vi.fn(),
}));
vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
  useQuery: () => ({}),
}));
vi.mock("../../api/transport", () => ({
  isodoFetch: (...args: unknown[]) => fixture.mutate(...args),
}));
vi.mock("./CloudPages", () => ({
  CloudPage: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));
vi.mock("../../components/ui/button", () => ({
  Button: ({ children, ...props }: { children: ReactNode }) => (
    <button {...props}>{children}</button>
  ),
}));
let renderer: ReactTestRenderer;
afterEach(async () => {
  if (renderer) await act(async () => renderer.unmount());
  vi.clearAllMocks();
  fixture.items = [];
});
const github = {
  market_mcp_id: "github",
  name: "GitHub",
  provider: "GitHub",
  description: "GitHub 官方 MCP",
  endpoint: "https://api.githubcopilot.com/mcp/readonly",
  favorite: true,
  status: "connected",
  oauth_configured: true,
};
describe("MCP market separation", () => {
  it("renders favorites only, default unchecked, unconnected cannot select", async () => {
    fixture.items = [
      github,
      { ...github, market_mcp_id: "not-favorite", name: "Hidden", favorite: false },
    ];
    const change = vi.fn();
    await act(async () => {
      renderer = create(<MCPFavoriteFields value={[]} onChange={change} />);
    });
    const inputs = renderer.root.findAllByType("input");
    expect(inputs).toHaveLength(1);
    expect(inputs[0]!.props.checked).toBe(false);
    expect(inputs[0]!.props.disabled).toBe(false);
    fixture.items = [{ ...github, status: "disconnected" }];
    await act(async () => renderer.update(<MCPFavoriteFields value={[]} onChange={change} />));
    expect(renderer.root.findByType("input").props.disabled).toBe(true);
    expect(fixture.connect).not.toHaveBeenCalled();
  });
  it("favorite changes do not launch OAuth and empty favorites have a message", async () => {
    fixture.items = [{ ...github, favorite: false, status: "disconnected" }];
    await act(async () => {
      renderer = create(<MCPMarketPage />);
    });
    const button = renderer.root.findAllByType("button").find((b) => b.children.includes("收藏"))!;
    await act(async () => button.props.onClick());
    expect(fixture.mutate).toHaveBeenCalledWith("/v1/mcp/favorites/github", { method: "PUT" });
    expect(fixture.connect).not.toHaveBeenCalled();
    const favorites = renderer.root
      .findAllByType("button")
      .find((b) => b.children.includes("我的收藏"))!;
    await act(async () => favorites.props.onClick());
    expect(JSON.stringify(renderer.toJSON())).toContain("还没有收藏的 MCP");
  });
});
