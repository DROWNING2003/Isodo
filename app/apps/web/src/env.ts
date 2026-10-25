/**
 * True when running inside the Electron preload bridge, false in a regular browser.
 * The preload script sets window.isodoDesktop before any web-app code executes.
 */
export const isElectron =
  typeof window !== "undefined" &&
  // The bridge is the strongest signal; the user agent keeps window chrome and
  // layout correct even if a preload script failed to expose it.
  (window.isodoDesktop !== undefined || /\bElectron\//.test(navigator.userAgent));

declare global {
  interface Window {
    readonly isodoDesktop?: {
      sandboxRequest: (
        path: string,
        method: string,
        body?: string,
        idempotencyKey?: string,
      ) => Promise<unknown>;
      localRuntimeStatus: () => Promise<{ available: boolean; node?: unknown }>;
      listLocalTemplates: () => Promise<unknown>;
      buildLocalTemplate: (templateID: string) => Promise<unknown>;
      getSandboxInjections: (sandboxID: string) => Promise<unknown>;
      setSandboxInjections: (sandboxID: string, rules: readonly unknown[]) => Promise<unknown>;
      getInjectionSummary: () => Promise<unknown>;
      removePersistentInjection: (ruleID: string) => Promise<unknown>;
      beginFileUpload: (input: {
        sandboxId: string;
        path: string;
        size: number;
        overwrite?: boolean;
      }) => Promise<{ id: string; chunkBytes: number }>;
      appendFileUpload: (
        id: string,
        offset: number,
        data: Uint8Array,
      ) => Promise<{ received: number }>;
      finishFileUpload: (id: string) => Promise<{ path: string; bytes: number; sha256: string }>;
      abortFileUpload: (id: string) => Promise<void>;
    };
  }
}
