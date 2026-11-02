import { act, create } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useMCPOAuth } from "./mcpMarket";

const mocks = vi.hoisted(() => ({ fetch: vi.fn(), external: vi.fn(), invalidate: vi.fn() }));
vi.mock("../../api/transport", () => ({ isodoFetch: mocks.fetch }));
vi.mock("../../localApi", () => ({
  ensureLocalApi: () => ({ shell: { openExternal: mocks.external } }),
}));
vi.mock("./auth", () => ({
  getAuthGeneration: () => 0,
  useAuth: () => ({ status: "authenticated" }),
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: vi.fn(),
  useQueryClient: () => ({ invalidateQueries: mocks.invalidate }),
}));
let oauth: ReturnType<typeof useMCPOAuth>;
let renderer: ReturnType<typeof create> | undefined;
function Probe() {
  oauth = useMCPOAuth();
  return null;
}
const authorizationURL = "https://github.com/login/oauth/authorize?client_id=test";
describe("GitHub MCP OAuth browser selection", () => {
  beforeEach(async () => {
    vi.useFakeTimers();
    mocks.fetch.mockImplementation(async (path: string) =>
      path.endsWith("/authorize")
        ? { transaction_id: "test", authorization_url: authorizationURL }
        : { status: "connected" },
    );
    await act(async () => {
      renderer = create(<Probe />);
    });
  });
  afterEach(async () => {
    await act(async () => {
      renderer?.unmount();
    });
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.resetAllMocks();
  });
  it("reserves a browser popup before asynchronous discovery and closes after connection", async () => {
    const popup = { opener: {}, location: { replace: vi.fn() }, close: vi.fn() };
    const open = vi.fn(() => popup);
    vi.stubGlobal("window", { open });
    let connection: Promise<void>;
    await act(async () => {
      connection = oauth.connect();
    });
    expect(open).toHaveBeenCalledWith("about:blank", "_blank");
    expect(open.mock.invocationCallOrder[0]).toBeLessThan(mocks.fetch.mock.invocationCallOrder[0]!);
    expect(popup.opener).toBeNull();
    expect(popup.location.replace).toHaveBeenCalledWith(authorizationURL);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
      await connection!;
    });
    expect(oauth.error).toBeNull();
    expect(oauth.pending).toBe(false);
    expect(popup.close).toHaveBeenCalledOnce();
    expect(mocks.external).not.toHaveBeenCalled();
  });
  it("does not create an OAuth transaction when the popup is blocked", async () => {
    vi.stubGlobal("window", { open: vi.fn(() => null) });
    await act(async () => {
      await oauth.connect();
    });
    expect(mocks.fetch).not.toHaveBeenCalled();
    expect(oauth.error).toContain("弹窗");
  });
  it("keeps Electron authorization in the external browser", async () => {
    const open = vi.fn();
    vi.stubGlobal("window", { desktopBridge: {}, open });
    let connection: Promise<void>;
    await act(async () => {
      connection = oauth.connect();
    });
    expect(mocks.external).toHaveBeenCalledWith(authorizationURL);
    expect(open).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
      await connection!;
    });
    expect(oauth.error).toBeNull();
  });
});
