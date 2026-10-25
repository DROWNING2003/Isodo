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
      localRuntimeStatus: () => Promise<{ available: boolean; node?: unknown }>;
      createLocalSandbox: (input: { name: string; template?: string }) => Promise<unknown>;
      getLocalOperation: (operationID: string) => Promise<unknown>;
      getLocalSandboxApp: (sandboxID: string) => Promise<unknown>;
      listLocalSandboxes: () => Promise<unknown>;
      deleteLocalSandbox: (sandboxID: string) => Promise<unknown>;
      startLocalSandbox: (sandboxID: string) => Promise<unknown>;
      stopLocalSandbox: (sandboxID: string) => Promise<unknown>;
      listLocalTemplates: () => Promise<unknown>;
      buildLocalTemplate: (templateID: string) => Promise<unknown>;
      getSandboxInjections: (sandboxID: string) => Promise<unknown>;
      setSandboxInjections: (sandboxID: string, rules: readonly unknown[]) => Promise<unknown>;
      getInjectionSummary: () => Promise<unknown>;
      removePersistentInjection: (ruleID: string) => Promise<unknown>;
      writeAgentConfig: (
        sandboxID: string,
        input: { path: string; content: string },
      ) => Promise<unknown>;
    };
  }
}
