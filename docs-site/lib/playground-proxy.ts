import { createOpenAPI } from 'fumadocs-openapi/server';

export const API_ORIGIN = 'https://api.drowning.top:18443';
const DOCS_ORIGIN = 'https://docs.drowning.top:18443';
const MAX_BODY_BYTES = 8 * 1024 * 1024;

const proxy = createOpenAPI({ input: {} }).createProxy({
  allowedOrigins: [API_ORIGIN],
  filterRequest(request) {
    const target = new URL(request.url);
    return target.origin === API_ORIGIN && !target.username && !target.password &&
      !target.hash && target.pathname.startsWith('/v1/');
  },
  overrides: {
    request(request) {
      const headers = new Headers(request.headers);
      headers.delete('cookie');
      headers.delete('host');
      headers.delete('referer');
      return new Request(request, { headers });
    },
    response(response) {
      const headers = new Headers(response.headers);
      headers.delete('set-cookie');
      headers.set('Cache-Control', 'no-store');
      return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
    },
  },
});

// This is a fixed Isodo API bridge, not an arbitrary URL or cookie proxy.
export async function handlePlaygroundRequest(request: Request): Promise<Response> {
  const url = new URL(request.url);
  const origin = request.headers.get('origin');
  if (origin && origin !== DOCS_ORIGIN) return new Response('Forbidden origin', { status: 403 });
  const targets = url.searchParams.getAll('url');
  let target: URL;
  try {
    if (targets.length !== 1) throw new Error('one target required');
    target = new URL(targets[0]);
    if (target.origin !== API_ORIGIN || target.username || target.password || target.hash ||
      !target.pathname.startsWith('/v1/')) throw new Error('invalid target');
  } catch {
    return new Response('Only the Isodo public API is supported', { status: 403 });
  }
  if (url.searchParams.get('cookie')) return new Response('Cookie forwarding is disabled', { status: 403 });
  let body: ArrayBuffer | undefined;
  if (request.body) {
    const reader = request.body.getReader();
    const chunks: Uint8Array[] = [];
    let size = 0;
    while (true) {
      const next = await reader.read();
      if (next.done) break;
      size += next.value.byteLength;
      if (size > MAX_BODY_BYTES) {
        await reader.cancel();
        return new Response('Playground requests are limited to 8 MiB', { status: 413 });
      }
      chunks.push(next.value);
    }
    const bytes = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
    body = bytes.buffer;
  }
  const headers = new Headers(request.headers);
  headers.delete('cookie');
  return proxy.handle(new Request(request.url, {
    method: request.method, headers, body,
    signal: AbortSignal.any([request.signal, AbortSignal.timeout(60_000)]),
  }));
}
