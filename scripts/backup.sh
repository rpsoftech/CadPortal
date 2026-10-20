#!/usr/bin/env bash
# Dump the portal database to backups/cad_portal-<timestamp>.dump (pg_dump custom format,
# already compressed) and prune old dumps. Run it from cron or a systemd timer.
#
#   scripts/backup.sh
#
# Environment (all optional):
#   COMPOSE_FILE  compose file that defines the db service   (default docker-compose.prod.yml)
#   BACKUP_DIR    where dumps are written                    (default backups)
#   KEEP          newest dumps to keep, 0 keeps everything   (default 14)
#   DB_NAME       database to dump                           (default the container's POSTGRES_DB)
#
# Dumps hold every user's password hash and the whole audit log: keep them as private as .env.
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE_FILE=${COMPOSE_FILE:-docker-compose.prod.yml}
BACKUP_DIR=${BACKUP_DIR:-backups}
KEEP=${KEEP:-14}

case $KEEP in '' | *[!0-9]*) echo "KEEP must be a whole number, got '$KEEP'" >&2; exit 2 ;; esac

dc() { docker compose -f "$COMPOSE_FILE" "$@"; }
# Runs a command in the db container with the target database name available as $TARGET_DB.
in_db() { dc exec -T -e "DB_NAME=${DB_NAME:-}" db sh -c 'TARGET_DB=${DB_NAME:-$POSTGRES_DB}; '"$1"; }

umask 077
mkdir -p "$BACKUP_DIR"
stamp=$(date +%Y%m%d-%H%M%S)
out="$BACKUP_DIR/cad_portal-$stamp.dump"
tmp="$out.partial"
trap 'rm -f "$tmp"' EXIT

in_db 'pg_dump -Fc -U "$POSTGRES_USER" -d "$TARGET_DB"' >"$tmp"

# A dump that cannot be read back is not a backup: check it before it replaces anything.
if [ ! -s "$tmp" ] || ! in_db 'pg_restore --list >/dev/null' <"$tmp"; then
  echo "backup failed: the dump is empty or unreadable" >&2
  exit 1
fi
mv "$tmp" "$out"
echo "wrote $out ($(wc -c <"$out" | tr -d ' ') bytes)"

if [ "$KEEP" -gt 0 ]; then
  # Names sort by timestamp, so everything after the newest $KEEP is old.
  ls -1 "$BACKUP_DIR"/cad_portal-*.dump | sort -r | tail -n +"$((KEEP + 1))" | while read -r old; do
    rm -f -- "$old"
    echo "pruned $old"
  done
fi
