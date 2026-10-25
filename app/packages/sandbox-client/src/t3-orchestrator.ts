import {
  type CodexProviderConfig,
  type SandboxSkill,
  DEFAULT_T3_PORT,
  DEFAULT_CODEX_BASE_URL,
  sandboxUrl,
  buildCodexAuthJson,
  buildCodexConfigToml,
  buildSkillInstallCommands,
} from "@t3tools/shared/sandbox";
import { envdHeaders, envdHealthCheck, envdProcessStart, envdFileUrl, envdFileRead } from "./envd";
import { uploadSandboxFile } from "./files";
import type { T3StartOptions } from "./types";

const CODEX_AUTH_PATH = "/home/user/.codex/auth.json";
const CODEX_CONFIG_PATH = "/home/user/.codex/config.toml";
const MOBILE_PAIRING_PATH = "/home/user/.t3/mobile-pairing.json";
export const T3_STARTUP_LOG_PATH = "/home/user/.t3/startup.log";
const T3_STARTUP_LOG_MAX_CHARS = 2_000;
export const T3_SERVER_BUNDLE_FILE_NAME = "t3-server-dist.bundle";
export const T3_SERVER_BUNDLE_PATH = `/tmp/.t3-server-${T3_SERVER_BUNDLE_FILE_NAME}`;
const T3_SERVER_RUNTIME_DIR = "/tmp/.t3-server-runtime";
const T3_SERVER_RUNTIME_ENTRY = `${T3_SERVER_RUNTIME_DIR}/bin.mjs`;
export const T3_SERVER_RETRY_DELAY_MS = 3_000;
export const PAIRING_ATTEMPT_DELAY_MS = 5_000;
export const PAIRING_FILE_POLL_DELAY_MS = 1_000;
export const DEFAULT_T3_SERVER_COMMAND =
  'nohup node "$T3_SERVER_ENTRY" serve --port 8080 --host 0.0.0.0 --base-dir /home/user/.t3 --mode web';
const T3_SERVER_NO_STATIC_DIRECTORY_RESPONSE = "No static directory configured and no dev URL set.";

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\"'\"'")}'`;
}

const PROVIDER_PLACEHOLDERS: Record<string, string> = {
  OPENAI_API_KEY: "sk-sandbox-injection-placeholder",
  ANTHROPIC_API_KEY: "sk-ant-sandbox-injection-placeholder",
  GOOGLE_API_KEY: "AIza-sandbox-injection-placeholder",
  GROK_API_KEY: "xai-sandbox-injection-placeholder",
  MISTRAL_API_KEY: "mistral-sandbox-injection-placeholder",
  COHERE_API_KEY: "cohere-sandbox-injection-placeholder",
  DEEPSEEK_API_KEY: "sk-sandbox-injection-placeholder",
  XAI_API_KEY: "xai-sandbox-injection-placeholder",
};

function placeholderFor(envVar: string): string {
  return PROVIDER_PLACEHOLDERS[envVar] ?? `${envVar}-sandbox-injection-placeholder`;
}

const ALL_API_ENV_VARS = [
  "OPENAI_API_KEY",
  "ANTHROPIC_API_KEY",
  "GOOGLE_API_KEY",
  "GROK_API_KEY",
  "MISTRAL_API_KEY",
  "COHERE_API_KEY",
  "DEEPSEEK_API_KEY",
  "XAI_API_KEY",
];

const AGENT_PLACEHOLDERS: ReadonlyArray<{ readonly path: string; readonly content: string }> = [
  {
    path: "/home/user/.codex/auth.json",
    content: JSON.stringify(
      Object.fromEntries(ALL_API_ENV_VARS.map((v) => [v, placeholderFor(v)])),
    ),
  },
  {
    path: "/home/user/.claude/credentials.json",
    content: JSON.stringify({ primaryApiKey: "sk-ant-sandbox-injection-placeholder" }),
  },
  {
    path: "/home/user/.cursor/auth.json",
    content: JSON.stringify({ apiKey: "sk-sandbox-injection-placeholder" }),
  },
  {
    path: "/home/user/.opencode/config.json",
    content: JSON.stringify({ apiKey: "sk-sandbox-injection-placeholder" }),
  },
];

// Some sandbox images already contain these placeholder files. Keep this
// optional bootstrap disabled so connects do not rewrite them unnecessarily.
const ENABLE_LEGACY_AGENT_PLACEHOLDER_BOOTSTRAP = false;

function buildAgentPlaceholderCommands(): string[] {
  const cmds: string[] = [];
  for (const { path, content } of AGENT_PLACEHOLDERS) {
    const dir = path.slice(0, path.lastIndexOf("/"));
    cmds.push(
      `mkdir -p ${dir}`,
      `printf '%s\\n' ${shellQuote(content)} > ${path}.tmp`,
      `chmod 600 ${path}.tmp`,
      `if [ ! -s ${path} ]; then mv ${path}.tmp ${path}; chmod 600 ${path}; else rm -f ${path}.tmp; fi`,
    );
  }
  return cmds;
}

export function buildT3StartCommand(
  providers: ReadonlyArray<CodexProviderConfig>,
  skills: ReadonlyArray<SandboxSkill>,
  serverCommand: string,
): string {
  const lines = [
    "unset T3CODE_PAIRING_TOKEN",
    "export T3CODE_SANDBOX=1",
    "export HOME=/home/user T3CODE_HOME=/home/user/.t3 CODEX_HOME=/home/user/.codex",
    "mkdir -p /home/user/.t3 /home/user/.codex",
    ...buildSkillInstallCommands(skills),
    `T3_SERVER_BUNDLE="${T3_SERVER_BUNDLE_PATH}"`,
    `T3_SERVER_RUNTIME_DIR="${T3_SERVER_RUNTIME_DIR}"`,
    'T3_SERVER_RUNTIME_ENTRY="$T3_SERVER_RUNTIME_DIR/bin.mjs"',
    'T3_SERVER_ENTRY="$T3_SERVER_RUNTIME_ENTRY"',
    'if [ ! -s "$T3_SERVER_BUNDLE" ]; then echo "T3 server bundle is missing" >&2; exit 1; fi',
    'T3_SERVER_RUNTIME_STAGING="${T3_SERVER_RUNTIME_DIR}.staging"',
    'rm -rf "$T3_SERVER_RUNTIME_STAGING"',
    'mkdir -p "$T3_SERVER_RUNTIME_STAGING"',
    'if ! tar -xzf "$T3_SERVER_BUNDLE" --strip-components=1 -C "$T3_SERVER_RUNTIME_STAGING"; then',
    '  echo "Could not extract the T3 server bundle" >&2',
    '  rm -rf "$T3_SERVER_RUNTIME_STAGING"',
    "  exit 1",
    "fi",
    'case "$(uname -m)" in',
    "  aarch64|arm64) T3_SERVER_ARCH=arm64 ;;",
    "  x86_64|amd64) T3_SERVER_ARCH=x64 ;;",
    '  *) echo "Unsupported T3 server architecture: $(uname -m)" >&2; rm -rf "$T3_SERVER_RUNTIME_STAGING"; exit 1 ;;',
    "esac",
    'T3_FFF_LIBRARY="$T3_SERVER_RUNTIME_STAGING/node_modules/@ff-labs/fff-bin-linux-${T3_SERVER_ARCH}-gnu/libfff_c.so"',
    'if [ -s "$T3_FFF_LIBRARY.gz" ]; then',
    '  if ! gzip -d "$T3_FFF_LIBRARY.gz"; then',
    '    echo "Could not decompress the T3 file-search native library" >&2',
    '    rm -rf "$T3_SERVER_RUNTIME_STAGING"',
    "    exit 1",
    "  fi",
    "fi",
    'if [ ! -s "$T3_SERVER_RUNTIME_STAGING/bin.mjs" ]; then',
    '  echo "The T3 server bundle does not contain dist/bin.mjs" >&2',
    '  rm -rf "$T3_SERVER_RUNTIME_STAGING"',
    "  exit 1",
    "fi",
    'rm -rf "$T3_SERVER_RUNTIME_DIR"',
    'mv "$T3_SERVER_RUNTIME_STAGING" "$T3_SERVER_RUNTIME_DIR"',
  ];

  const authJson = buildCodexAuthJson(
    providers.length > 0
      ? providers
      : ALL_API_ENV_VARS.map(
          (envVar) =>
            ({
              name: envVar,
              baseUrl: "",
              envVar,
              configKey: "",
            }) as CodexProviderConfig,
        ),
  );
  const configToml = buildCodexConfigToml(
    providers.length > 0
      ? providers
      : [
          {
            name: "OpenAI" as const,
            baseUrl: DEFAULT_CODEX_BASE_URL,
            envVar: "OPENAI_API_KEY",
            configKey: "requires_openai_auth" as const,
          },
        ],
  );
  lines.push(
    `printf '%s\\n' ${shellQuote(authJson)} > ${CODEX_AUTH_PATH}.tmp`,
    `chmod 600 ${CODEX_AUTH_PATH}.tmp`,
    `if ! cmp -s ${CODEX_AUTH_PATH}.tmp ${CODEX_AUTH_PATH}; then`,
    `  mv ${CODEX_AUTH_PATH}.tmp ${CODEX_AUTH_PATH}`,
    `  chmod 600 ${CODEX_AUTH_PATH}`,
    "else",
    `  rm -f ${CODEX_AUTH_PATH}.tmp`,
    "fi",
    `chmod 600 ${CODEX_AUTH_PATH}`,
    `printf '%s' ${shellQuote(configToml)} > ${CODEX_CONFIG_PATH}.tmp`,
    `chmod 600 ${CODEX_CONFIG_PATH}.tmp`,
    `if ! cmp -s ${CODEX_CONFIG_PATH}.tmp ${CODEX_CONFIG_PATH}; then`,
    `  mv ${CODEX_CONFIG_PATH}.tmp ${CODEX_CONFIG_PATH}`,
    `  chmod 600 ${CODEX_CONFIG_PATH}`,
    "else",
    `  rm -f ${CODEX_CONFIG_PATH}.tmp`,
    "fi",
  );

  if (ENABLE_LEGACY_AGENT_PLACEHOLDER_BOOTSTRAP) {
    lines.push(...buildAgentPlaceholderCommands());
  }
  for (const v of ALL_API_ENV_VARS) {
    const val = placeholderFor(v);
    lines.push(`export ${v}="\${${v}:-${val}}"`);
  }

  lines.push(
    "if command -v fuser >/dev/null 2>&1; then fuser -k 8080/tcp >/dev/null 2>&1 || true; fi",
    // The minimal sandbox template does not necessarily include procps/pgrep.
    // Inspect /proc instead so reconnects can still replace a server started by
    // the image entrypoint rather than silently starting a second copy.
    'RUNNING_PID=""',
    "for PROC_DIR in /proc/[0-9]*; do",
    '  PID="${PROC_DIR##*/}"',
    '  [ "$PID" = "$$" ] && continue',
    '  if [ -r "$PROC_DIR/cmdline" ]; then',
    "    CMDLINE=\"$(tr '\\0' ' ' < \"$PROC_DIR/cmdline\" 2>/dev/null || true)\"",
    '    case "$CMDLINE" in',
    '      *"bin.mjs serve --port 8080"*) RUNNING_PID="$PID"; break ;;',
    "    esac",
    "  fi",
    "done",
    'if [ -n "$RUNNING_PID" ]; then',
    '  kill "$RUNNING_PID"',
    "  WAIT_ATTEMPTS=0",
    '  while kill -0 "$RUNNING_PID" 2>/dev/null && [ "$WAIT_ATTEMPTS" -lt 50 ]; do',
    "    sleep 0.1",
    "    WAIT_ATTEMPTS=$((WAIT_ATTEMPTS + 1))",
    "  done",
    '  if kill -0 "$RUNNING_PID" 2>/dev/null; then',
    '    kill -KILL "$RUNNING_PID"',
    "    WAIT_ATTEMPTS=0",
    '    while kill -0 "$RUNNING_PID" 2>/dev/null && [ "$WAIT_ATTEMPTS" -lt 20 ]; do',
    "      sleep 0.1",
    "      WAIT_ATTEMPTS=$((WAIT_ATTEMPTS + 1))",
    "    done",
    "  fi",
    '  if kill -0 "$RUNNING_PID" 2>/dev/null; then exit 1; fi',
    "fi",
    `${serverCommand} > ${T3_STARTUP_LOG_PATH} 2>&1 </dev/null &`,
  );
  return lines.join("\n");
}

export async function startT3Server(options: T3StartOptions): Promise<void> {
  const envdBase = sandboxUrl(options.sandboxID, options.domain, 49983);
  if (!envdBase) throw new Error("The sandbox envd URL could not be resolved.");

  const fetchImpl = options.fetchImpl ?? fetch;
  const wait = options.wait ?? ((ms: number) => new Promise((r) => setTimeout(r, ms)));
  const headers = envdHeaders(options);

  const healthy = await envdHealthCheck(envdBase, headers, { fetchImpl, wait });
  if (!healthy) throw new Error("envd is not ready");

  await uploadSandboxFile({
    sandboxID: options.sandboxID,
    domain: options.domain,
    path: T3_SERVER_BUNDLE_PATH,
    file: options.serverBundle,
    fileName: T3_SERVER_BUNDLE_FILE_NAME,
    fetchImpl,
    ...(options.envdAccessToken !== undefined ? { envdAccessToken: options.envdAccessToken } : {}),
    ...(options.trafficAccessToken !== undefined
      ? { trafficAccessToken: options.trafficAccessToken }
      : {}),
  });

  const serverCommand = options.serverCommand ?? DEFAULT_T3_SERVER_COMMAND;

  const command = buildT3StartCommand(
    options.codexProviders ?? [],
    options.skills ?? [],
    serverCommand,
  );

  const started = await envdProcessStart(envdBase, headers, command, { fetchImpl });
  if (!started.ok) {
    throw new Error(`${started.status} ${await started.text().catch(() => started.statusText)}`);
  }
}

export async function waitForT3Server(
  serverUrl: string,
  options?: {
    readonly fetchImpl?: typeof fetch;
    readonly retryCount?: number;
    readonly retryDelayMs?: number;
    readonly wait?: (ms: number) => Promise<void>;
  },
): Promise<void> {
  const fetchImpl = options?.fetchImpl ?? fetch;
  const retries = options?.retryCount ?? 30;
  const retryDelay = options?.retryDelayMs ?? T3_SERVER_RETRY_DELAY_MS;
  const wait = options?.wait ?? ((ms: number) => new Promise((r) => setTimeout(r, ms)));
  for (let i = 0; i < retries; i++) {
    try {
      const res = await fetchImpl(serverUrl, { signal: AbortSignal.timeout(3000) });
      if (res.ok) return;
      if (res.status === 503) {
        const body = await res.text().catch(() => "");
        if (body.trim() === T3_SERVER_NO_STATIC_DIRECTORY_RESPONSE) return;
      }
    } catch {}
    if (i + 1 < retries) await wait(retryDelay);
  }
  throw new Error(`The sandbox service did not become ready at ${serverUrl}`);
}

async function readT3StartupLog(
  sandboxID: string,
  domain: string,
  headers: Record<string, string>,
  fetchImpl: typeof fetch,
): Promise<string | null> {
  try {
    const response = await envdFileRead(
      envdFileUrl(sandboxID, domain, T3_STARTUP_LOG_PATH),
      headers,
      { fetchImpl, retryCount: 1 },
    );
    if (!response) return null;
    const text = (await response.text()).trim();
    if (!text) return null;
    return text.length > T3_STARTUP_LOG_MAX_CHARS
      ? `…${text.slice(-T3_STARTUP_LOG_MAX_CHARS)}`
      : text;
  } catch {
    return null;
  }
}

export function buildT3PairingCommand(serverUrl: string): string {
  return [
    "set -eu",
    `rm -f ${MOBILE_PAIRING_PATH} ${MOBILE_PAIRING_PATH}.tmp`,
    `T3_SERVER_RUNTIME_ENTRY="${T3_SERVER_RUNTIME_ENTRY}"`,
    'T3_SERVER_ENTRY="$T3_SERVER_RUNTIME_ENTRY"',
    'if [ ! -s "$T3_SERVER_ENTRY" ]; then echo "T3 server runtime is missing" >&2; exit 1; fi',
    `node "$T3_SERVER_ENTRY" auth pairing create --base-dir /home/user/.t3 --base-url '${serverUrl}' --ttl 15m --label mobile-sandbox --json > ${MOBILE_PAIRING_PATH}.tmp`,
    `mv ${MOBILE_PAIRING_PATH}.tmp ${MOBILE_PAIRING_PATH}`,
  ].join("\n");
}

export function readPairingUrlFromJson(value: string, serverUrl: string): string | null {
  try {
    const parsed = JSON.parse(value) as { readonly pairUrl?: unknown };
    if (typeof parsed.pairUrl !== "string") return null;
    const expected = new URL(serverUrl);
    const actual = new URL(parsed.pairUrl);
    if (
      actual.origin !== expected.origin ||
      actual.pathname !== "/pair" ||
      !new URLSearchParams(actual.hash.slice(1)).get("token")
    )
      return null;
    return actual.toString();
  } catch {
    return null;
  }
}

export async function getT3PairingUrl(
  sandboxID: string,
  domain: string,
  access?: { readonly envdAccessToken?: string; readonly trafficAccessToken?: string | null },
  options?: {
    readonly fetchImpl?: typeof fetch;
    readonly pairingCommand?: string;
    readonly attemptDelayMs?: number;
    readonly pairingPollDelayMs?: number;
    readonly serverRetryCount?: number;
    readonly serverRetryDelayMs?: number;
    readonly wait?: (ms: number) => Promise<void>;
  },
): Promise<string> {
  const serverUrl = sandboxUrl(sandboxID, domain, DEFAULT_T3_PORT);
  if (!serverUrl) throw new Error("Could not resolve the sandbox service URL.");

  const envdBase = sandboxUrl(sandboxID, domain, 49983);
  if (!envdBase) throw new Error("The sandbox envd URL could not be resolved.");

  const headers = envdHeaders({ sandboxID, ...access });
  const fetchImpl = options?.fetchImpl ?? fetch;

  const fileUrl = envdFileUrl(sandboxID, domain, MOBILE_PAIRING_PATH);
  try {
    await waitForT3Server(serverUrl, {
      fetchImpl,
      ...(options?.serverRetryCount !== undefined ? { retryCount: options.serverRetryCount } : {}),
      ...(options?.serverRetryDelayMs !== undefined
        ? { retryDelayMs: options.serverRetryDelayMs }
        : {}),
      ...(options?.wait !== undefined ? { wait: options.wait } : {}),
    });
  } catch (error) {
    const startupLog = await readT3StartupLog(sandboxID, domain, headers, fetchImpl);
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(startupLog ? `${message}. T3 startup log: ${startupLog}` : message);
  }

  const pairingCmd = options?.pairingCommand ?? buildT3PairingCommand(serverUrl);
  const attemptDelay = options?.attemptDelayMs ?? PAIRING_ATTEMPT_DELAY_MS;
  const pairingPollDelay = options?.pairingPollDelayMs ?? PAIRING_FILE_POLL_DELAY_MS;
  for (let attempt = 0; attempt < 6; attempt++) {
    if (attempt > 0) await new Promise((r) => setTimeout(r, attemptDelay));
    const issued = await envdProcessStart(envdBase, headers, pairingCmd, { fetchImpl });
    if (!issued.ok) continue;

    for (let i = 0; i < 15; i++) {
      if (i > 0) await new Promise((r) => setTimeout(r, pairingPollDelay));
      const pairFile = await envdFileRead(fileUrl, headers, { fetchImpl, retryCount: 1 });
      if (!pairFile) continue;
      const text = await pairFile.text();
      const url = readPairingUrlFromJson(text, serverUrl);
      if (url) return url;
    }
  }
  throw new Error(`The sandbox service did not issue a fresh pairing code at ${serverUrl}.`);
}
