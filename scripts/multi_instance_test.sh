#!/usr/bin/env bash
# Proves the production stack works with more than one API instance. It builds and starts the stack with
# two api containers (`docker compose up --scale api=2`) on an empty database, so both run the migrations
# at the same moment, then checks that
#   - both instances are healthy and the migrations ran once, cleanly;
#   - the web container (nginx) really spreads requests over both;
#   - a token from a sign-in on one instance works on the other (the token is stateless);
#   - 60 designs created in parallel get 60 different codes, with no error and no deadlock;
#   - several metal edits (each queues a weight rebuild, and both instances run a worker) end with every
#     job done exactly once and none failed.
#
#   scripts/multi_instance_test.sh
#
# Uses its own compose project, so it never touches another stack, and removes it (with its volumes) at
# the end. Environment (all optional): WEB_PORT (default 18090), COMPOSE_PROJECT_NAME.
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1 # Git Bash on Windows must not rewrite /app/adduser

export COMPOSE_PROJECT_NAME=${COMPOSE_PROJECT_NAME:-cadportal-multi}
export WEB_PORT=${WEB_PORT:-18090}
export STORAGE=local # one host, one shared uploads volume: fine for this test (use R2 across hosts)
POSTGRES_PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -d '/+=\n')
JWT_SECRET=$(head -c 36 /dev/urandom | base64 | tr -d '/+=\n')
export POSTGRES_PASSWORD JWT_SECRET
export S3_ENDPOINT= S3_BUCKET= S3_ACCESS_KEY_ID= S3_SECRET_ACCESS_KEY= HEARTBEAT_URL= \
  SMTP_HOST= BACKUP_S3_BUCKET= BACKUP_VIEW_ACCESS_KEY_ID= BACKUP_VIEW_SECRET_ACCESS_KEY=
unset COMPOSE_PROFILES
COMPOSE=(docker compose -f docker-compose.prod.yml)
base=http://127.0.0.1:$WEB_PORT
tmp=$(mktemp -d)

fail() { echo "FAIL: $*" >&2; "${COMPOSE[@]}" logs --no-color --tail 60 api web >&2 || true; exit 1; }
cleanup() { rm -rf "$tmp"; "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT
trap 'echo "FAIL: command failed at line $LINENO" >&2' ERR

sql() { "${COMPOSE[@]}" exec -T db psql -v ON_ERROR_STOP=1 -At -U cad -d cad_portal -c "$1"; }
count_in_logs() { # container id, fixed string -> number of matching log lines
  docker logs "$1" 2>&1 | grep -cF -- "$2" || true
}

echo "== start two API instances on an empty database"
"${COMPOSE[@]}" up -d --build --scale api=2 --wait --wait-timeout 240 || fail "the stack did not come up healthy with two API instances"

ids=$("${COMPOSE[@]}" ps -q api)
[ "$(echo "$ids" | wc -l | tr -d ' ')" = "2" ] || fail "expected two api containers"
for id in $ids; do
  [ "$(docker inspect -f '{{.State.Health.Status}}' "$id")" = "healthy" ] || fail "api container $id is not healthy"
  if docker logs "$id" 2>&1 | grep -qiE 'migrate up|dirty|deadlock|panic'; then fail "migration trouble in the log of $id"; fi
done
[ "$(sql 'SELECT dirty FROM schema_migrations')" = "f" ] || fail "schema_migrations is dirty"
echo "both healthy, migrations clean (version $(sql 'SELECT version FROM schema_migrations'))"

# nginx resolves `api` per request: it must see both addresses (Docker DNS round robin).
set -- $ids
first=$1 second=$2

echo "== requests are spread over both instances"
b1=$(count_in_logs "$first" '"path":"/api/v1/auth/config"')
b2=$(count_in_logs "$second" '"path":"/api/v1/auth/config"')
for _ in $(seq 1 60); do curl -fsS "$base/api/v1/auth/config" >/dev/null; done
a1=$(count_in_logs "$first" '"path":"/api/v1/auth/config"')
a2=$(count_in_logs "$second" '"path":"/api/v1/auth/config"')
echo "instance 1 served $((a1 - b1)), instance 2 served $((a2 - b2)) of 60"
[ $((a1 - b1)) -ge 10 ] && [ $((a2 - b2)) -ge 10 ] || fail "web did not spread requests over both instances"
[ $((a1 - b1 + a2 - b2)) -eq 60 ] || fail "some requests were not served by either instance"

echo "== a token from one instance works on the other"
pw=$(head -c 24 /dev/urandom | base64 | tr -d '/+=\n')
printf '%s' "$pw" | "${COMPOSE[@]}" exec -T api /app/adduser -email multi@example.com -name Multi -role admin -password-stdin >/dev/null
login=$(curl -fsS -X POST "$base/api/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"email\":\"multi@example.com\",\"password\":\"$pw\"}")
token=$(echo "$login" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
[ -n "$token" ] || fail "sign-in returned no token"
auth=(-H "Authorization: Bearer $token")
m1=$(count_in_logs "$first" '"path":"/api/v1/auth/me"')
m2=$(count_in_logs "$second" '"path":"/api/v1/auth/me"')
for _ in $(seq 1 30); do
  [ "$(curl -s -w '%{http_code}' "${auth[@]}" "$base/api/v1/auth/me" | tail -c 3)" = "200" ] || fail "token rejected"
done
n1=$(($(count_in_logs "$first" '"path":"/api/v1/auth/me"') - m1))
n2=$(($(count_in_logs "$second" '"path":"/api/v1/auth/me"') - m2))
echo "token accepted by both: instance 1 answered $n1, instance 2 answered $n2 of 30"
[ "$n1" -ge 1 ] && [ "$n2" -ge 1 ] || fail "the token was not exercised on both instances"

echo "== 60 designs created in parallel"
sql "UPDATE categories SET code_prefix = 'MI' WHERE name = 'Rings'" >/dev/null
cat_id=$(sql "SELECT id FROM categories WHERE name = 'Rings'")
for i in $(seq 1 60); do
  (curl -s -o "$tmp/out" -w '%{http_code}\n' -X POST "$base/api/v1/designs" "${auth[@]}" \
    -H 'Content-Type: application/json' -d "{\"category_id\":$cat_id,\"name\":\"parallel $i\"}" | tail -n 1 >"$tmp/create-$i") &
done
wait
bad=$(cat "$tmp"/create-* | grep -vc '^201$' || true)
[ "$bad" = "0" ] || fail "$bad of 60 parallel creations did not answer 201"
[ "$(sql "SELECT count(*) || '/' || count(DISTINCT code) FROM master_designs WHERE name LIKE 'parallel %'")" = "60/60" ] \
  || fail "duplicate or missing design codes"
echo "60 created, 60 distinct codes ($(sql "SELECT min(code) || '..' || max(code) FROM master_designs WHERE name LIKE 'parallel %'"))"

echo "== weight rebuilds queued back to back"
for sg in 17.76 17.77 17.78 17.79 17.80 17.75; do
  code=$(curl -s -w '%{http_code}' -X PUT "$base/api/v1/masters/metals/1" "${auth[@]}" \
    -H 'Content-Type: application/json' -d "{\"specific_gravity\":$sg,\"is_default_input\":false,\"active\":true}" | tail -c 3)
  [ "$code" = "200" ] || fail "metal edit answered $code"
done
for _ in $(seq 1 60); do
  [ "$(sql "SELECT count(*) FROM weight_recalc_jobs WHERE status IN ('pending','running')")" = "0" ] && break
  sleep 1
done
[ "$(sql "SELECT count(*) FROM weight_recalc_jobs WHERE status IN ('pending','running')")" = "0" ] || fail "rebuild jobs never finished"
[ "$(sql "SELECT count(*) FROM weight_recalc_jobs WHERE status = 'failed'")" = "0" ] || fail "a rebuild job failed"
echo "$(sql "SELECT count(*) FROM weight_recalc_jobs WHERE status = 'done'") jobs done, none failed"

echo "== no deadlocks or panics in either log"
for id in $ids; do
  if docker logs "$id" 2>&1 | grep -qiE 'deadlock|panic|duplicate key'; then fail "deadlock/panic/duplicate key in the log of $id"; fi
done
echo "multi-instance OK"
