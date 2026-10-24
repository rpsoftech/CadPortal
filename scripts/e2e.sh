#!/usr/bin/env bash
# Browser smoke tests (Playwright) against a running production-style stack.
#
#   docker compose -f docker-compose.prod.yml up -d --build --wait   # with STORAGE=local
#   scripts/e2e.sh
#
# Creates two throwaway users in the stack's database (an admin and a production user) with
# random passwords, then runs frontend/e2e. The tests create designs (E2E001, E2E002), so run
# them against a stack you can throw away, never a real catalog.
#
# Environment (all optional):
#   COMPOSE_FILE   compose file of the running stack   (default docker-compose.prod.yml)
#   E2E_BASE_URL   where the web container listens     (default http://127.0.0.1:${WEB_PORT:-8080})
#   E2E_A11Y_WORKERS  parallel workers for the accessibility scans (default 4)
#
# With no arguments it runs everything in two steps: the `main` project (tests that create data,
# one worker, in file order), then the `a11y` project (read-only axe scans, in parallel). Arguments
# are passed to `playwright test` as they are, for a subset:
#   scripts/e2e.sh smoke.zip                       one file (needs the data smoke.e2e.ts creates)
#   scripts/e2e.sh --project=main -g "job bag"     tests by title
#   scripts/e2e.sh --project=a11y --no-deps        only the accessibility scans, on data already there
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE_FILE=${COMPOSE_FILE:-docker-compose.prod.yml}
export E2E_BASE_URL=${E2E_BASE_URL:-http://127.0.0.1:${WEB_PORT:-8080}}
export E2E_ADMIN_EMAIL=e2e-admin@example.com
export E2E_PROD_EMAIL=e2e-production@example.com
E2E_ADMIN_PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -d '/+=\n')
E2E_PROD_PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -d '/+=\n')
export E2E_ADMIN_PASSWORD E2E_PROD_PASSWORD

add_user() { # email, role, password
  printf '%s' "$3" | docker compose -f "$COMPOSE_FILE" exec -T api /app/adduser \
    -email "$1" -name "E2E $2" -role "$2" -password-stdin >/dev/null
}
add_user "$E2E_ADMIN_EMAIL" admin "$E2E_ADMIN_PASSWORD"
add_user "$E2E_PROD_EMAIL" production "$E2E_PROD_PASSWORD"

cd frontend
if [ "$#" -gt 0 ]; then
  exec yarn playwright test "$@"
fi
rc=0
yarn playwright test --project=main || rc=$?
# The scans run even after a failure above, so one run reports everything that is wrong.
PLAYWRIGHT_HTML_OUTPUT_DIR=playwright-report-a11y \
  yarn playwright test --project=a11y --no-deps --workers="${E2E_A11Y_WORKERS:-4}" || rc=$?
exit "$rc"
