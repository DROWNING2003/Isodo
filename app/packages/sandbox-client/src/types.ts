import type {
  SandboxInfo,
  SandboxConnectionInfo,
  SandboxCreateInput,
  SandboxInjection,
  InjectionById,
  OpenaiInjection,
  AnthropicInjection,
  GeminiInjection,
  QiniuInjection,
  GithubInjection,
  HttpInjection,
  CodexProviderConfig,
  SandboxSkill,
} from "@t3tools/shared/sandbox";

import type { SandboxCredentials as ContractSandboxCredentials } from "@t3tools/contracts/sandbox";

export type {
  SandboxInfo,
  SandboxConnectionInfo,
  SandboxCreateInput,
  SandboxInjection,
  InjectionById,
  OpenaiInjection,
  AnthropicInjection,
  GeminiInjection,
  QiniuInjection,
  GithubInjection,
  HttpInjection,
  CodexProviderConfig,
  SandboxSkill,
};

export type SandboxCredentials = ContractSandboxCredentials;

export interface EnvdAccess {
  readonly sandboxID: string;
  readonly envdAccessToken?: string;
  readonly trafficAccessToken?: string | null;
}

export interface SandboxClientOptions {
  readonly apiKey: string;
  readonly apiUrl?: string;
  readonly fetchImpl?: typeof fetch;
}

export interface CredentialStore {
  get(): Promise<SandboxCredentials | null>;
  set(creds: SandboxCredentials): Promise<void>;
  clear(): Promise<void>;
}

export interface InjectionStore {
  get(): ReadonlyArray<HttpInjection>;
  set(injections: ReadonlyArray<HttpInjection>): void;
}

export interface T3StartOptions extends EnvdAccess {
  readonly domain: string;
  /** The build-time server bundle to install before starting T3. */
  readonly serverBundle: Blob;
  readonly codexProviders?: ReadonlyArray<CodexProviderConfig>;
  readonly skills?: ReadonlyArray<SandboxSkill>;
  readonly serverCommand?: string;
  readonly fetchImpl?: typeof fetch;
  readonly wait?: (ms: number) => Promise<void>;
}
