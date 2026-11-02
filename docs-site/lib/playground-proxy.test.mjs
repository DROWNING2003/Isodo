import assert from 'node:assert/strict';
import { test, afterEach, mock } from 'node:test';
import { handlePlaygroundRequest, API_ORIGIN } from './playground-proxy.ts';

afterEach(() => mock.restoreAll());
const request = (target, init = {}) => new Request(`https://docs.drowning.top:18443/api/playground?url=${encodeURIComponent(target)}`, init);

test('forwards an explicit API key to Isodo, without site cookies', async () => {
  const calls = [];
  mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push(new Request(url, init));
    return new Response('{"error":{"code":"UNAUTHENTICATED"}}', { status: 401, headers: { 'Content-Type': 'application/json', 'Set-Cookie': 'upstream=not-for-docs' } });
  });
  const response = await handlePlaygroundRequest(request(API_ORIGIN + '/v1/sandboxes', {
    method: 'POST', body: '{"name":"test"}',
    headers: { 'Origin': 'https://docs.drowning.top:18443', 'X-API-Key': 'fixture-only', Cookie: 'docs=session', 'Content-Type': 'application/json' },
  }));
  assert.equal(response.status, 401);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, API_ORIGIN + '/v1/sandboxes');
  assert.equal(calls[0].headers.get('X-API-Key'), 'fixture-only');
  assert.equal(calls[0].headers.get('Cookie'), null);
  assert.equal(await calls[0].text(), '{"name":"test"}');
  assert.equal(response.headers.get('Set-Cookie'), null);
  assert.equal(response.headers.get('Cache-Control'), 'no-store');
});

test('rejects other hosts, internal paths, userinfo and cross-site requests before fetch', async () => {
  const upstream = mock.method(globalThis, 'fetch', async () => { throw new Error('unexpected fetch'); });
  for (const target of ['https://example.com/v1/sandboxes', 'http://127.0.0.1/v1/sandboxes', API_ORIGIN + '/internal/mcp/test', 'https://user@api.drowning.top:18443/v1/sandboxes']) {
    assert.equal((await handlePlaygroundRequest(request(target))).status, 403);
  }
  assert.equal((await handlePlaygroundRequest(request(API_ORIGIN + '/v1/sandboxes', { headers: { Origin: 'https://evil.example' } }))).status, 403);
  assert.equal(upstream.mock.callCount(), 0);
});

test('limits request bodies before contacting the API', async () => {
  const upstream = mock.method(globalThis, 'fetch', async () => { throw new Error('unexpected fetch'); });
  const response = await handlePlaygroundRequest(request(API_ORIGIN + '/v1/sandboxes', { method: 'POST', body: new Uint8Array(8 * 1024 * 1024 + 1) }));
  assert.equal(response.status, 413);
  assert.equal(upstream.mock.callCount(), 0);
});

test('does not follow redirects to another origin', async () => {
  const upstream = mock.method(globalThis, 'fetch', async () => new Response(null, { status: 302, headers: { Location: 'https://example.com/v1/sandboxes' } }));
  const response = await handlePlaygroundRequest(request(API_ORIGIN + '/v1/sandboxes'));
  assert.equal(response.status, 400);
  assert.equal(upstream.mock.callCount(), 1);
});

test('OpenAPI and the playground proxy use the same API origin', async () => {
  const { createOpenAPI } = await import('fumadocs-openapi/server');
  const { fileURLToPath } = await import('node:url');
  const file = fileURLToPath(new URL('../../api/openapi/openapi-public.yaml', import.meta.url));
  const document = (await createOpenAPI({ input: { public: file } }).getSchema('public')).bundled;
  assert.equal(document.servers[0].url, API_ORIGIN);
  assert.equal(document.paths['/v1/sandboxes'].post.operationId, 'CreateSandbox');
});
