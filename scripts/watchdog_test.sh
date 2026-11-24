#!/usr/bin/env bash
# Tests scripts/watchdog.sh with fake df, docker and curl: no real server, network or Docker needed.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/state" "$work/compose"
fails=0
fail() { echo "FAIL: $*"; fails=$((fails+1)); }
ok() { echo "ok:   $*"; }

# fake commands, steered by files in $work
cat > "$work/bin/df" <<'S'
#!/usr/bin/env bash
echo "Filesystem 1024-blocks Used Available Capacity Mounted on"
echo "/dev/x 100 1 1 $(cat "$WORK/disk")% /"
S
cat > "$work/bin/docker" <<'S'
#!/usr/bin/env bash
cat "$WORK/containers"
S
cat > "$work/bin/curl" <<'S'
#!/usr/bin/env bash
# the portal's own health probe is a plain GET of /healthz; everything else is a message and is logged
for a in "$@"; do
  case $a in */healthz) [ "$(cat "$WORK/healthz")" = up ] && exit 0 || exit 22 ;; esac
done
input=""
for a in "$@"; do [ "$a" = "-" ] && input=$(cat); done
echo "SEND url=[$input] args=[$*]" >> "$WORK/sent"
[ -f "$WORK/sendfail" ] && exit 22
exit 0
S
chmod +x "$work"/bin/*
export WORK="$work" PATH="$work/bin:$PATH"
cat > "$work/meminfo" <<'S'
MemTotal:       1000000 kB
MemAvailable:    600000 kB
S
reset() { echo 40 > "$work/disk"; echo up > "$work/healthz"; : > "$work/sent"; rm -f "$work/sendfail"
  printf 'api|running|Up 2 hours (healthy)\nweb|running|Up 2 hours (healthy)\ndb|running|Up 2 hours (healthy)\n' > "$work/containers"
  rm -rf "$work/state"/*; }
printf 'TELEGRAM_BOT_TOKEN=123:SECRET\nTELEGRAM_CHAT_ID=-100\nALERT_WEBHOOK_URL=https://hooks.example/h-SECRETPATH\nALERT_SERVER_NAME=staging\n' > "$work/compose/.env"
run() { COMPOSE_DIR="$work/compose" WATCHDOG_STATE_DIR="$work/state" MEMINFO_FILE="$work/meminfo" WATCHDOG_NOW=${NOW:-1000} bash "$here/watchdog.sh" "$@" 2>&1; }
sent() { grep -c '^SEND' "$work/sent"; }

reset; out=$(run); [ "$(sent)" = 0 ] && ok "all well: nothing is sent" || fail "all well sent something: $(cat "$work/sent")"

reset; echo 96 > "$work/disk"; run >/dev/null
[ "$(sent)" = 2 ] && ok "disk 96%: one message to telegram and one to the webhook" || fail "disk 96% sent $(sent)"
grep -q 'chat_id=-100' "$work/sent" && grep -q 'staging' "$work/sent" && grep -q 'Disk is 96% full' "$work/sent" && ok "the message names the server and the problem" || fail "message content: $(cat "$work/sent")"
grep -q 'critical' "$work/sent" && ok "96% is critical" || fail "not critical"
grep -q 'SECRET' <(grep -o 'args=\[.*' "$work/sent") && fail "token or webhook path is in the command line" || ok "token and webhook path are kept out of the command line"

NOW=1100 run >/dev/null; [ "$(sent)" = 2 ] && ok "still full a little later: not repeated" || fail "repeated: $(sent)"
NOW=$((1000 + 7*3600)) run >/dev/null; [ "$(sent)" = 4 ] && ok "still full after 6 hours: reminded" || fail "no reminder: $(sent)"
echo 50 > "$work/disk"; NOW=$((1000 + 8*3600)) run >/dev/null
[ "$(sent)" = 6 ] && grep -q 'Disk is 50% full\|Disk is' "$work/sent" && ok "back to normal: one recovery message" || fail "recovery: $(sent)"
NOW=$((1000 + 9*3600)) run >/dev/null; [ "$(sent)" = 6 ] && ok "after recovery: silent" || fail "chatter after recovery: $(sent)"

reset; echo 88 > "$work/disk"; run >/dev/null; n1=$(sent); echo 97 > "$work/disk"; NOW=1100 run >/dev/null
[ "$n1" = 2 ] && [ "$(sent)" = 4 ] && ok "warning that becomes critical is sent again at once" || fail "escalation: $n1 then $(sent)"

reset; printf 'api|running|Up 1 minute (unhealthy)\nweb|running|Up 2 hours (healthy)\ndb|exited|Exited (1) 3 minutes ago\n' > "$work/containers"; run >/dev/null
grep -q 'api: Up 1 minute (unhealthy)' "$work/sent" && grep -q 'db: Exited' "$work/sent" && ok "unhealthy and stopped containers are named" || fail "containers: $(cat "$work/sent")"
reset; printf 'grafana|exited|Exited (1)\nweb|running|Up (healthy)\n' > "$work/containers"; run >/dev/null
[ "$(sent)" = 0 ] && ok "containers that are not the portal's are ignored" || fail "ignored container alerted"

reset; echo down > "$work/healthz"; run >/dev/null
grep -q 'The portal does not answer' "$work/sent" && ok "a dead web address is reported" || fail "healthz down: $(cat "$work/sent")"

reset; printf 'MemTotal: 1000 kB\nMemAvailable: 30 kB\n' > "$work/meminfo"; run >/dev/null
grep -q 'Memory is 97% used' "$work/sent" && grep -q critical "$work/sent" && ok "memory almost gone is critical" || fail "memory: $(cat "$work/sent")"
printf 'MemTotal:       1000000 kB\nMemAvailable:    600000 kB\n' > "$work/meminfo"

reset; echo 96 > "$work/disk"; printf 'TELEGRAM_BOT_TOKEN=1:S\nTELEGRAM_CHAT_ID=1\nALERT_MIN_LEVEL=critical\n' > "$work/compose/.env"; run >/dev/null
[ "$(sent)" = 1 ] && ok "only Telegram configured: one message" || fail "telegram only: $(sent)"
reset; echo 88 > "$work/disk"; run >/dev/null; [ "$(sent)" = 0 ] && ok "ALERT_MIN_LEVEL=critical drops warnings" || fail "min level ignored"

reset; echo 96 > "$work/disk"; touch "$work/sendfail"; out=$(run); echo "$out" | grep -q 'failed' && ! echo "$out" | grep -q 'SECRET\|1:S' && ok "a failed send is reported without the token" || fail "send failure output: $out"

: > "$work/compose/.env"; out=$(run --test); echo "$out" | grep -q 'no TELEGRAM_BOT_TOKEN' && ok "--test without settings explains what is missing" || fail "--test: $out"
printf 'TELEGRAM_BOT_TOKEN=1:S\nTELEGRAM_CHAT_ID=1\n' > "$work/compose/.env"; reset; run --test >/dev/null
grep -q 'Test message' "$work/sent" && ok "--test sends a test message" || fail "--test sent nothing"

if [ "$fails" -gt 0 ]; then echo "$fails failed"; exit 1; fi
echo "all watchdog tests passed"
