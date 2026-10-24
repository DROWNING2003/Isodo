#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
. ./.env
isodo_test_db="isodo_test_$(date +%s)_$$"
docker compose exec -T db createdb -U "$POSTGRES_USER" "$isodo_test_db"
trap 'docker compose exec -T db dropdb -U "$POSTGRES_USER" "$isodo_test_db"' EXIT
sh scripts/migrate-db.sh "$isodo_test_db"
ISODO_TEST_DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@127.0.0.1:$POSTGRES_PORT/$isodo_test_db?sslmode=disable" CGO_ENABLED=1 go test -race ./... -count=1
