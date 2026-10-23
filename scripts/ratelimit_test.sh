#!/usr/bin/env bash
# Checks the web container's sign-in rate limit against a running stack:
#
#   WEB_PORT=18080 scripts/ratelimit_test.sh
#
# It sends bad sign-ins for an account that does not exist (so no real account is locked),
# pretending to be one client address through X-Forwarded-For, and expects the limit to start
# answering 429 while a different client address is still answered normally.
set -euo pipefail

base=${E2E_BASE_URL:-http://127.0.0.1:${WEB_PORT:-8080}}
login() { # client address -> prints the HTTP status
  curl -s -o /dev/null -w '%{http_code}' -X POST "$base/api/v1/auth/login" \
    -H 'Content-Type: application/json' -H "X-Forwarded-For: $1" \
    -d '{"email":"nobody-ratelimit@example.com","password":"wrong-password-0000"}'
}
fail() { echo "FAIL: $*" >&2; exit 1; }

ok=0 limited=0
for _ in $(seq 1 30); do
  case $(login 203.0.113.50) in
    401) ok=$((ok + 1)) ;;
    429) limited=$((limited + 1)) ;;
    *) fail "unexpected status from the sign-in endpoint" ;;
  esac
done
echo "client A: $ok answered, $limited limited"
[ "$ok" -ge 20 ] || fail "the limit bit too early ($ok answered)"
[ "$limited" -ge 1 ] || fail "30 rapid sign-ins were never limited"

[ "$(login 203.0.113.51)" = "401" ] || fail "a different client address was limited too"
echo "client B: still answered normally"

# The limit covers sign-in only.
code=$(curl -s -o /dev/null -w '%{http_code}' -H 'X-Forwarded-For: 203.0.113.50' "$base/api/v1/designs")
[ "$code" = "401" ] || fail "other API routes should not be limited (got $code)"
echo "sign-in rate limit OK"

# "Forgot password" is public too, so it has its own limit (5 a minute, burst of 10).
forgot() { # client address -> prints the HTTP status
  curl -s -o /dev/null -w '%{http_code}' -X POST "$base/api/v1/auth/forgot-password" \
    -H 'Content-Type: application/json' -H "X-Forwarded-For: $1" -d '{"email":"nobody-ratelimit@example.com"}'
}
ok=0 limited=0
for _ in $(seq 1 30); do
  case $(forgot 203.0.113.60) in
    202) ok=$((ok + 1)) ;;
    429) limited=$((limited + 1)) ;;
    *) fail "unexpected status from the forgot-password endpoint" ;;
  esac
done
echo "forgot-password, client A: $ok answered, $limited limited"
[ "$ok" -ge 10 ] || fail "the forgot-password limit bit too early ($ok answered)"
[ "$limited" -ge 1 ] || fail "30 rapid forgot-password requests were never limited"
[ "$(forgot 203.0.113.61)" = "202" ] || fail "a different client address was limited too"
echo "forgot-password rate limit OK"
