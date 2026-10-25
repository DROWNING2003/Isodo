SHELL := /bin/sh

.PHONY: help init infra-up infra-down infra-ps infra-logs casdoor-ready db-migrate dev-certs api node node-local node-local-t3 web electron mobile t3-app-bundle docs docs-build test test-integration

COMPOSE := docker compose -f compose.yaml
IOS_SIMULATOR ?= iPhone 17
NODE_BACKEND ?= chv
API_NODE_BACKEND ?= lima
# Casdoor is reached through whatever origin .env declares (an ngrok tunnel in
# shared setups). Falling back to localhost silently made the container and the
# API disagree about the issuer, which breaks the OAuth callback handshake.
API_CASDOOR_ISSUER ?= $(shell . ./.env 2>/dev/null; printf '%s' "$${CASDOOR_ORIGIN:-http://localhost:8000}")
ISODO_CONTROL_CLIENT_URI ?= spiffe://isodo/control-worker
GOARCH ?= $(shell go env GOARCH)
ISODO_GUEST_AGENT_BINARY ?= $(CURDIR)/.tools/isodo-guest-linux-$(GOARCH)
MOBILE_DEV_ENV := APP_VARIANT=development T3CODE_IOS_PERSONAL_TEAM=1 T3CODE_IOS_PERSONAL_TEAM_BUNDLE_ID=com.isodo.app.dev EXPO_NO_GIT_STATUS=1

help:
	@printf '%s\n' \
	  'make init        Create a local .env with generated passwords' \
	  'make infra-up    Start PostgreSQL, Redis, Casdoor and SeaweedFS' \
	  'make infra-down  Stop services and preserve database, Redis and SeaweedFS volumes' \
	  'make infra-ps    Show service status' \
	  'make infra-logs  Follow infrastructure logs' \
	  'make casdoor-ready Wait for Casdoor OIDC discovery' \
	  'make seaweedfs-ready Wait for template S3 storage health' \
	  'make db-migrate  Apply the initial Isodo schema once' \
	  'make dev-certs   Create local-only mTLS certificates for the Node and API' \
	  'make api         Apply schema and run the Go API on 127.0.0.1:8080' \
	  'make node        Run the Linux Node Orchestrator gRPC endpoint' \
	  'make node-local  Run the Lima-backed local Node Orchestrator' \
	  'make node-local-t3 Build T3 and run the Lima-backed Node with T3 and Agent CLIs' \
	  'make web         Run the Isodo web UI on 127.0.0.1:5733 via pnpm' \
	  'make electron    Build and open the Isodo desktop app via pnpm' \
	  'make mobile      Build/install the iOS development app and open Simulator' \
	  'make t3-app-bundle Build the T3 server bundle with its web application' \
	  'make docs        Run the Fumadocs site on 127.0.0.1:3000' \
	  'make docs-build  Build the Fumadocs site for production' \
	  'make generate    Generate OpenAPI bindings, gRPC code and docs' \
	  'make generate-check Check generated API files for drift' \
	  'make test        Run Go tests' \
	  'make test-storage Test S3 artifact integrity and permissions' \
	  'make test-integration Run race tests against an isolated temporary database'

init:
	@if [ -f .env ]; then \
	  echo '.env already exists; leaving it unchanged'; \
	else \
	  db_password=$$(openssl rand -hex 24) || exit 1; \
	  admin_password=$$(openssl rand -hex 24) || exit 1; \
	  { \
	    awk '!/^POSTGRES_PASSWORD=/ && !/^CASDOOR_ADMIN_PASSWORD=/' .env.example; \
	    printf 'POSTGRES_PASSWORD=%s\nCASDOOR_ADMIN_PASSWORD=%s\n' "$$db_password" "$$admin_password"; \
	  } > .env; \
	  chmod 600 .env; \
	  echo 'Created .env with local-only random passwords.'; \
	  echo 'Casdoor admin username: admin'; \
	  echo 'Read the generated admin password from .env'; \
	fi

	python3 scripts/configure-seaweedfs.py

infra-up: init
	python3 scripts/configure-casdoor.py
	CASDOOR_ORIGIN="$(API_CASDOOR_ISSUER)" $(COMPOSE) up -d

infra-down:
	$(COMPOSE) down

infra-ps:
	$(COMPOSE) ps

infra-logs:
	$(COMPOSE) logs -f --tail=100

casdoor-ready: infra-up
	@. ./.env; \
	for attempt in $$(seq 1 60); do \
	  if curl --fail --silent --show-error "$(API_CASDOOR_ISSUER)/.well-known/openid-configuration" >/dev/null 2>&1; then \
	    echo "Casdoor is ready at $(API_CASDOOR_ISSUER)"; exit 0; \
	  fi; \
	  sleep 2; \
	done; \
	echo "Casdoor did not become ready; inspect with 'make infra-logs'" >&2; exit 1

db-migrate: infra-up
	sh scripts/migrate-db.sh

dev-certs:
	sh scripts/dev-certs.sh

api: db-migrate casdoor-ready dev-certs
	@. ./.env; \
	ISODO_DATABASE_URL="postgres://$${POSTGRES_USER}:$${POSTGRES_PASSWORD}@127.0.0.1:$${POSTGRES_PORT}/$${POSTGRES_DB}?sslmode=disable" \
	ISODO_CASDOOR_ISSUER="$(API_CASDOOR_ISSUER)" \
	ISODO_CASDOOR_CLIENT_ID="$$ISODO_CASDOOR_CLIENT_ID" \
	ISODO_CASDOOR_CLIENT_SECRET="$$ISODO_CASDOOR_CLIENT_SECRET" \
	ISODO_WEB_ORIGIN="$$ISODO_WEB_ORIGIN" ISODO_LISTEN_ADDR="$$ISODO_LISTEN_ADDR" \
	ISODO_INJECTION_KEY="$${ISODO_INJECTION_KEY:-}" \
	ISODO_INJECTION_KEY_FILE="$${ISODO_INJECTION_KEY_FILE:-./certs/injection.key}" \
	ISODO_NODE_BACKEND="$(API_NODE_BACKEND)" \
	ISODO_NODE_RPC_TARGET="$${ISODO_NODE_RPC_TARGET:-127.0.0.1:9090}" \
	ISODO_NODE_ID="$${ISODO_NODE_ID:-isodo-local-node}" \
	ISODO_NODE_TLS_SERVER_NAME="$${ISODO_NODE_TLS_SERVER_NAME:-isodo-node.local}" \
	ISODO_NODE_SERVER_CA="$${ISODO_NODE_SERVER_CA:-./certs/node-ca.crt}" \
	ISODO_CONTROL_TLS_CERT="$${ISODO_CONTROL_TLS_CERT:-./certs/control-worker.crt}" \
	ISODO_CONTROL_TLS_KEY="$${ISODO_CONTROL_TLS_KEY:-./certs/control-worker.key}" \
	go run ./cmd/isodo-api

node:
	@. ./.env; \
	ISODO_NODE_BACKEND="$(NODE_BACKEND)" \
	ISODO_GUEST_AGENT_BINARY="$(ISODO_GUEST_AGENT_BINARY)" \
	ISODO_NODE_ID="$${ISODO_NODE_ID:-isodo-local-node}" \
	ISODO_NODE_LISTEN_ADDR="$${ISODO_NODE_LISTEN_ADDR:-0.0.0.0:9090}" \
	ISODO_NODE_TLS_CERT="$${ISODO_NODE_TLS_CERT:-./certs/node.crt}" \
	ISODO_NODE_TLS_KEY="$${ISODO_NODE_TLS_KEY:-./certs/node.key}" \
	ISODO_NODE_CLIENT_CA="$${ISODO_NODE_CLIENT_CA:-./certs/control-ca.crt}" \
	ISODO_CONTROL_CLIENT_URI="$${ISODO_CONTROL_CLIENT_URI:-$(ISODO_CONTROL_CLIENT_URI)}" \
	ISODO_NODE_VERSION="$${ISODO_NODE_VERSION:-0.1.0-dev}" \
	ISODO_CH_VERSION="$${ISODO_CH_VERSION:-}" \
	ISODO_CH_BINARY="$${ISODO_CH_BINARY:-cloud-hypervisor}" \
	ISODO_NODE_STATE_DIR="$${ISODO_NODE_STATE_DIR:-}" \
	ISODO_TEMPLATE_S3_ENDPOINT="$${ISODO_TEMPLATE_S3_ENDPOINT:-}" \
	ISODO_TEMPLATE_S3_REGION="$${ISODO_TEMPLATE_S3_REGION:-us-east-1}" \
	ISODO_TEMPLATE_S3_BUCKET="$${ISODO_TEMPLATE_S3_BUCKET:-}" \
	SEAWEEDFS_READ_ACCESS_KEY="$${SEAWEEDFS_READ_ACCESS_KEY:-}" \
	SEAWEEDFS_READ_SECRET_KEY="$${SEAWEEDFS_READ_SECRET_KEY:-}" \
	ISODO_LIMA_BINARY="$${ISODO_LIMA_BINARY:-limactl}" \
	ISODO_LIMA_INSTANCE_PREFIX="$${ISODO_LIMA_INSTANCE_PREFIX:-isodo-sbx-}" \
	ISODO_LIMA_TEMPLATE="$${ISODO_LIMA_TEMPLATE:-template:ubuntu-24.04}" \
	ISODO_LIMA_VM_TYPE="$${ISODO_LIMA_VM_TYPE:-vz}" \
	ISODO_LIMA_CPUS="$${ISODO_LIMA_CPUS:-2}" \
	ISODO_LIMA_MEMORY_GIB="$${ISODO_LIMA_MEMORY_GIB:-4}" \
	ISODO_LIMA_DISK_GIB="$${ISODO_LIMA_DISK_GIB:-20}" \
	ISODO_T3_SERVER_BUNDLE="$(if $(ISODO_T3_SERVER_BUNDLE),$(ISODO_T3_SERVER_BUNDLE),$${ISODO_T3_SERVER_BUNDLE})" \
	go run ./cmd/isodo-node

node-local: dev-certs $(ISODO_GUEST_AGENT_BINARY)
	$(MAKE) node NODE_BACKEND=lima

node-local-t3: dev-certs $(ISODO_GUEST_AGENT_BINARY) t3-app-bundle
	$(MAKE) node NODE_BACKEND=lima ISODO_T3_SERVER_BUNDLE="$(CURDIR)/app/artifacts/sandbox/t3-server-dist.bundle"

$(ISODO_GUEST_AGENT_BINARY):
	mkdir -p "$(dir $@)"
	GOOS=linux GOARCH="$(GOARCH)" CGO_ENABLED=0 go build -o "$@" ./cmd/isodo-guest

test:
	go test ./...

web:
	cd app && HOST=127.0.0.1 PORT=5734 VITE_BOUNDLY_WEB_SURFACE=full pnpm --filter @t3tools/web dev

electron:
	cd app && pnpm build:desktop && pnpm --filter @t3tools/desktop start

mobile:
	cd app && $(MOBILE_DEV_ENV) pnpm --filter @t3tools/mobile exec expo run:ios --device "$(IOS_SIMULATOR)"

# T3's server bundle normally omits dist/client. The Isodo Guest runs headless,
# so ship the built web application beside bin.mjs for ServerConfig.resolveStaticDir.
t3-app-bundle:
	cd app && corepack pnpm@11.10.0 --filter @t3tools/web build
	cd app && corepack pnpm@11.10.0 --filter t3 build:bundle
	mkdir -p app/apps/server/dist/client
	cp -R app/apps/web/dist/. app/apps/server/dist/client/
	mkdir -p app/artifacts/sandbox
	tar -czf app/artifacts/sandbox/t3-server-dist.bundle --exclude='*.map' -C app/apps/server dist

docs:
	npm --prefix docs-site run dev -- --hostname 127.0.0.1

docs-build:
	npm --prefix docs-site run build

test-integration: infra-up
	sh scripts/test-integration.sh

# Contract files are the source of truth; generated code is never edited by hand.
.PHONY: generate generate-openapi generate-proto generate-docs generate-check
generate: generate-openapi generate-proto generate-sql generate-docs

generate-openapi:
	sh scripts/generate-api.sh

generate-proto:
	sh scripts/generate-proto.sh

generate-docs:
	sh scripts/generate-docs.sh

generate-check:
	python3 scripts/check-generated-api.py

.PHONY: generate-sql
generate-sql:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

.PHONY: seaweedfs-ready test-storage
seaweedfs-ready:
	@for attempt in $$(seq 1 60); do \
	  if $(COMPOSE) exec -T seaweedfs wget -q -O /dev/null http://127.0.0.1:8333/healthz; then \
	    echo 'SeaweedFS S3 is ready'; exit 0; \
	  fi; \
	  sleep 2; \
	done; \
	echo 'SeaweedFS did not become ready; inspect make infra-logs' >&2; exit 1

test-storage: infra-up seaweedfs-ready
	sh scripts/test-storage.sh
