# Generic Sandbox HTTP Port Preview — implementation handoff

Status: **Draft scaffold only.** This PR **does not** publish working sandbox services yet. It is deliberately independent of PR #23 (Commands); branch from `main` and keep the two review scopes separate.

## User story / target

A user starts an HTTP service *inside* an Isodo CHV Sandbox:

```sh
python3 -m http.server 3000 --bind 127.0.0.1
```

An authorized browser can then open the predictable URL:

```text
https://3000-<sandboxID>.drowning.top/
https://5173-<sandboxID>.drowning.top/
https://8000-<sandboxID>.drowning.top/
```

If the external HTTPS listener still uses port 18443, the URL includes `:18443` after the hostname. That external port is **not** the Guest service port.

A user must **not** be able to access a private Sandbox simply by knowing its ID and guessing a port. The URL can be deterministic while access remains authenticated.

## What is already in main

- `internal/control/preview/preview.go` includes host parsing, an HMAC-signed T3 bootstrap link and host-only `HttpOnly` preview cookie, reverse proxy, CORS/origin enforcement, and `Publisher.Publish` for T3 pairing links.
- `internal/control/orchestrator/relay.go` already streams a TCP connection through Control → Node mTLS.
- `internal/node/orchestrator/chv_relay.go` bridges Node → Guest PortForward; **current Node logic allows arbitrary service values, and `generation=0` is used by T3**, so it is not currently a hardened generic-port authorization path.
- Guest `PortForward` supports TCP dialing in the VM, including `127.0.0.1:<port>`.
- Public OpenAPI currently exposes only the T3 app/app-link operation, not arbitrary ports.

## Scope committed in this scaffold PR

- `internal/control/preview/http_ports.go`: `Publisher.HTTPPortBaseURL(sandboxID, port)` produces the desired predictable public hostname using the configured public HTTPS port. This method **does not mint access**, register a proxy, or make the service reachable.
- `http_ports_test.go`: confirms port 3000/5173/8000, configured HTTPS 18443 vs 443, current Host parser compatibility, invalid ID/zero port, and reserved management/T3 ports.
- Reserves Guest management port 1024 and current T3 port 8080 from **generic HTTP publishing**, so the two services cannot be confused. Do not change the old T3 `Publish` behavior.

## Codex continuation plan

### 1. Implement authenticated grant issuance (Control)

Implement a public API (contract proposal, **not yet registered**):

```http
POST /v1/sandboxes/{sandboxID}/previews
Content-Type: application/json
Authorization: Bearer <authorized-user-or-scoped-key>

{"port":3000,"ttl_seconds":600}
```

Example response (illustrative only):

```json
{
  "port": 3000,
  "url": "https://3000-<sandboxID>.drowning.top:18443/",
  "authorization_url": "https://3000-<sandboxID>.drowning.top:18443/__isodo/auth/<token>?next=/",
  "expires_at": "2026-11-02T10:00:00Z"
}
```

`url` is stable/predictable. `authorization_url` bootstraps an HttpOnly **host-only** preview cookie once. A plain bookmark of `url` works only while authorized. Never use the predictable URL itself as a capability.

At the issuer, **first** authenticate the owner and verify tenant-scoped Sandbox access, desired/observed state both running, no deletion, assigned Node, generation/revision, and current Guest identity. Limit grant TTL; never issue a token for arbitrary sandbox IDs, stopped VMs, reserved ports, or a Node from another tenant. Prefer a narrow explicit `sandboxes:preview` key scope; if adding a new scope, update API key migrations, UI, tests, and documentation together. Cookie credentials must not appear in ordinary request logs.

### 2. Add a separate generic-HTTP claim/purpose to Preview Gateway

Do **not** reuse an unmodified T3 pairing token for arbitrary ports. Extend signed claims with explicit purpose (`http-port` vs legacy `t3`) plus runtime binding, e.g. tenant/account, generation, revision, boot ID and Guest epoch. Preserve old T3 pairing links and cookies.

On **every HTTP request and websocket handshake**:

1. Parse `<port>-<sandboxID>` host (already exists).
2. Verify signed preview cookie, expected `http-port` purpose, hostname sandbox ID/port and expiry.
3. Query authoritative sandbox/runtime state to confirm ownership binding, running state, assignment, generation/revision and current boot/epoch before proxying. Token validity alone is insufficient.
4. Respect existing cross-site Origin/CSRF protection; host-only Cookie, HttpOnly, Secure, SameSite appropriate to deployment.
5. Return 401 for missing/invalid grant and reject outdated grants after Stop/Start/Delete. For streams already open when VM stops, make sure the relay is torn down by lifecycle cancellation.

Introduce a gateway authorizer interface (fed by Control's Store/Node inspection) instead of coupling the generic proxy package directly to Postgres. Avoid logging grant values; do not forward the preview auth Cookie to Guest HTTP apps.

### 3. Explicit Node HTTP relay service

Reuse the existing `RelayPort` transport but distinguish `service="http-port"` from the existing `service="t3"`. The Node must **enforce** each service's allowed port and minimum generation/fencing requirements, and deny unknown services or reserved Guest management ports. Generic HTTP relay must:

- require nonzero ownership generation
- verify the live sandbox ID, Node journal state=running, current boot/epoch, and no in-flight stop/delete
- only connect to the requested **Guest TCP** port; never arbitrary host IP/Unix sockets/Guest management Vsock
- ensure requested generic port is valid and is not the reserved T3/management port

Do **not** add a new Node host-side listening port, a VMM NIC or a parallel `client-proxy`. Existing T3 must keep working.

### 4. HTTP/WebSocket/SSE streaming

The current gateway uses `httputil.ReverseProxy`. Reuse it for regular requests, websocket upgrades, SSE and Vite HMR, but verify the underlying `RelayPort` `net.Conn` half-close/long-lived stream behavior and cancellation. Do not claim websocket works without running a real test.

Make upstream `Host`, forwarded headers, internal cookie stripping and cross-origin behavior explicit. Set reasonable dial/idle timeouts and proxy error handling. Avoid leaking private headers or forwarding arbitrary authority to Guest.

### 5. OpenAPI, SDK and tests

After the handler works, add `POST /v1/sandboxes/{sandboxID}/previews` to `api/openapi/openapi-public.yaml`, run `make generate`, and commit the actual generated Go/TypeScript/docs output. Do not manually modify generated files in a way that causes `make generate-check` drift.

Provide SDK method `sandbox.ports.getPreviewURL(port)` or equivalent, making clear that a returned `url` alone does not bypass authorization.

Minimum tests:
- DNS-host format + external `18443` override (included in scaffold)
- grant issuance denied for wrong tenant, stopped/deleting Sandbox, malformed port, port 1024/8080, absent generation
- T3 cookie cannot grant generic-http access and vice versa
- invalid/expired/tampered token and wrong sandbox/port
- Stop/Start generation/boot/epoch/revision changes invalidate an old grant
- legitimate HTTP GET/POST and SSE; WebSocket/Vite HMR over Node relay
- verify headers and internal cookies never leak to Guest
- T3 workbench unchanged

## Definition of done

1. Start `python3 -m http.server 3000 --bind 127.0.0.1` inside an **isolated test** CHV Sandbox.
2. Obtain authorization via the API and visit `https://3000-<sandboxID>.drowning.top(:18443)/` in a browser without opening a host port per sandbox.
3. Repeat with a Vite service on 5173, including HMR/websocket.
4. Wrong account, unsigned link, reserved port and stale lifecycle grants are denied.
5. No regressions to T3 `8080` or other existing APIs.
6. `go test ./internal/control/preview`, Node/Control component tests, and `make generate-check` pass.

## Constraints

- Remote CHV first; keep Lima and CHV's external URL/API contract consistent.
- Do not change existing production services, E2B/client-proxy, deployed jfcs1024 VMs or Nginx without authorization.
- Keep this PR separate from Commands PR #23.
- No full Electron E2E migration is required for this slice; minimal HTTP/relay integration validation is still mandatory before merge.
- Do not report this scaffold as a finished working port preview feature.
