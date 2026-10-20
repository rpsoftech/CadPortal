#!/usr/bin/env bash
# Self-test for changes.sh: builds a throwaway git repository, makes one change at a time
# and checks which jobs the script says to run. CI runs this before trusting the script,
# because a wrong "skip" would silently pass required checks.
set -eu

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$here/changes.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
# The repository lives in a subdirectory so the script's outputs never end up in the diff.
mkdir "$tmp/repo"
cd "$tmp/repo"

git init -q .
git config user.email ci@example.com
git config user.name ci
git config commit.gpgsign false
git config core.autocrlf false

seed=(
  README.md CLAUDE.md docs/notes.md .github/dependabot.yml docker-compose.yml docker-compose.prod.yml
  backend/go.mod backend/go.sum backend/internal/service/x.go backend/internal/service/x_test.go
  backend/internal/db/queries/q.sql backend/migrations/000001_init.up.sql backend/sqlc.yaml
  frontend/package.json frontend/yarn.lock frontend/src/app/a.ts frontend/src/app/a.spec.ts
)
for f in "${seed[@]}"; do mkdir -p "$(dirname "$f")"; echo "$f" > "$f"; done
git add -A && git commit -q -m base

failures=0
order="test sqlc frontend docker vulncheck"

# check NAME EXPECTED FILE...  -> change the files, run the script as a pull request, compare.
# EXPECTED is the subset of jobs that must be true, space separated ("-" for none).
check() {
  local name="$1" expected="$2"; shift 2
  local before; before="$(git rev-parse HEAD)"
  for f in "$@"; do mkdir -p "$(dirname "$f")"; echo "change $RANDOM" >> "$f"; done
  git add -A && git commit -q -m "$name"
  local out="$tmp/out.txt"; : > "$out"
  EVENT_NAME=pull_request BASE_SHA="$before" GITHUB_OUTPUT="$out" bash "$script" > "$tmp/log.txt" 2>&1
  local got=""
  for j in $order; do grep -qx "$j=true" "$out" && got="$got $j"; done
  got="${got# }"; [ -z "$got" ] && got="-"
  if [ "$got" = "$expected" ]; then
    printf 'ok    %-34s -> %s\n' "$name" "$got"
  else
    printf 'FAIL  %-34s -> got [%s], want [%s]\n' "$name" "$got" "$expected"
    failures=$((failures + 1))
  fi
}

# run EVENT BASE EXPECTED NAME -> an event where no diff is involved.
run() {
  local event="$1" base="$2" expected="$3" name="$4"
  local out="$tmp/out.txt"; : > "$out"
  EVENT_NAME="$event" BASE_SHA="$base" GITHUB_OUTPUT="$out" bash "$script" > "$tmp/log.txt" 2>&1
  local got=""
  for j in $order; do grep -qx "$j=true" "$out" && got="$got $j"; done
  got="${got# }"
  if [ "$got" = "$expected" ]; then printf 'ok    %-34s -> %s\n' "$name" "$got"
  else printf 'FAIL  %-34s -> got [%s], want [%s]\n' "$name" "$got" "$expected"; failures=$((failures + 1)); fi
}

all="test sqlc frontend docker vulncheck"

# Changes that need no job.
check "readme only"               "-"  README.md
check "docs folder"               "-"  docs/notes.md
check "CLAUDE.md"                 "-"  CLAUDE.md
check "dependabot config"         "-"  .github/dependabot.yml
check "dev compose file"          "-"  docker-compose.yml

# Frontend.
check "frontend source"           "frontend docker"  frontend/src/app/a.ts
check "frontend spec only"        "frontend"         frontend/src/app/a.spec.ts
check "frontend lockfile"         "frontend docker"  frontend/yarn.lock

# Backend.
check "backend source"            "test docker vulncheck"  backend/internal/service/x.go
check "backend test only"         "test"                   backend/internal/service/x_test.go
check "backend go.mod"            "test docker vulncheck"  backend/go.mod
check "migration"                 "test sqlc docker"       backend/migrations/000002_add.up.sql
check "sqlc query"                "test sqlc docker"       backend/internal/db/queries/q.sql
check "sqlc config"               "test sqlc docker"       backend/sqlc.yaml

# Deployment files, and combinations.
check "production compose"        "docker"  docker-compose.prod.yml
check "docs + frontend"           "frontend docker"  README.md frontend/src/app/a.ts
check "frontend + backend source"    "test frontend docker vulncheck"  frontend/src/app/a.ts backend/internal/service/x.go
check "backend + sqlc + frontend" "$all"   backend/migrations/000003.up.sql frontend/src/app/a.ts backend/internal/service/x.go

# The pipeline itself re-validates everything.
check "ci workflow"               "$all"   .github/workflows/ci.yml
check "ci helper script"          "$all"   .github/scripts/changes.sh

# Events and situations where the answer is uncertain: run everything.
head="$(git rev-parse HEAD)"
run workflow_dispatch "$head" "$all" "manual run"
run schedule          "$head" "$all" "scheduled run"
run push 0000000000000000000000000000000000000000 "$all" "push of a new branch (zero base)"
run push ""           "$all" "push with no base"
run push deadbeefdeadbeefdeadbeefdeadbeefdeadbeef "$all" "push with an unknown base commit"
run pull_request "$(git rev-parse HEAD~1)" "$all" "last change was the CI script"

if [ "$failures" -ne 0 ]; then
  echo "$failures scenario(s) failed" >&2
  exit 1
fi
echo "all scenarios passed"
