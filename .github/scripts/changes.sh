#!/usr/bin/env bash
# Decides which CI jobs a change needs, so a docs-only or frontend-only change does not
# pay for the backend tests, and vice versa. Writes one "job=true|false" line per job to
# $GITHUB_OUTPUT (or stdout when run by hand).
#
# Inputs (environment):
#   EVENT_NAME  github.event_name
#   BASE_SHA    commit to diff against (the PR base, or the push's "before" commit)
#   HEAD_REF    commit to diff to (default HEAD)
#
# Safety rule: whenever the answer is uncertain (no usable base, diff failure, manual or
# scheduled run, or the CI definition itself changed) every job runs. A skipped job counts
# as passing for required checks, so wrongly skipping would be worse than running too much.
set -u

out="${GITHUB_OUTPUT:-/dev/stdout}"
event="${EVENT_NAME:-}"
base="${BASE_SHA:-}"
head="${HEAD_REF:-HEAD}"
jobs=(test sqlc frontend docker vulncheck)

emit() { printf '%s=%s\n' "$1" "$2" >> "$out"; }

run_all() {
  echo "Running every job: $1"
  for j in "${jobs[@]}"; do emit "$j" true; done
  exit 0
}

case "$event" in
  workflow_dispatch | schedule) run_all "$event run" ;;
esac

# A new branch or a force push has no usable "before" commit (all zeros or unknown).
if [ -z "$base" ] || [ -z "${base//0/}" ] || ! git cat-file -e "${base}^{commit}" 2>/dev/null; then
  run_all "no usable base commit"
fi

files=$(git diff --name-only "$base" "$head" 2>/dev/null) || run_all "could not diff against the base"

echo "Changed files:"
if [ -z "$files" ]; then echo "  (none)"; else printf '%s\n' "$files" | sed 's/^/  /'; fi

# match PATTERN: any changed file matches. nontest PATTERN: same, ignoring test-only files.
match() { printf '%s\n' "$files" | grep -Eq "$1"; }
nontest() { printf '%s\n' "$files" | grep -Ev '(_test\.go|\.spec\.ts)$' | grep -Eq "$1"; }

# Changing the pipeline or its helper scripts re-validates everything.
if match '^\.github/(workflows/ci\.yml|scripts/)'; then run_all "the CI definition changed"; fi

decide() { if "$@"; then echo true; else echo false; fi; }

emit test      "$(decide match '^backend/')"
emit sqlc      "$(decide match '^backend/(internal/db/|migrations/|sqlc\.yaml$)')"
emit vulncheck "$(decide nontest '^backend/(go\.(mod|sum)$|.*\.go$)')"
emit frontend  "$(decide match '^frontend/')"
# Images are built from non-test sources plus the production compose file.
emit docker    "$(decide nontest '^(backend/|frontend/|docker-compose\.prod\.yml$)')"
