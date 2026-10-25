export type SandboxTemplate = "dev" | "daily" | "blank";
export type Agent = "Claude Code" | "Codex" | "dsh" | "OpenCode";
export type PermissionMode = "supervised" | "accept-edits" | "auto" | "full";
export interface CloudFile {
  readonly path: string;
  readonly content: string;
}
export interface CloudSandbox {
  readonly id: string;
  readonly name: string;
  readonly template: SandboxTemplate;
  readonly status: "running" | "paused";
  readonly cpu: number;
  readonly memoryGB: number;
  readonly diskGB: number;
  readonly connectionIds: readonly string[];
  readonly files: readonly CloudFile[];
}
export interface CloudTask {
  readonly id: string;
  readonly sandboxId: string;
  readonly title: string;
  readonly agent: Agent;
  readonly mode: PermissionMode;
  readonly status: "approval" | "completed" | "cancelled";
  readonly output: string;
  readonly artifact: CloudFile | null;
}
export interface CredentialReference {
  readonly id: string;
  readonly service: string;
  readonly label: string;
}
export interface CloudSnapshot {
  readonly id: string;
  readonly name: string;
  readonly sourceSandboxId: string;
  readonly template: SandboxTemplate;
  readonly cpu: number;
  readonly memoryGB: number;
  readonly diskGB: number;
  readonly files: readonly CloudFile[];
}
export type CloudApiKeyScope = "sandboxes:read" | "sandboxes:write";
export interface CloudApiKey {
  readonly id: string;
  readonly name: string;
  readonly prefix: string;
  readonly scopes: readonly CloudApiKeyScope[];
  readonly createdAt: string;
  readonly expiresAt: string | null;
  readonly lastUsedAt: string | null;
  readonly revokedAt: string | null;
}
export interface CreatedCloudApiKey {
  readonly apiKey: CloudApiKey;
  /** Demo-only secret, returned once and never stored in CloudState. */
  readonly secret: string;
}
export interface CloudState {
  readonly sandboxes: readonly CloudSandbox[];
  readonly tasks: readonly CloudTask[];
  readonly credentials: readonly CredentialReference[];
  readonly snapshots: readonly CloudSnapshot[];
  readonly apiKeys: readonly CloudApiKey[];
}
/** Control-plane seam. A future service returns authorized sandbox runtime endpoints. */
export interface CloudSandboxService {
  readonly mode: "mock" | "remote";
  readonly getSnapshot: () => CloudState;
  readonly subscribe: (listener: () => void) => () => void;
  readonly createSandbox: (input: { name: string; template: SandboxTemplate }) => CloudSandbox;
  readonly setStatus: (id: string, status: CloudSandbox["status"]) => void;
  readonly createTask: (input: {
    sandboxId: string;
    prompt: string;
    agent: Agent;
    mode: PermissionMode;
  }) => CloudTask;
  readonly resolveTask: (id: string, allow: boolean) => void;
  readonly setConnection: (sandboxId: string, credentialId: string, granted: boolean) => void;
  readonly takeSnapshot: (sandboxId: string) => CloudSnapshot;
  readonly restoreSnapshot: (snapshotId: string) => CloudSandbox;
  readonly createApiKey: (input: {
    name: string;
    scopes: readonly CloudApiKeyScope[];
    expiresInDays?: number | null;
  }) => CreatedCloudApiKey;
  readonly revokeApiKey: (id: string) => void;
}
