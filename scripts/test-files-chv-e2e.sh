#!/usr/bin/env bash
# Isolated, real public API → PostgreSQL/worker → mTLS Node → CHV Guest suite.
set -euo pipefail
readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
: "${ISODO_CHV_BINARY:?Path to Cloud Hypervisor required}"
: "${ISODO_CHV_TEMPLATE_ARTIFACT:?Path to cached immutable artifact required}"
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && -w /dev/kvm ]] || { echo 'Requires Linux amd64/KVM.' >&2; exit 1; }
for tool in docker python3 debugfs cp setsid; do command -v "$tool" >/dev/null; done
readonly WORK="$(mktemp -d /tmp/isodo-files-e2e-run.XXXXXX)"
readonly CONTAINER="isodo-files-e2e-$(basename "$WORK" | tr '[:upper:].' '[:lower:]-')"
TEST_PID=''
cleanup() {
  local status=$?
  if [[ -n "$TEST_PID" ]]; then
    kill -- "-$TEST_PID" 2>/dev/null || true
    sleep 0.2
    kill -KILL -- "-$TEST_PID" 2>/dev/null || true
  fi
  docker rm -fv "$CONTAINER" >/dev/null 2>&1 || true
  rm -f "$WORK/postgres.env" "$WORK/test.env"
  # Keep diagnostics on failure, but never retain cloned templates or VM disks.
  python3 - "$WORK" <<'PY_CLEANUP'
import pathlib,re,shutil,sys
work=pathlib.Path(sys.argv[1])
for child in work.iterdir():
 if child.is_dir() and (child.name=='artifact' or re.fullmatch('[0-9a-f]{64}',child.name)):
  shutil.rmtree(child)
for child in work.glob('isodo-files-chv-*/node/vms'):
 shutil.rmtree(child)
PY_CLEANUP
  if [[ "$status" == 0 ]]; then rm -rf "$WORK"; echo '清理完成：专用数据库、模板副本和临时目录已删除。'; else echo "验收失败，诊断目录：$WORK；专用 PostgreSQL 已删除。" >&2; fi
  exit "$status"
}
trap cleanup EXIT
if [[ -z "${ISODO_FILES_E2E_GUEST_BINARY:-}" ]]; then
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$WORK/isodo-guest" ./cmd/isodo-guest)
  ISODO_FILES_E2E_GUEST_BINARY="$WORK/isodo-guest"
fi
if [[ -z "${ISODO_FILES_E2E_TEST_BINARY:-}" ]]; then
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$WORK/files-e2e.test" ./internal/node/orchestrator)
  ISODO_FILES_E2E_TEST_BINARY="$WORK/files-e2e.test"
fi
export ISODO_FILES_E2E_GUEST_BINARY
# Never patch the shared cache: clone flat files and replace Guest only in the copy.
python3 - "$ISODO_CHV_TEMPLATE_ARTIFACT" "$WORK" <<'PY'
import hashlib,json,os,pathlib,subprocess,sys
source,work=map(pathlib.Path,sys.argv[1:]);out=work/'artifact';out.mkdir()
m=json.loads((source/'manifest.json').read_bytes())
if m['rootfs'].get('format')!='raw' or m['rootfs'].get('filesystem')!='ext4' or m['rootfs'].get('partitioned') or m.get('runtime_snapshot'):
 raise SystemExit('Requires raw ext4 nonpartitioned cold-boot template')
for item in source.iterdir():
 if item.is_symlink() or not item.is_file():raise SystemExit('Flat regular cached artifact required')
 subprocess.run(['cp','--reflink=auto','--sparse=always',str(item),str(out/item.name)],check=True)
rootfs=out/m['rootfs']['file'];guest=os.environ['ISODO_FILES_E2E_GUEST_BINARY']
commands=work/'debugfs.commands';commands.write_text(f'rm /usr/local/bin/isodo-guest\nwrite {guest} /usr/local/bin/isodo-guest\nset_inode_field /usr/local/bin/isodo-guest mode 0100555\n')
with (work/'debugfs.log').open('w') as log:subprocess.run(['debugfs','-w','-f',str(commands),str(rootfs)],check=True,stdout=log,stderr=log)
check=work/'guest-check';subprocess.run(['debugfs','-R',f'dump /usr/local/bin/isodo-guest {check}',str(rootfs)],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
def digest(p):
 h=hashlib.sha256()
 with open(p,'rb') as f:
  for b in iter(lambda:f.read(4<<20),b''):h.update(b)
 return h.hexdigest()
if digest(check)!=digest(guest):raise SystemExit('Guest patch verification failed')
check.unlink();m['rootfs']['sha256']=digest(rootfs);m['rootfs']['size_bytes']=rootfs.stat().st_size
raw=json.dumps(m,sort_keys=True,separators=(',',':')).encode();(out/'manifest.json').write_bytes(raw);name=hashlib.sha256(raw).hexdigest();out.rename(work/name);(work/'artifact.path').write_text(str(work/name))
PY
python3 - "$WORK" <<'PY'
import pathlib,secrets,sys
p=pathlib.Path(sys.argv[1])/'postgres.env';p.write_text('POSTGRES_USER=isodo_e2e\nPOSTGRES_DB=isodo_files_e2e_runtime\nPOSTGRES_PASSWORD='+secrets.token_urlsafe(24)+'\n');p.chmod(0o600)
PY
docker run -d --name "$CONTAINER" --tmpfs /var/lib/postgresql/data:rw,nosuid,size=512m --env-file "$WORK/postgres.env" -p 127.0.0.1::5432 postgres:16-alpine >/dev/null
for ((i=0;i<60;i++)); do
  if docker exec "$CONTAINER" pg_isready -U isodo_e2e -d isodo_files_e2e_runtime >/dev/null 2>&1; then break; fi
  if [[ "$i" == 59 ]]; then echo '专用 PostgreSQL 启动超时' >&2; exit 1; fi
  sleep 1
done
(cd "$ROOT" && python3 scripts/render-migrations.py) | docker exec -i "$CONTAINER" psql -v ON_ERROR_STOP=1 -U isodo_e2e -d isodo_files_e2e_runtime > "$WORK/migrations.log" 2>&1
readonly PORT="$(docker inspect "$CONTAINER" --format '{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}')"
export ISODO_FILES_CHV_E2E=1
export ISODO_CHV_TEMPLATE_ARTIFACT="$(cat "$WORK/artifact.path")"
export ISODO_FILES_E2E_WORK_DIR="$WORK"
# Only temporary database material is read here, never production credentials.
ISODO_FILES_E2E_DATABASE_URL="$(python3 - "$WORK/postgres.env" "$PORT" <<'PY'
import sys,urllib.parse
config=dict(line.rstrip().split('=',1) for line in open(sys.argv[1]));print('postgresql://isodo_e2e:'+urllib.parse.quote(config['POSTGRES_PASSWORD'],safe='')+'@127.0.0.1:'+sys.argv[2]+'/isodo_files_e2e_runtime?sslmode=disable')
PY
)"
export ISODO_FILES_E2E_DATABASE_URL
setsid "$ISODO_FILES_E2E_TEST_BINARY" -test.run '^TestFilesPublicAPIRealCHV$' -test.v -test.timeout 8m > "$WORK/test.log" 2>&1 &
TEST_PID=$!
status=0
wait "$TEST_PID" || status=$?
cat "$WORK/test.log"
# Kill only this test's process group, including any child left after a timeout.
kill -- "-$TEST_PID" 2>/dev/null || true
sleep 0.2
kill -KILL -- "-$TEST_PID" 2>/dev/null || true
TEST_PID=''
exit "$status"
