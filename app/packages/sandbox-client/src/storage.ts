import type { SandboxCredentials } from "@t3tools/contracts/sandbox";
import type { HttpInjection } from "@t3tools/shared/sandbox";
import type { CredentialStore, InjectionStore } from "./types";

export function createLocalStorageCredentialStore(prefix = "sandbox_"): CredentialStore {
  const keys = {
    e2bApiKey: `${prefix}e2b_api_key`,
    e2bApiUrl: `${prefix}e2b_api_url`,
    openaiApiKey: `${prefix}openai_api_key`,
    openaiBaseUrl: `${prefix}openai_base_url`,
    templateID: `${prefix}template_id`,
  } as const satisfies Record<keyof SandboxCredentials, string>;

  return {
    get: async () => {
      const raw: Record<string, string | null> = {};
      for (const [field, storageKey] of Object.entries(keys) as [
        keyof SandboxCredentials,
        string,
      ][]) {
        raw[field] = localStorage.getItem(storageKey);
      }
      return raw.e2bApiKey ? (raw as unknown as SandboxCredentials) : null;
    },
    set: async (creds) => {
      for (const [field, storageKey] of Object.entries(keys) as [
        keyof SandboxCredentials,
        string,
      ][]) {
        const value = creds[field];
        if (value) localStorage.setItem(storageKey, value);
        else localStorage.removeItem(storageKey);
      }
    },
    clear: async () => {
      for (const storageKey of Object.values(keys)) {
        localStorage.removeItem(storageKey);
      }
    },
  };
}

export function createLocalStorageInjectionStore(prefix = "sandbox_"): InjectionStore {
  const key = `${prefix}additional_injections`;
  return {
    get: () => {
      try {
        const raw = localStorage.getItem(key);
        return raw ? (JSON.parse(raw) as HttpInjection[]) : [];
      } catch {
        return [];
      }
    },
    set: (injections) => {
      if (injections.length > 0) localStorage.setItem(key, JSON.stringify(injections));
      else localStorage.removeItem(key);
    },
  };
}

export async function proxyCredentialStore(
  store: CredentialStore | null,
  bridge?: {
    getSandboxCredentials?: () => Promise<SandboxCredentials | null>;
    setSandboxCredentials?: (creds: SandboxCredentials) => Promise<void>;
    clearSandboxCredentials?: () => Promise<void>;
  },
): Promise<CredentialStore> {
  if (bridge?.getSandboxCredentials) {
    return {
      get: () => bridge.getSandboxCredentials!(),
      set: (creds) => bridge.setSandboxCredentials!(creds),
      clear: () => bridge.clearSandboxCredentials!(),
    };
  }
  return store ?? createLocalStorageCredentialStore();
}
