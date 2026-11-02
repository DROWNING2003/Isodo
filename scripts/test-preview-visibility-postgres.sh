#!/usr/bin/env bash
# A new database container only. Never reads .env or connects to an existing DB.
set -euo pipefail
umask 077
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d /tmp/isodo-preview-policy.XXXXXX)"
CID=""
cleanup(){
 if [[ -n "$CID" ]];then docker logs "$CID" > "$WORK/postgres.log" 2>&1;docker stop "$CID" >/dev/null;fi
 rm -f -- "$WORK/postgres.env"
 echo "专用 PostgreSQL 已清理；日志目录: $WORK"
}
trap cleanup EXIT
python3 - "$WORK" <<'PY'
import pathlib,secrets,sys
(pathlib.Path(sys.argv[1])/'postgres.env').write_text('POSTGRES_PASSWORD='+secrets.token_urlsafe(32)+'\nPOSTGRES_DB=isodo_preview_visibility\n')
PY
CID="$(docker run -d --rm --env-file "$WORK/postgres.env" -p 127.0.0.1::5432 "${ISODO_TEST_POSTGRES_IMAGE:-postgres:17.4}")"
for attempt in {1..600};do
 if docker exec "$CID" pg_isready -h 127.0.0.1 -U postgres -d isodo_preview_visibility >/dev/null 2>&1;then break;fi
 sleep .1
done
(cd "$ROOT" && python3 scripts/render-migrations.py) > "$WORK/schema.sql"
docker exec -i "$CID" psql -q -v ON_ERROR_STOP=1 -U postgres -d isodo_preview_visibility < "$WORK/schema.sql" > "$WORK/migrations.log"
PORT="$(docker port "$CID" 5432 | sed 's/.*://')"
python3 - "$ROOT" "$WORK" "$PORT" "${ISODO_TEST_BINARY:-}" <<'PY'
import os,pathlib,subprocess,sys
root,work,port,binary=sys.argv[1:]
password=(pathlib.Path(work)/'postgres.env').read_text().splitlines()[0].split('=',1)[1]
env=dict(os.environ,ISODO_TEST_DATABASE_URL='postgres://postgres:'+password+'@127.0.0.1:'+port+'/isodo_preview_visibility?sslmode=disable')
args=[binary,'-test.run','^TestPortPreviewPersistenceAndDeletion$','-test.v'] if binary else ['go','test','./internal/control/postgres','-run','^TestPortPreviewPersistenceAndDeletion$','-count=1','-v']
with (pathlib.Path(work)/'test.log').open('w') as log:
 p=subprocess.run(args,cwd=root,env=env,stdout=log,stderr=subprocess.STDOUT)
 print((pathlib.Path(work)/'test.log').read_text())
 if p.returncode:raise SystemExit(p.returncode)
PY
# Verify the new migration is reversible on this disposable database.
python3 - "$ROOT/migrations/0015_http_preview_visibility.sql" "$WORK" <<'PY'
import pathlib,sys
source=pathlib.Path(sys.argv[1]).read_text()
root=pathlib.Path(sys.argv[2])
(root/'down.sql').write_text(source.split('-- +goose Down')[1])
(root/'up.sql').write_text(source.split('-- +goose Up')[1].split('-- +goose Down')[0])
PY
for migration in down up;do docker exec -i "$CID" psql -q -v ON_ERROR_STOP=1 -U postgres -d isodo_preview_visibility < "$WORK/$migration.sql";done
echo '迁移 Down→Up 通过'
