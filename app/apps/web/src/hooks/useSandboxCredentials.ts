import type { SandboxCredentials } from "@t3tools/sandbox-client";
import {
  DEFAULT_OPENAI_BASE_URL,
  DEFAULT_SANDBOX_API_URL,
  DEFAULT_TEMPLATE_ID,
} from "@t3tools/sandbox-client";
import { useCallback, useEffect, useState } from "react";
import { sandboxCredentialStore } from "../lib/sandboxCredentialStore";

export const DEFAULT_CREDENTIALS: SandboxCredentials = {
  e2bApiKey: "",
  e2bApiUrl: DEFAULT_SANDBOX_API_URL,
  openaiApiKey: "",
  openaiBaseUrl: DEFAULT_OPENAI_BASE_URL,
  templateID: DEFAULT_TEMPLATE_ID,
};

export interface UseSandboxCredentialsResult {
  readonly credentials: SandboxCredentials;
  readonly isLoaded: boolean;
  readonly hasRequired: boolean;
  readonly save: (creds: SandboxCredentials) => Promise<void>;
  readonly clear: () => Promise<void>;
}

export function useSandboxCredentials(): UseSandboxCredentialsResult {
  const [credentials, setCredentials] = useState<SandboxCredentials>(DEFAULT_CREDENTIALS);
  const [isLoaded, setIsLoaded] = useState(false);

  useEffect(() => {
    sandboxCredentialStore
      .get()
      .then((saved) => {
        if (saved) setCredentials({ ...DEFAULT_CREDENTIALS, ...saved });
        setIsLoaded(true);
      })
      .catch(() => setIsLoaded(true));
  }, []);

  const save = useCallback(async (creds: SandboxCredentials) => {
    await sandboxCredentialStore.set(creds);
    setCredentials(creds);
  }, []);

  const clear = useCallback(async () => {
    await sandboxCredentialStore.clear();
    setCredentials(DEFAULT_CREDENTIALS);
  }, []);

  return {
    credentials,
    isLoaded,
    hasRequired: !!credentials.e2bApiKey,
    save,
    clear,
  };
}
