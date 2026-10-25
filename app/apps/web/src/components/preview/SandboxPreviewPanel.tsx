import type { ScopedThreadRef } from "@t3tools/contracts";
import { useCallback, useEffect, useMemo, useState } from "react";

import { ensureLocalApi } from "~/localApi";
import { resolveDiscoveredServerUrl } from "~/browser/browserTargetResolver";
import {
  applyPreviewServerSnapshot,
  rememberPreviewUrl,
  useThreadPreviewState,
} from "~/previewStateStore";
import { previewEnvironment } from "~/state/preview";
import { useAtomCommand } from "~/state/use-atom-command";
import {
  BROWSER_HISTORY_MAX_ENTRIES_PER_PROJECT,
  removeUrlForThread,
  useThreadRecentHistory,
} from "~/browserHistoryStore";

import { PreviewChromeRow } from "./PreviewChromeRow";
import { PreviewEmptyState } from "./PreviewEmptyState";
import { subscribePreviewAction } from "./previewActionBus";
import { usePreviewSession } from "./usePreviewSession";

interface Props {
  readonly threadRef: ScopedThreadRef;
  readonly tabId?: string | null | undefined;
  readonly configuredUrls?: ReadonlyArray<string> | undefined;
  readonly visible: boolean;
}

/**
 * The sandbox web surface has no Electron webview. Keep the browser surface
 * useful by resolving sandbox ports to their public proxy URL inside an iframe.
 * The user can still send the current URL to the system browser explicitly.
 */
export function SandboxPreviewPanel({ threadRef, tabId, configuredUrls, visible }: Props) {
  const [refreshNonces, setRefreshNonces] = useState<Record<string, number>>({});
  const [focusUrlNonce, setFocusUrlNonce] = useState<number | undefined>(undefined);
  const [selectionTabKey, setSelectionTabKey] = useState<string | null>(null);
  const previewState = useThreadPreviewState(threadRef);
  const recentHistoryEntries = useThreadRecentHistory(
    threadRef,
    BROWSER_HISTORY_MAX_ENTRIES_PER_PROJECT,
  );
  usePreviewSession(threadRef);
  const navigate = useAtomCommand(previewEnvironment.navigate, "sandbox preview navigate");
  const tabKey = tabId ?? "new";
  const refreshNonce = refreshNonces[tabKey] ?? 0;
  const sessionUrl = useMemo(() => {
    if (!tabId) return "";
    const snapshot = previewState.sessions[tabId];
    return snapshot && snapshot.navStatus._tag !== "Idle" ? snapshot.navStatus.url : "";
  }, [previewState.sessions, tabId]);
  const currentUrl = useMemo(() => {
    const recentUrl = tabId ? sessionUrl : (previewState.recentlySeenUrls[0] ?? "");
    if (recentUrl.length === 0) return "";
    try {
      return resolveDiscoveredServerUrl(threadRef.environmentId, recentUrl);
    } catch {
      return recentUrl;
    }
  }, [previewState.recentlySeenUrls, sessionUrl, tabId, threadRef.environmentId]);

  const openPublicPreview = useCallback(
    async (rawUrl: string) => {
      let resolvedUrl = rawUrl;
      try {
        resolvedUrl = resolveDiscoveredServerUrl(threadRef.environmentId, rawUrl);
      } catch {
        // Keep the regular browser's URL validation as the final fallback.
      }
      if (tabId) {
        const result = await navigate({
          environmentId: threadRef.environmentId,
          input: {
            threadId: threadRef.threadId,
            tabId,
            url: resolvedUrl,
          },
        });
        if (result._tag === "Failure") return;
        applyPreviewServerSnapshot(threadRef, result.value);
      }
      setSelectionTabKey((current) => (current === tabKey ? null : current));
      rememberPreviewUrl(threadRef, resolvedUrl);
    },
    [navigate, tabId, tabKey, threadRef],
  );

  const openCurrentPreview = useCallback(() => {
    if (currentUrl.length > 0) {
      void ensureLocalApi()
        .shell.openExternal(currentUrl)
        .catch(() => undefined);
    }
  }, [currentUrl]);

  const handleRefresh = useCallback(() => {
    setRefreshNonces((current) => ({ ...current, [tabKey]: (current[tabKey] ?? 0) + 1 }));
  }, [tabKey]);

  const handleShowServerSelection = useCallback(() => {
    setSelectionTabKey(tabKey);
  }, [tabKey]);

  useEffect(() => {
    if (!visible) return;
    return subscribePreviewAction((action) => {
      if (action === "refresh") {
        handleRefresh();
      } else if (action === "focus-url") {
        setFocusUrlNonce((value) => (value ?? 0) + 1);
      }
    });
  }, [handleRefresh, visible]);

  const showServerSelection = selectionTabKey === tabKey;

  const previewContent =
    !showServerSelection && currentUrl.length > 0 ? (
      <iframe
        key={`${tabKey}:${currentUrl}:${refreshNonce}`}
        src={currentUrl}
        title="Sandbox public preview"
        className="h-full w-full border-0 bg-background"
        referrerPolicy="no-referrer"
        sandbox="allow-downloads allow-forms allow-modals allow-popups allow-same-origin allow-scripts"
        data-sandbox-preview-frame="true"
      />
    ) : (
      <PreviewEmptyState
        threadRef={threadRef}
        environmentId={threadRef.environmentId}
        configuredUrls={configuredUrls}
        recentEntries={recentHistoryEntries}
        onRemoveRecent={(url) => removeUrlForThread(threadRef, url)}
        onOpenUrl={openPublicPreview}
      />
    );

  return (
    <div
      className="flex min-h-0 flex-1 flex-col bg-background"
      data-preview-visible={visible ? "true" : "false"}
    >
      <PreviewChromeRow
        url={showServerSelection ? "" : currentUrl}
        displayUrl={!showServerSelection && currentUrl ? currentUrl : undefined}
        loading={false}
        canGoBack={!showServerSelection && currentUrl.length > 0}
        canGoForward={false}
        refreshDisabled={showServerSelection || currentUrl.length === 0}
        focusUrlNonce={focusUrlNonce}
        onBack={handleShowServerSelection}
        onForward={() => undefined}
        onRefresh={handleRefresh}
        onSubmit={openPublicPreview}
        onOpenInBrowser={
          !showServerSelection && currentUrl.length > 0 ? openCurrentPreview : undefined
        }
      />
      <div className="relative min-h-0 flex-1 overflow-hidden">{previewContent}</div>
    </div>
  );
}
