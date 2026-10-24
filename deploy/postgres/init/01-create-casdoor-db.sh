#!/bin/sh
set -eu

psql \
  --set=ON_ERROR_STOP=1 \
  --set=casdoor_database="$CASDOOR_DATABASE" \
  --username="$POSTGRES_USER" \
  --dbname="$POSTGRES_DB" <<'SQL'
SELECT format('CREATE DATABASE %I', :'casdoor_database')
WHERE NOT EXISTS (
  SELECT 1 FROM pg_database WHERE datname = :'casdoor_database'
) \gexec
SQL
