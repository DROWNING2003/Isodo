import {
  createLocalStorageCredentialStore,
  createLocalStorageInjectionStore,
  proxyCredentialStore,
} from "@t3tools/sandbox-client";
import type {
  CredentialStore,
  InjectionStore,
  SandboxCredentials,
  HttpInjection,
} from "@t3tools/sandbox-client";
import type { DesktopBridge } from "@t3tools/contracts";

let credStore: CredentialStore | null = null;
let injStore: InjectionStore | null = null;

function resolveCredentialStore(): CredentialStore {
  if (credStore) return credStore;
  const bridge = (globalThis as { desktopBridge?: DesktopBridge }).desktopBridge;
  credStore = bridge?.getSandboxCredentials
    ? {
        get: () => bridge.getSandboxCredentials!(),
        set: (creds) => bridge.setSandboxCredentials!(creds),
        clear: () => bridge.clearSandboxCredentials!(),
      }
    : createLocalStorageCredentialStore();
  return credStore;
}

export const sandboxCredentialStore: CredentialStore = {
  get: () => resolveCredentialStore().get(),
  set: (creds) => resolveCredentialStore().set(creds),
  clear: () => resolveCredentialStore().clear(),
};

function resolveInjectionStore(): InjectionStore {
  if (injStore) return injStore;
  injStore = createLocalStorageInjectionStore();
  return injStore;
}

export function getAdditionalInjections(): ReadonlyArray<HttpInjection> {
  return resolveInjectionStore().get();
}

export function setAdditionalInjections(injections: ReadonlyArray<HttpInjection>): void {
  resolveInjectionStore().set(injections);
}
