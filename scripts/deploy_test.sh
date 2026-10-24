#!/usr/bin/env bash
# Tests scripts/deploy.sh without Docker: a fake `docker` records every call, so we can check what the
# script would do. The point is the safety properties: the database is never taken down or wiped, a
# production version is backed up first, and bad input is refused before anything runs.
set -euo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin" "$work/stack"
touch "$work/stack/docker-compose.prod.yml" "$work/stack/.env"
log="$work/calls.log"

cat >"$work/bin/docker" <<'EOF'
#!/usr/bin/env bash
# fake docker: log the compose arguments after the -f flags; answer `config --services`.
args=("$@"); [ "${args[0]}" = compose ] && args=("${args[@]:1}")
while [ "${args[0]:-}" = -f ]; do args=("${args[@]:2}"); done
echo "${args[*]}" >>"$FAKE_DOCKER_LOG"
if [ "${args[0]}" = config ]; then printf '%s\n' db api web ${FAKE_BACKUP:+backup}; fi
# `ps -q --status running db`: a container id when the database is up, nothing when it is not.
if [ "${args[0]}" = ps ] && [ -z "${FAKE_DB_DOWN:-}" ]; then echo abc123def456; fi
if [ "${args[0]}" = run ] && [ -n "${FAKE_BACKUP_FAILS:-}" ]; then exit 1; fi
exit 0
EOF
chmod +x "$work/bin/docker"

fail() { echo "FAIL: $*" >&2; exit 1; }
deploy() { : >"$log"; PATH="$work/bin:$PATH" FAKE_DOCKER_LOG="$log" COMPOSE_DIR="$work/stack" scripts/deploy.sh "$@"; }
ran() { grep -q -- "$1" "$log"; }

# ---- staging: pull and restart the app containers only
FAKE_BACKUP=1 deploy staging >/dev/null || fail "staging deployment failed"
ran '^pull api web backup$' || fail "staging must pull the application images"
ran '^up -d --no-deps --wait api web backup$' || fail "staging must restart only the application services"
ran 'run --rm backup' && fail "staging must not take a backup (it runs every few minutes)"

# ---- never anything that could harm the database, in any mode
for mode in "staging" "1.2.3" "1.2.3-rc1"; do
  FAKE_BACKUP=1 deploy $mode >/dev/null || fail "deploying $mode failed"
  if grep -v '^config' "$log" | grep -Eq '(^| )(down|rm|stop|kill)( |$)|(^| )-v( |$)|--volumes|--remove-orphans'; then
    fail "$mode: a destructive command was issued: $(cat "$log")"
  fi
  # With the database running, nothing but the question "is it running?" may mention it.
  if grep -v '^config' "$log" | grep -v '^ps -q --status running db$' | grep -Eq '(^| )db( |$)'; then
    fail "$mode: the database service was named: $(cat "$log")"
  fi
  grep -q -- '--no-deps' "$log" || fail "$mode: up must use --no-deps so the database is left alone"
done

# ---- a new server (or a stopped database): the database is STARTED, never recreated, and before the app
FAKE_DB_DOWN=1 FAKE_BACKUP=1 deploy staging >/dev/null || fail "first deployment failed"
ran '^up -d --no-recreate --wait db$' || fail "a missing database must be started with --no-recreate: $(cat "$log")"
db_line=$(grep -n '^up -d --no-recreate --wait db$' "$log" | cut -d: -f1)
app_line=$(grep -n '^up -d --no-deps' "$log" | cut -d: -f1)
[ "$db_line" -lt "$app_line" ] || fail "the database must be started before the application services"
if grep -v '^config' "$log" | grep -Eq '(^| )(down|rm|stop|kill)( |$)|(^| )-v( |$)|--volumes|--remove-orphans|--force-recreate'; then
  fail "first deployment issued a destructive command: $(cat "$log")"
fi
# ...and with the database already running it is not started or touched at all
FAKE_BACKUP=1 deploy staging >/dev/null || fail "deployment failed"
ran '^up -d --no-recreate --wait db$' && fail "a running database must be left alone"

# ---- a version is backed up first, and the order is backup, pull, up
FAKE_BACKUP=1 deploy 1.2.3 >/dev/null || fail "versioned deployment failed"
backup_line=$(grep -n '^run --rm backup once$' "$log" | cut -d: -f1 || true)
pull_line=$(grep -n '^pull ' "$log" | cut -d: -f1 || true)
up_line=$(grep -n '^up ' "$log" | cut -d: -f1 || true)
[ -n "$backup_line" ] && [ -n "$pull_line" ] && [ -n "$up_line" ] || fail "missing a backup, pull or up step: $(cat "$log")"
[ "$backup_line" -lt "$pull_line" ] && [ "$pull_line" -lt "$up_line" ] || fail "the order must be backup, pull, up"

# ---- a failed backup stops everything
if FAKE_BACKUP=1 FAKE_BACKUP_FAILS=1 deploy 1.2.3 >/dev/null 2>&1; then fail "deployed although the backup failed"; fi
ran '^pull' && fail "pulled after a failed backup"
ran '^up' && fail "restarted after a failed backup"

# ---- without a backup service there is no safety net, unless the operator says so
if deploy 1.2.3 >/dev/null 2>&1; then fail "a version was deployed with no backup service and no --skip-backup"; fi
ran '^up' && fail "restarted with no backup possible"
deploy 1.2.3 --skip-backup >/dev/null || fail "--skip-backup should be allowed"
ran '^up -d --no-deps --wait api web$' || fail "--skip-backup should deploy api and web"

# ---- bad input is refused before anything runs
for bad in "" latest main "1.2" "1.2.3;rm" "1.2.3 x" "../1.2.3"; do
  if deploy "$bad" >/dev/null 2>&1; then fail "accepted '$bad'"; fi
  [ ! -s "$log" ] || fail "ran docker for the bad version '$bad'"
done
if deploy 1.2.3 --force >/dev/null 2>&1; then fail "accepted an unknown option"; fi

# ---- it needs the files it says it needs
rm "$work/stack/.env"
if deploy staging >/dev/null 2>&1; then fail "ran without a .env"; fi

echo "deploy script OK"
