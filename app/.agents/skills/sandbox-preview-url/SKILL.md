---
name: sandbox-preview-url
description: Format preview URLs using the Qiniu sandbox domain pattern. Use when an agent working inside a sandbox needs to give the user a URL to preview their work (running web server, dev server, etc.). The sandbox exposes services via a predictable hostname pattern rather than localhost.
---

# Sandbox Preview URL

Qiniu sandboxes expose running services at:

```
https://{port}-{sandboxID}.{domain}
```

## When to use this format

Whenever you're running inside a Qiniu sandbox and need to tell the user where to look at the running service:

- A web app running on port 3000 → `https://3000-abc123def456.sandbox.example.com`
- A dev server on port 5173 → `https://5173-abc123def456.sandbox.example.com`
- T3 Code on port 8080 → `https://8080-abc123def456.sandbox.example.com`

**Never** use `localhost:PORT` for sandbox previews — the user's browser can't reach it.

## Where to get the domain

The sandbox domain comes from the sandbox's `domain` field in the API response, or from the configured fallback `sandboxDomain` in credentials.

## Validation

The sandbox ID must be alphanumeric (`[a-z0-9-]+`). The domain must be a valid FQDN. See `packages/shared/src/sandbox.ts:sandboxUrl()` for the canonical implementation.

## Example

```
sandboxID: "a1b2c3d4e5f6"
domain:    "sandbox.qiniu-sandbox.com"
port:      3000

→ https://3000-a1b2c3d4e5f6.sandbox.qiniu-sandbox.com
```
