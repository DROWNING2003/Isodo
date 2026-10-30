export function parseRemoteAPI(value: string, options?: { insecurePrivateHost?: string }): URL;
export function startDesktopServer(options: {
  staticDir: string;
  apiURL: string;
  port?: number;
  origin?: string;
  insecurePrivateHost?: string;
}): Promise<{ url: string; close(): Promise<void> }>;
