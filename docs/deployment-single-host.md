# Isodo single-host Nomad deployment (draft)

This PR does not deploy to jfcs1024 or mutate its running VMs. Existing PostgreSQL, Casdoor, Redis, and SeaweedFS stay in Compose.

- API: Nomad Docker task, image pushed to aslan-spock-register.qiniu.io/las, version-pinned.
- Node: optional raw_exec Nomad job, **disabled by default**. Current CHVServer.Close() stops live VMs and runtime ownership is not reconciled on restart.
- Desktop: Electron bundles React assets. A 127.0.0.1 HTTP origin proxies /auth, /v1 to a configured remote API. For release set ISODO_REMOTE_API_URL=https://YOUR_API_ORIGIN or configure desktop.json in Electron userData.

## API workflow

After reviewing port conflicts and /etc/isodo/api.env and Nomad client name, on a machine with registry access:
```sh
ISODO_RELEASE=dev-001 bash deploy.sh build-api
docker push aslan-spock-register.qiniu.io/las/isodo-api:dev-001
```

On jfcs1024, first plan:
```sh
ISODO_NOMAD_NODE_NAME=YOUR_NOMAD_CLIENT ISODO_API_IMAGE=aslan-spock-register.qiniu.io/las/isodo-api:dev-001 bash deploy.sh update-api
```
Only with explicit approval add ISODO_CONFIRM_UPDATE_API=yes. Script does **not** migrate, start, or stop Compose.

## Node caveat

Only use update-node during an approved maintenance window, after confirming no active VMs, orphans or pending operations. Its journal guard is intentionally conservative, not a full runtime reconciliation implementation. The example Node resource limits and raw_exec cgroup model must be verified before activation.

## Desktop Action

Run workflow **Desktop macOS build** manually or on a PR. It produces an **unsigned development-only arm64 ZIP**, not a notarized distributable. The artifact includes Electron, bundled renderer, native macOS Node binary and Linux arm64 guest binary, but **does not include the local T3 workspace bundle**. Local Agent workspace and complete OAuth/Preview pairing have not been E2E verified. The fixed 127.0.0.1:5734 origin requires matching server-side OAuth callback and session origin configuration; this is **not yet production-ready**.

No GitHub Actions job accesses jfcs1024, registry, or production secrets.
