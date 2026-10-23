#!/usr/bin/env bash
# End-to-end test of the backup service against a real database and a real S3-compatible store (RustFS,
# standing in for R2): a nightly run stores an encrypted dump and copies files, a second run copies only
# what is new, the dump restores into a fresh database, a deleted file comes back with its encoding, and
# a broken bucket is recorded as a failure instead of passing quietly.
#
#   BACKUP_IMAGE=cadportal-backup:test scripts/backup_service_test.sh
#
# Build the image first:  docker build --target backup -t cadportal-backup:test backend
# Everything runs in throwaway containers on a private network; nothing else is touched.
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1 # Git Bash on Windows must not rewrite /data and /scratch

IMAGE=${BACKUP_IMAGE:-cadportal-backup:test}
S3_IMAGE=${S3_IMAGE:-rustfs/rustfs:latest}
AWS_IMAGE=${AWS_IMAGE:-amazon/aws-cli:latest}
PG_IMAGE=${PG_IMAGE:-postgres:18-alpine}
net=bktest-$$
tmp=$(mktemp)
fail() { echo "FAIL: $*" >&2; exit 1; }
cleanup() { rm -f "$tmp"; docker rm -f "$net-s3" "$net-pg" "$net-daemon" >/dev/null 2>&1 || true; docker network rm "$net" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker network create "$net" >/dev/null
docker run -d --name "$net-s3" --network "$net" -e RUSTFS_ACCESS_KEY=testkey -e RUSTFS_SECRET_KEY=testsecret123 "$S3_IMAGE" /data >/dev/null
docker run -d --name "$net-pg" --network "$net" -e POSTGRES_USER=cad -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=cad_portal "$PG_IMAGE" >/dev/null

aws() { docker run --rm -i --network "$net" -e AWS_ACCESS_KEY_ID=testkey -e AWS_SECRET_ACCESS_KEY=testsecret123 -e AWS_DEFAULT_REGION=auto "$AWS_IMAGE" --endpoint-url "http://$net-s3:9000" "$@"; }
sql() { docker exec -i "$net-pg" psql -v ON_ERROR_STOP=1 -At -U cad -d "${DB:-cad_portal}" -c "$1"; }
head_enc() { aws s3api head-object --bucket "$1" --key "$2" --query ContentEncoding --output text; }

for _ in $(seq 1 60); do
  docker exec "$net-pg" pg_isready -U cad -d cad_portal >/dev/null 2>&1 && aws s3 ls >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$net-pg" pg_isready -U cad -d cad_portal >/dev/null || fail "database did not start"
aws s3 mb s3://vault >/dev/null
aws s3 mb s3://backups >/dev/null

# The portal's table for run history, and some data to protect.
for m in 000011_backup_runs 000012_backup_requests; do
  docker exec -i "$net-pg" psql -v ON_ERROR_STOP=1 -q -U cad -d cad_portal <"backend/migrations/$m.up.sql"
done
sql "CREATE TABLE demo (v text); INSERT INTO demo VALUES ('one'), ('two')" >/dev/null

# Files as the portal stores them: CAD gzipped and flagged, a photo plain.
printf 'cad bytes %.0s' $(seq 1 200) | gzip -n | aws s3 cp - s3://vault/cad/LR0001/1/a.3dm.gz --content-encoding gzip --content-type application/octet-stream >/dev/null
printf 'jpeg bytes' | aws s3 cp - s3://vault/media/LR0001/p.jpg --content-type image/jpeg >/dev/null

keys=$(docker run --rm "$IMAGE" keygen 2>/dev/null)
pub=$(echo "$keys" | sed -n 's/^# public key: //p')
secret=$(echo "$keys" | grep '^AGE-SECRET-KEY-')
[ -n "$pub" ] && [ -n "$secret" ] || fail "keygen printed no key pair"

svc() { # service <extra docker args...> -- <command...>
  local extra=() cmd=()
  while [ "$1" != "--" ]; do extra+=("$1"); shift; done
  shift
  cmd=("$@")
  docker run --rm --network "$net" \
    -e DATABASE_URL="postgres://cad:pw@$net-pg:5432/cad_portal?sslmode=disable" \
    -e S3_ENDPOINT="http://$net-s3:9000" -e S3_BUCKET=vault -e S3_ACCESS_KEY_ID=testkey -e S3_SECRET_ACCESS_KEY=testsecret123 \
    -e BACKUP_S3_BUCKET=backups -e BACKUP_S3_ACCESS_KEY_ID=testkey -e BACKUP_S3_SECRET_ACCESS_KEY=testsecret123 \
    -e BACKUP_AGE_RECIPIENT="$pub" "${extra[@]}" "$IMAGE" "${cmd[@]}"
}

# ---- first run
svc -- once || fail "the first backup failed"
dump=$(aws s3 ls s3://backups/db/ | awk '{print $4}')
case $dump in cad_portal-*.dump.age) ;; *) fail "expected one encrypted dump, found '$dump'" ;; esac
[ "$(aws s3 ls s3://backups/files/ --recursive | wc -l | tr -d ' ')" = "2" ] || fail "both files should be in the backup"
[ "$(head_enc backups files/cad/LR0001/1/a.3dm.gz)" = "gzip" ] || fail "the copied CAD file lost its Content-Encoding"
[ "$(sql "SELECT ok::text || ',' || files_copied || ',' || files_total FROM backup_runs ORDER BY id DESC LIMIT 1")" = "true,2,2" ] || fail "the run was not recorded as 2 files copied"

# The copied CAD file is byte-identical to the original (still gzip, not decoded on the way).
a=$(aws s3 cp s3://vault/cad/LR0001/1/a.3dm.gz - | sha256sum)
b=$(aws s3 cp s3://backups/files/cad/LR0001/1/a.3dm.gz - | sha256sum)
[ "$a" = "$b" ] || fail "the copy differs from the original"

# ---- the dump is encrypted, and restores with the secret key
docker run --rm --network "$net" -e BACKUP_S3_BUCKET=backups -e BACKUP_S3_ENDPOINT="http://$net-s3:9000" \
  -e BACKUP_S3_ACCESS_KEY_ID=testkey -e BACKUP_S3_SECRET_ACCESS_KEY=testsecret123 "$IMAGE" fetch "db/$dump" /scratch/x.dump >/dev/null 2>&1 \
  && fail "an encrypted dump was fetched without the secret key"
docker run --rm --network "$net" --entrypoint sh \
  -e BACKUP_S3_BUCKET=backups -e BACKUP_S3_ENDPOINT="http://$net-s3:9000" -e BACKUP_S3_ACCESS_KEY_ID=testkey \
  -e BACKUP_S3_SECRET_ACCESS_KEY=testsecret123 -e BACKUP_AGE_IDENTITY="$secret" -e PGPASSWORD=pw "$IMAGE" -c "
  set -e
  /app/backup fetch 'db/$dump' /scratch/x.dump
  psql -q -h $net-pg -U cad -d cad_portal -c 'CREATE DATABASE restored'
  pg_restore -h $net-pg -U cad -d restored /scratch/x.dump" || fail "the dump did not restore"
# Standard output works too (that is what the README restore recipe uses), and it is the plain dump.
fetched=$(docker run --rm --network "$net" -e BACKUP_S3_BUCKET=backups -e BACKUP_S3_ENDPOINT="http://$net-s3:9000" \
  -e BACKUP_S3_ACCESS_KEY_ID=testkey -e BACKUP_S3_SECRET_ACCESS_KEY=testsecret123 -e BACKUP_AGE_IDENTITY="$secret" \
  "$IMAGE" fetch "db/$dump" - 2>/dev/null >"$tmp" && head -c 5 "$tmp")
[ "$fetched" = "PGDMP" ] || fail "fetch to standard output did not give a plain dump (got '$fetched')"
[ "$(DB=restored sql "SELECT string_agg(v, ',' ORDER BY v) FROM demo")" = "one,two" ] || fail "restored data differs"

# ---- second run: only what is new
printf 'second photo' | aws s3 cp - s3://vault/media/LR0001/q.jpg --content-type image/jpeg >/dev/null
sleep 1
svc -- once || fail "the second backup failed"
[ "$(sql "SELECT files_copied || ',' || files_total FROM backup_runs ORDER BY id DESC LIMIT 1")" = "1,3" ] || fail "the second run should copy only the new photo"

# ---- a file lost from the vault comes back, with its encoding
aws s3 rm s3://vault/cad/LR0001/1/a.3dm.gz >/dev/null
svc -- restore-files || fail "restore-files failed"
[ "$(aws s3 cp s3://vault/cad/LR0001/1/a.3dm.gz - | sha256sum)" = "$a" ] || fail "the restored file differs"
[ "$(head_enc vault cad/LR0001/1/a.3dm.gz)" = "gzip" ] || fail "the restored file lost its Content-Encoding"

# ---- "Back up now" from the portal: the running service picks the request up within a minute
before=$(sql "SELECT count(*) FROM backup_runs")
sql "INSERT INTO backup_requests (requested_by) VALUES (NULL)" >/dev/null
docker run -d --name "$net-daemon" --network "$net" \
  -e DATABASE_URL="postgres://cad:pw@$net-pg:5432/cad_portal?sslmode=disable" \
  -e S3_ENDPOINT="http://$net-s3:9000" -e S3_BUCKET=vault -e S3_ACCESS_KEY_ID=testkey -e S3_SECRET_ACCESS_KEY=testsecret123 \
  -e BACKUP_S3_BUCKET=backups -e BACKUP_S3_ACCESS_KEY_ID=testkey -e BACKUP_S3_SECRET_ACCESS_KEY=testsecret123 \
  -e BACKUP_S3_ENDPOINT="http://$net-s3:9000" -e BACKUP_AGE_RECIPIENT="$pub"   -e BACKUP_AT="21:15, 03:00,09:30,03:00" "$IMAGE" >/dev/null
picked=""
for _ in $(seq 1 40); do
  picked=$(sql "SELECT count(*) FROM backup_requests WHERE picked_up_at IS NOT NULL AND (SELECT count(*) FROM backup_runs WHERE ok) > $before")
  [ "$picked" = "1" ] && break
  sleep 3
done
docker logs "$net-daemon" 2>&1 | tail -5
docker logs "$net-daemon" 2>&1 | grep -q "next backup at" || fail "the service did not report its next scheduled run (BACKUP_AT list)"
docker rm -f "$net-daemon" >/dev/null 2>&1
[ "$picked" = "1" ] || fail "a requested backup was not picked up by the running service"

# ---- a broken backup is recorded as a failure, and exits non-zero
if docker run --rm --network "$net" \
  -e DATABASE_URL="postgres://cad:pw@$net-pg:5432/cad_portal?sslmode=disable" \
  -e S3_ENDPOINT="http://$net-s3:9000" -e S3_BUCKET=vault -e S3_ACCESS_KEY_ID=testkey -e S3_SECRET_ACCESS_KEY=testsecret123 \
  -e BACKUP_S3_BUCKET=nosuchbucket -e BACKUP_S3_ACCESS_KEY_ID=testkey -e BACKUP_S3_SECRET_ACCESS_KEY=testsecret123 \
  -e BACKUP_AGE_RECIPIENT="$pub" "$IMAGE" once >/dev/null 2>&1; then
  fail "a backup into a missing bucket reported success"
fi
[ "$(sql "SELECT ok::text FROM backup_runs ORDER BY id DESC LIMIT 1")" = "false" ] || fail "the failed run was not recorded as failed"

# ---- it refuses to start without a way to protect the dump, and with the vault as its own backup
if docker run --rm --network "$net" -e DATABASE_URL="postgres://cad:pw@$net-pg:5432/cad_portal?sslmode=disable" \
  -e BACKUP_S3_ENDPOINT="http://$net-s3:9000" -e BACKUP_S3_BUCKET=backups -e BACKUP_S3_ACCESS_KEY_ID=k -e BACKUP_S3_SECRET_ACCESS_KEY=s \
  "$IMAGE" once >/dev/null 2>&1; then
  fail "it ran without BACKUP_AGE_RECIPIENT"
fi
if svc -e BACKUP_S3_BUCKET=vault -- once >/dev/null 2>&1; then fail "it accepted the vault as the backup bucket"; fi

# ---- a bad time in the BACKUP_AT list stops the service with a message instead of running on a wrong schedule
if svc -e BACKUP_AT="02:00,25:00" -- run >/dev/null 2>&1; then fail "it accepted BACKUP_AT=02:00,25:00"; fi

echo "backup service OK"
