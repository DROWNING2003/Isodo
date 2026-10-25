import * as Schema from "effect/Schema";

export const SandboxCredentialsSchema = Schema.Struct({
  e2bApiKey: Schema.String,
  e2bApiUrl: Schema.String,
  openaiApiKey: Schema.String,
  openaiBaseUrl: Schema.String,
  templateID: Schema.String,
});
export type SandboxCredentials = typeof SandboxCredentialsSchema.Type;
