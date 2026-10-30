# Isodo single-host Nomad deployment (isolated API test)

This workflow deploys only `isodo-api-pr18-test`; it does not replace the existing E2B `api` job and does not deploy or modify a Node. Existing PostgreSQL, Redis, SeaweedFS, templates, Node state, and the Isodo checkout are preserved.

Environment observed on 2026-10-30: Nomad datacenter `dc1`; Nomad client `jfcs1024` is ready with healthy Docker and `raw_exec` drivers; the host private interface is `10.210.10.32`. Port `5004` was free during the read-only check. Use the isolated config and artifacts directory `/root/zkp/isodo-pr18-test`; do not reuse `/root/zkp/isodo-unify-demo-20261025-s3`, `/root/zkp/isodo-api-unify`, `/root/zkp/isodo`, or the existing Postgres database `isodo_probe_t3` without a separate safety review.

The existing Nomad `api` job is E2B and remains untouched. The isolated test stack now has its own PostgreSQL and Casdoor Compose services under `/root/zkp/isodo-pr18-test`, plus Nomad job `isodo-api-pr18-test` on `jfcs1024`. The API is at `http://10.210.10.32:5004`; Casdoor is at `http://10.210.10.32:18000`. `/healthz` returns 200, the Casdoor discovery document returns 200, and `/auth/login` redirects to Casdoor with the registered `http://app.isodo.localhost:5734/auth/callback` and PKCE S256. This is a private-LAN test only; its OIDC HTTP traffic is unencrypted.

- API: Nomad Docker task, image pushed to aslan-spock-register.qiniu.io/las, version-pinned.
- Node: optional raw_exec Nomad job, **disabled by default**. Current CHVServer.Close() stops live VMs and runtime ownership is not reconciled on restart.
- Desktop: Electron bundles React assets. A 127.0.0.1 HTTP origin proxies /auth, /v1 to a configured remote API. For release set ISODO_REMOTE_API_URL=https://YOUR_API_ORIGIN or configure desktop.json in Electron userData.

## API workflow

Build and push a versioned image from a machine with registry access:
```sh
ISODO_RELEASE=pr18-test-20261030-private-http-01 bash deploy.sh build-api
docker push aslan-spock-register.qiniu.io/las/isodo-api:pr18-test-20261030-private-http-01
```

On `jfcs1024`, create the test-only config under `/root/zkp/isodo-pr18-test` without copying E2B credentials or pointing at an existing production database. The API env file must provide `ISODO_DATABASE_URL`, `ISODO_CASDOOR_ISSUER`, `ISODO_CASDOOR_CLIENT_ID`, `ISODO_CASDOOR_CLIENT_SECRET`, and a valid `ISODO_WEB_ORIGIN`; the mounted cert directory must contain `injection.key`. Keep any Node RPC/mTLS variables unset: this API test intentionally disables its Node client and create worker.

For this isolated LAN test only, `ISODO_CASDOOR_INSECURE_HTTP_HOST` may contain the exact private IP and port of a plain-HTTP Casdoor issuer, for example `10.210.10.32:18000`. This opts that one authority into unencrypted OIDC discovery, user-info, authorization, and token requests. The setting is rejected unless it is a private unicast IP with a valid port, and all HTTP endpoints must use that exact authority. Leave it unset in production; use HTTPS for any non-loopback production issuer.

First run the read-only Nomad validate and scheduler plan:
```sh
ISODO_NOMAD_NODE_NAME=jfcs1024 \
ISODO_API_BIND_ADDR=10.210.10.32 \
ISODO_API_PORT=5004 \
ISODO_API_CONFIG_DIR=/root/zkp/isodo-pr18-test \
ISODO_API_IMAGE=aslan-spock-register.qiniu.io/las/isodo-api:pr18-test-20261030-private-http-01 \
bash deploy.sh plan-api
```

Only after confirming the test credentials, database, Casdoor issuer, API config files, and port isolation, apply with `ISODO_CONFIRM_UPDATE_API=yes bash deploy.sh update-api`. The script validates that Nomad planned a successful placement and stops on plan errors. The Isodo API process does not run SQL migrations on startup; any schema setup must be reviewed and run only against a newly isolated test database. The API test job is separate from the E2B `api` job and does not connect to the existing Isodo Node.

The temporary no-certificate LAN test can bind Casdoor and the Isodo API to `10.210.10.32` on their dedicated ports. Set the Casdoor issuer to `http://10.210.10.32:18000` and `ISODO_CASDOOR_INSECURE_HTTP_HOST=10.210.10.32:18000` in the isolated API config. This sends OIDC discovery and authorization traffic unencrypted inside the private LAN; use only for this isolated test and switch to HTTPS before any shared or production use. Do not put the private IP in the Electron package.

## Node caveat

Only use update-node during an approved maintenance window, after confirming no active VMs, orphans or pending operations. Its journal guard is intentionally conservative, not a full runtime reconciliation implementation. The example Node resource limits and raw_exec cgroup model must be verified before activation.

## Desktop Action

Run workflow **Desktop macOS build** manually or on a PR. It produces an **unsigned development-only arm64 ZIP**, not a notarized distributable. The artifact includes Electron, bundled renderer, native macOS Node binary and Linux arm64 guest binary, but **does not include the local T3 workspace bundle**. Local Agent workspace and complete OAuth/Preview pairing have not been E2E verified. The fixed 127.0.0.1:5734 origin requires matching server-side OAuth callback and session origin configuration; this is **not yet production-ready**.

No GitHub Actions job accesses jfcs1024, registry, or production secrets.
