// Keep the gzip bytes behind an opaque extension. Static servers commonly add
// `Content-Encoding: gzip` to `.gz` paths, and browser fetch transparently
// decompresses that response before it reaches the sandbox uploader.
export const DEFAULT_T3_SERVER_BUNDLE_URL = "/t3-server-dist.bundle";

export async function fetchT3ServerBundle(
  url: string,
  fetchImpl: typeof fetch = fetch,
): Promise<Blob> {
  const normalizedUrl = url.trim();
  if (!normalizedUrl) {
    throw new Error("A T3 server bundle URL is required.");
  }

  const response = await fetchImpl(normalizedUrl, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(
      `The current T3 server bundle is unavailable (${response.status}). Build and publish t3-server-dist.bundle first.`,
    );
  }

  const bundle = await response.blob();
  if (bundle.size === 0) {
    throw new Error("The current T3 server bundle is empty. Build it again before connecting.");
  }
  return bundle;
}
