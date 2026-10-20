#!/usr/bin/env bash
# Restore a dump made by scripts/backup.sh, REPLACING the contents of the database.
#
#   scripts/restore.sh backups/cad_portal-20260101-020000.dump          # asks first
#   scripts/restore.sh backups/cad_portal-20260101-020000.dump --yes    # no prompt
#
# The API is stopped during the restore (so nothing writes while tables are rebuilt) and
# started again afterwards if it was running. The restore is one transaction: if it fails,
# the database is left exactly as it was.
#
# Environment (all optional): COMPOSE_FILE, DB_NAME, as for backup.sh.
set -euo pipefail
cd "$(dirname "$0")/.."

file=${1:-}
yes=${2:-}
COMPOSE_FILE=${COMPOSE_FILE:-docker-compose.prod.yml}

if [ -z "$file" ] || [ ! -f "$file" ]; then
  echo "usage: scripts/restore.sh <dump file> [--yes]" >&2
  exit 2
fi

dc() { docker compose -f "$COMPOSE_FILE" "$@"; }
in_db() { dc exec -T -e "DB_NAME=${DB_NAME:-}" db sh -c 'TARGET_DB=${DB_NAME:-$POSTGRES_DB}; '"$1"; }

if ! in_db 'pg_restore --list >/dev/null' <"$file"; then
  echo "$file is not a readable pg_dump custom-format file" >&2
  exit 1
fi

target=$(in_db 'echo "$TARGET_DB"')
if [ "$yes" != "--yes" ]; then
  printf 'This replaces everything in database "%s" with %s.\nType the database name to continue: ' "$target" "$file"
  read -r answer
  [ "$answer" = "$target" ] || { echo "aborted"; exit 1; }
fi

api_was_running=
if dc ps --status running --services | grep -qx api; then
  api_was_running=1
  dc stop api
fi

status=0
in_db 'pg_restore --clean --if-exists --no-owner --single-transaction -U "$POSTGRES_USER" -d "$TARGET_DB"' <"$file" || status=$?

if [ -n "$api_was_running" ]; then
  dc start api
fi
if [ "$status" -ne 0 ]; then
  echo "restore failed (exit $status); the database was not changed" >&2
  exit "$status"
fi
echo "restored $file into $target"
