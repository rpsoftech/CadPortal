#!/usr/bin/env bash
# Pull published images and restart only the application containers. The database container and its
# volume are never touched: no `down`, no `-v`, no `rm`, and `--no-deps` keeps compose from recreating
# the database when only the images changed.
#
#   scripts/deploy.sh staging           # the newest build of main (the :staging images)
#   scripts/deploy.sh 1.2.3             # a released version (a v1.2.3 tag), production
#   scripts/deploy.sh 1.2.3 --skip-backup
#
# Staging is meant to run from a timer (deploy/systemd): it does nothing when nothing changed.
# A version deployment takes a backup first, because migrations run when the new API starts and cannot
# be undone by starting the old image again.
#
# Environment (all optional):
#   COMPOSE_DIR   the directory with docker-compose.prod.yml and .env   (default: this repository)
set -euo pipefail

target=${1:-}
skip_backup=false
[ "${2:-}" = "--skip-backup" ] && skip_backup=true

fail() { echo "deploy: $*" >&2; exit 1; }

case $target in
  staging) ;;
  [0-9]*.[0-9]*.[0-9]*) case $target in *[!0-9A-Za-z.-]*) fail "'$target' is not a version" ;; esac ;;
  *) fail "usage: scripts/deploy.sh staging | X.Y.Z [--skip-backup]" ;;
esac
[ -z "${2:-}" ] || [ "${2:-}" = "--skip-backup" ] || fail "unknown option '$2'"

cd "${COMPOSE_DIR:-$(dirname "$0")/..}"
[ -f docker-compose.prod.yml ] || fail "docker-compose.prod.yml not found in $(pwd)"
[ -f .env ] || fail "no .env here: copy .env.example and fill it in first"

# Two runs at once (a slow pull and the next timer tick) must not race.
if command -v flock >/dev/null 2>&1; then
  exec 9>.deploy.lock
  flock -n 9 || { echo "deploy: another deployment is running; nothing to do"; exit 0; }
fi

export CADPORTAL_VERSION=$target
dc() { docker compose -f docker-compose.prod.yml -f docker-compose.images.yml "$@"; }

# Which of the application services this installation runs (backup only if its profile is on).
services=()
for s in api web backup; do
  if dc config --services | grep -qx "$s"; then services+=("$s"); fi
done
[ "${#services[@]}" -gt 0 ] || fail "no application services found in the compose files"

echo "deploy: $target -> ${services[*]}"
echo "deploy: running now:"
dc images "${services[@]}" 2>/dev/null || true

if [ "$target" != staging ] && [ "$skip_backup" = false ]; then
  if printf '%s\n' "${services[@]}" | grep -qx backup; then
    echo "deploy: taking a backup first"
    dc run --rm backup once || fail "the backup failed, so nothing was changed (--skip-backup overrides, at your own risk)"
  else
    fail "the backup service is not enabled (COMPOSE_PROFILES=backup), so there is no backup to fall back on: enable it, or pass --skip-backup"
  fi
fi

dc pull "${services[@]}"
# Compose recreates only the services whose image changed; the others, and the database, stay up.
dc up -d --no-deps --wait "${services[@]}"

echo "deploy: running now:"
dc images "${services[@]}" 2>/dev/null || true
echo "deploy: done. To go back, run this script with the earlier version. That is only safe if the newer"
echo "deploy: release added no database migration; otherwise restore the backup taken above."
