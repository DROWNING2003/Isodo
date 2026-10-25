import { envdFileUrl, envdHeaders } from "./envd";
import type { EnvdAccess } from "./types";

export const ENVD_FILE_UPLOAD_TIMEOUT_MS = 60_000;

export interface SandboxFileUploadOptions extends EnvdAccess {
  readonly domain: string;
  readonly path: string;
  readonly file: Blob;
  readonly fileName: string;
  readonly fetchImpl?: typeof fetch;
  readonly timeoutMs?: number;
}

export async function uploadSandboxFile(options: SandboxFileUploadOptions): Promise<void> {
  const fileName = options.fileName.trim();
  if (!fileName) {
    throw new Error("A file name is required for sandbox upload.");
  }

  const fileUrl = envdFileUrl(options.sandboxID, options.domain, options.path);
  const headers = envdHeaders(options);
  delete headers["Content-Type"];

  const body = new FormData();
  body.append("file", options.file, fileName);

  const fetchImpl = options.fetchImpl ?? fetch;
  const response = await fetchImpl(fileUrl, {
    method: "POST",
    headers,
    body,
    signal: AbortSignal.timeout(options.timeoutMs ?? ENVD_FILE_UPLOAD_TIMEOUT_MS),
  });
  if (response.ok) return;

  const detail = await response.text().catch(() => response.statusText);
  throw new Error(`${response.status} ${detail || response.statusText}`);
}
