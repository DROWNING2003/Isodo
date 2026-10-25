import { EnvironmentId, ThreadId } from "@t3tools/contracts";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vite-plus/test";

const mocks = vi.hoisted(() => ({
  openExternal: vi.fn(async (_url: string): Promise<void> => undefined),
  navigate: vi.fn(async () => ({ _tag: "Success", value: {} })),
  applyPreviewServerSnapshot: vi.fn(),
  rememberPreviewUrl: vi.fn(),
  readPreparedConnection: vi.fn(() => ({
    httpBaseUrl: "https://8080-sandbox-1.e2b.example.com",
  })),
  recentlySeenUrls: ["http://localhost:5173/"],
  sessions: {} as Record<
    string,
    { navStatus: { _tag: "Idle" } | { _tag: "Success"; url: string } }
  >,
  chromeProps: null as {
    url: string;
    canGoBack: boolean;
    refreshDisabled: boolean;
    onBack: () => void;
    onRefresh: () => void;
    onSubmit: (url: string) => void;
    onOpenInBrowser?: () => void;
    openInBrowserInNavigation?: boolean;
  } | null,
  emptyStateProps: null as { onOpenUrl: (url: string) => void } | null,
}));

vi.mock("~/localApi", () => ({
  ensureLocalApi: () => ({ shell: { openExternal: mocks.openExternal } }),
}));

vi.mock("~/previewStateStore", () => ({
  applyPreviewServerSnapshot: mocks.applyPreviewServerSnapshot,
  rememberPreviewUrl: mocks.rememberPreviewUrl,
  useThreadPreviewState: () => ({
    recentlySeenUrls: mocks.recentlySeenUrls,
    sessions: mocks.sessions,
  }),
}));

vi.mock("~/state/preview", () => ({
  previewEnvironment: { navigate: {} },
}));

vi.mock("~/state/use-atom-command", () => ({
  useAtomCommand: () => mocks.navigate,
}));

vi.mock("~/state/session", () => ({
  readPreparedConnection: mocks.readPreparedConnection,
}));

vi.mock("./PreviewChromeRow", () => ({
  PreviewChromeRow: (props: typeof mocks.chromeProps) => {
    mocks.chromeProps = props;
    return null;
  },
}));

vi.mock("./PreviewEmptyState", () => ({
  PreviewEmptyState: (props: typeof mocks.emptyStateProps) => {
    mocks.emptyStateProps = props;
    return null;
  },
}));

vi.mock("./usePreviewSession", () => ({
  usePreviewSession: vi.fn(),
}));

import { SandboxPreviewPanel } from "./SandboxPreviewPanel";

const threadRef = {
  environmentId: EnvironmentId.make("environment-1"),
  threadId: ThreadId.make("thread-1"),
} as const;

describe("SandboxPreviewPanel", () => {
  beforeEach(() => {
    mocks.openExternal.mockClear();
    mocks.navigate.mockClear();
    mocks.applyPreviewServerSnapshot.mockClear();
    mocks.rememberPreviewUrl.mockClear();
    mocks.recentlySeenUrls = ["http://localhost:5173/"];
    mocks.sessions = {};
    mocks.chromeProps = null;
    mocks.emptyStateProps = null;
  });

  it("opens sandbox localhost previews in the current tab", async () => {
    const markup = renderToStaticMarkup(
      <SandboxPreviewPanel threadRef={threadRef} configuredUrls={[]} visible />,
    );

    expect(markup).toContain('data-sandbox-preview-frame="true"');
    expect(markup).toContain(
      'sandbox="allow-downloads allow-forms allow-modals allow-popups allow-same-origin allow-scripts"',
    );
    expect(mocks.chromeProps?.url).toBe("https://5173-sandbox-1.e2b.example.com/");
    expect(mocks.chromeProps?.canGoBack).toBe(true);
    expect(mocks.chromeProps?.refreshDisabled).toBe(false);
    expect(mocks.chromeProps?.onOpenInBrowser).toBeDefined();
    expect(mocks.chromeProps?.openInBrowserInNavigation).toBe(true);
    mocks.chromeProps?.onSubmit("http://localhost:5173/app");
    expect(mocks.openExternal).not.toHaveBeenCalled();
    mocks.chromeProps?.onOpenInBrowser?.();
    await vi.waitFor(() =>
      expect(mocks.openExternal).toHaveBeenNthCalledWith(
        1,
        "https://5173-sandbox-1.e2b.example.com/",
      ),
    );

    mocks.recentlySeenUrls = [];
    renderToStaticMarkup(<SandboxPreviewPanel threadRef={threadRef} configuredUrls={[]} visible />);
    expect(mocks.chromeProps?.canGoBack).toBe(false);
    expect(mocks.chromeProps?.refreshDisabled).toBe(true);
    mocks.emptyStateProps?.onOpenUrl("http://localhost:5173/dashboard");
    expect(mocks.openExternal).toHaveBeenCalledTimes(1);
    expect(mocks.rememberPreviewUrl).toHaveBeenNthCalledWith(
      1,
      threadRef,
      "https://5173-sandbox-1.e2b.example.com/app",
    );
  });

  it("keeps each browser tab on its own session URL", () => {
    mocks.sessions = {
      "tab-a": { navStatus: { _tag: "Success", url: "http://localhost:5173/a" } },
      "tab-b": { navStatus: { _tag: "Success", url: "http://localhost:5173/b" } },
    };

    renderToStaticMarkup(<SandboxPreviewPanel threadRef={threadRef} tabId="tab-a" visible />);
    expect(mocks.chromeProps?.url).toBe("https://5173-sandbox-1.e2b.example.com/a");

    renderToStaticMarkup(<SandboxPreviewPanel threadRef={threadRef} tabId="tab-b" visible />);
    expect(mocks.chromeProps?.url).toBe("https://5173-sandbox-1.e2b.example.com/b");
  });

  it("navigates only the active browser tab when selecting a server", async () => {
    mocks.sessions = {
      "tab-a": { navStatus: { _tag: "Idle" } },
      "tab-b": { navStatus: { _tag: "Idle" } },
    };

    renderToStaticMarkup(<SandboxPreviewPanel threadRef={threadRef} tabId="tab-a" visible />);
    await mocks.chromeProps?.onSubmit("http://localhost:5173/next");

    expect(mocks.navigate).toHaveBeenCalledWith({
      environmentId: threadRef.environmentId,
      input: {
        threadId: threadRef.threadId,
        tabId: "tab-a",
        url: "https://5173-sandbox-1.e2b.example.com/next",
      },
    });
    expect(mocks.openExternal).not.toHaveBeenCalled();
    expect(mocks.navigate).not.toHaveBeenCalledWith(
      expect.objectContaining({ input: expect.objectContaining({ tabId: "tab-b" }) }),
    );
  });
});
