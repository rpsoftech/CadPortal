#!/usr/bin/env bash
# Proves a backup can be restored: take a dump, damage the database, restore, and check the
# damage is gone. Needs a running stack with a db service.
#
#   COMPOSE_FILE=docker-compose.prod.yml scripts/backup_restore_test.sh
#
# It adds and drops one small table (backup_probe) in the target database and leaves
# everything else alone. It never touches a backups/ directory: dumps go to a temp dir.
set -euo pipefail
cd "$(dirname "$0")/.."

export COMPOSE_FILE=${COMPOSE_FILE:-docker-compose.prod.yml}
export BACKUP_DIR
BACKUP_DIR=$(mktemp -d)
trap 'rm -rf "$BACKUP_DIR"' EXIT

sql() { docker compose -f "$COMPOSE_FILE" exec -T -e "DB_NAME=${DB_NAME:-}" db sh -c 'psql -v ON_ERROR_STOP=1 -At -U "$POSTGRES_USER" -d "${DB_NAME:-$POSTGRES_DB}" -c "$1"' sh "$1"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

sql "DROP TABLE IF EXISTS backup_probe; CREATE TABLE backup_probe (v text); INSERT INTO backup_probe VALUES ('before')" >/dev/null

KEEP=2 scripts/backup.sh
dump=$(ls "$BACKUP_DIR"/cad_portal-*.dump)

sql "DROP TABLE backup_probe" >/dev/null
[ "$(sql "SELECT to_regclass('backup_probe') IS NULL")" = "t" ] || fail "setup: the probe table should be gone"

scripts/restore.sh "$dump" --yes
[ "$(sql "SELECT v FROM backup_probe")" = "before" ] || fail "restore did not bring the probe table back"

# A wrong confirmation must abort without changing anything.
sql "UPDATE backup_probe SET v = 'after'" >/dev/null
if echo nope | scripts/restore.sh "$dump" >/dev/null 2>&1; then fail "restore ran without confirmation"; fi
[ "$(sql "SELECT v FROM backup_probe")" = "after" ] || fail "an aborted restore changed the database"

# An unreadable file must be refused before anything is touched.
echo not-a-dump >"$BACKUP_DIR/bad.dump"
if scripts/restore.sh "$BACKUP_DIR/bad.dump" --yes >/dev/null 2>&1; then fail "restore accepted a bad file"; fi
[ "$(sql "SELECT v FROM backup_probe")" = "after" ] || fail "a refused restore changed the database"

# Retention keeps only the newest KEEP dumps.
for _ in 1 2 3; do sleep 1; KEEP=2 scripts/backup.sh >/dev/null; done
[ "$(ls "$BACKUP_DIR"/cad_portal-*.dump | wc -l | tr -d ' ')" = "2" ] || fail "retention did not prune to 2"

sql "DROP TABLE backup_probe" >/dev/null
echo "backup/restore round trip OK"
