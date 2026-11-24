#!/usr/bin/env bash
# Looks at the server from the inside and sends a message (Telegram and/or a webhook) when something is wrong:
# the disk or memory is nearly full, a portal container is down or unhealthy, or the web address stops answering.
# Run it every few minutes from a systemd timer (deploy/systemd/cadportal-watchdog.*) or from cron.
#
# A problem is reported once, again every WATCHDOG_REPEAT_HOURS (default 6) while it lasts, at once if it gets
# worse (warning -> critical), and a message tells you when it is over. Nothing is sent while all is well.
#
# Settings come from .env in COMPOSE_DIR (default: this repository) or from the environment:
#   TELEGRAM_BOT_TOKEN TELEGRAM_CHAT_ID ALERT_WEBHOOK_URL ALERT_SERVER_NAME ALERT_MIN_LEVEL
#   WATCHDOG_DISK_WARN (85) WATCHDOG_DISK_CRIT (95) WATCHDOG_MEM_WARN (90) WATCHDOG_MEM_CRIT (96)
#   WATCHDOG_DISK_PATH (/)  WATCHDOG_STATE_DIR (/var/tmp/cadportal-watchdog)  WATCHDOG_REPEAT_HOURS (6)
#   COMPOSE_PROJECT_NAME (cadportal-prod)  WEB_PORT (8080)
#   scripts/watchdog.sh --test     sends one test message and exits
set -uo pipefail

cd "${COMPOSE_DIR:-$(dirname "$0")/..}" || exit 1

# Read only the names this script uses from .env (it is not sourced: it may hold anything).
read_env() {
  [ -f .env ] || return 0
  local k v
  for k in TELEGRAM_BOT_TOKEN TELEGRAM_CHAT_ID ALERT_WEBHOOK_URL ALERT_SERVER_NAME ALERT_MIN_LEVEL \
    WATCHDOG_DISK_WARN WATCHDOG_DISK_CRIT WATCHDOG_MEM_WARN WATCHDOG_MEM_CRIT COMPOSE_PROJECT_NAME WEB_PORT; do
    [ -n "${!k:-}" ] && continue
    v=$(grep -E "^${k}=" .env | tail -n1 | cut -d= -f2-)
    v=${v%\"}; v=${v#\"}; v=${v%\'}; v=${v#\'}
    [ -n "$v" ] && export "$k=$v"
  done
  return 0
}
read_env

server=${ALERT_SERVER_NAME:-$(hostname)}
min_level=${ALERT_MIN_LEVEL:-info}
state=${WATCHDOG_STATE_DIR:-/var/tmp/cadportal-watchdog}
repeat=$(( ${WATCHDOG_REPEAT_HOURS:-6} * 3600 ))
disk_warn=${WATCHDOG_DISK_WARN:-85}; disk_crit=${WATCHDOG_DISK_CRIT:-95}
mem_warn=${WATCHDOG_MEM_WARN:-90};   mem_crit=${WATCHDOG_MEM_CRIT:-96}
project=${COMPOSE_PROJECT_NAME:-cadportal-prod}
now=${WATCHDOG_NOW:-$(date +%s)}
mkdir -p "$state"

rank() { case $1 in info) echo 0 ;; warning) echo 1 ;; critical) echo 2 ;; *) echo 0 ;; esac; }

json_escape() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e ':a;N;$!ba;s/\n/\\n/g'; }

# send LEVEL TITLE BODY: to every configured channel. The token and the URL go to curl on its standard input,
# so they are not in the process list, and they never reach the output.
send() {
  local level=$1 title=$2 body=${3:-} icon text
  [ "$(rank "$level")" -ge "$(rank "$min_level")" ] || return 0
  case $level in critical) icon="🚨" ;; warning) icon="⚠️" ;; *) icon="✅" ;; esac
  text="$icon [$server] $title"
  [ -n "$body" ] && text="$text"$'\n'"$body"
  if [ -n "${TELEGRAM_BOT_TOKEN:-}" ] && [ -n "${TELEGRAM_CHAT_ID:-}" ]; then
    printf 'url = "https://api.telegram.org/bot%s/sendMessage"\n' "$TELEGRAM_BOT_TOKEN" | curl -fsS -m 15 -o /dev/null -K - \
      --data-urlencode "chat_id=${TELEGRAM_CHAT_ID}" --data-urlencode "text=${text}" >/dev/null 2>&1 \
      || echo "watchdog: the message to telegram failed" >&2
  fi
  if [ -n "${ALERT_WEBHOOK_URL:-}" ]; then
    printf 'url = "%s"\n' "$ALERT_WEBHOOK_URL" | curl -fsS -m 15 -o /dev/null -K - -X POST -H 'Content-Type: application/json' \
      --data "{\"level\":\"$level\",\"server\":\"$(json_escape "$server")\",\"title\":\"$(json_escape "$title")\",\"message\":\"$(json_escape "$body")\",\"text\":\"$(json_escape "$text")\",\"time\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}" >/dev/null 2>&1 \
      || echo "watchdog: the message to the webhook failed" >&2
  fi
}

if [ "${1:-}" = "--test" ]; then
  if [ -z "${TELEGRAM_BOT_TOKEN:-}${ALERT_WEBHOOK_URL:-}" ]; then echo "watchdog: no TELEGRAM_BOT_TOKEN or ALERT_WEBHOOK_URL is set" >&2; exit 1; fi
  send info "Test message" "If you can read this, alerts from this server reach you."
  echo "watchdog: test message sent"
  exit 0
fi

# report KEY LEVEL TITLE BODY: LEVEL ok means the problem is over.
report() {
  local key=$1 level=$2 title=$3 body=${4:-} f="$state/$1" last_level last_at
  if [ "$level" = ok ]; then
    if [ -f "$f" ]; then
      send info "$title" "$body"
      rm -f "$f"
    fi
    return 0
  fi
  if [ -f "$f" ]; then
    read -r last_level last_at < "$f" || true
    if [ "$last_level" = "$level" ] && [ $(( now - ${last_at:-0} )) -lt "$repeat" ]; then return 0; fi
  fi
  send "$level" "$title" "$body"
  echo "$level $now" > "$f"
}

# --- disk -------------------------------------------------------------------------------------------------
disk_path=${WATCHDOG_DISK_PATH:-/}
used=$(df -P "$disk_path" 2>/dev/null | awk 'NR==2 {gsub("%","",$5); print $5}')
if [ -n "$used" ]; then
  lvl=ok
  [ "$used" -ge "$disk_warn" ] && lvl=warning
  [ "$used" -ge "$disk_crit" ] && lvl=critical
  report disk "$lvl" "Disk is ${used}% full" "$disk_path is ${used}% used. Free space (old images: docker image prune -a, logs, backups scratch) before it fills up."
fi

# --- memory -----------------------------------------------------------------------------------------------
meminfo=${MEMINFO_FILE:-/proc/meminfo}
used_mem=$(awk '/^MemTotal:/ {t=$2} /^MemAvailable:/ {a=$2} END {if (t>0) printf "%d", (t-a)*100/t}' "$meminfo" 2>/dev/null)
if [ -n "$used_mem" ]; then
  lvl=ok
  [ "$used_mem" -ge "$mem_warn" ] && lvl=warning
  [ "$used_mem" -ge "$mem_crit" ] && lvl=critical
  report memory "$lvl" "Memory is ${used_mem}% used" "Little memory is left on the server. Check docker stats for the container using it."
fi

# --- containers -------------------------------------------------------------------------------------------
if command -v docker >/dev/null 2>&1; then
  bad=""
  while IFS='|' read -r svc st status; do
    [ -z "$svc" ] && continue
    case $svc in api|web|db|backup) ;; *) continue ;; esac
    if [ "$st" != running ] || [[ $status == *"(unhealthy)"* ]] || [[ $status == *Restarting* ]]; then
      bad="$bad"$'\n'"- $svc: $status"
    fi
  done < <(docker ps -a --filter "label=com.docker.compose.project=$project" \
             --format '{{.Label "com.docker.compose.service"}}|{{.State}}|{{.Status}}' 2>/dev/null)
  if [ -n "$bad" ]; then
    report containers critical "A container is down or unhealthy" "${bad#$'\n'}"$'\n'"Look at: docker compose -f docker-compose.prod.yml logs --tail 100"
  else
    report containers ok "All containers are healthy again" ""
  fi
fi

# --- the web address --------------------------------------------------------------------------------------
if curl -fsS -m 8 -o /dev/null "http://127.0.0.1:${WEB_PORT:-8080}/healthz" 2>/dev/null; then
  report web ok "The portal answers again" ""
else
  report web critical "The portal does not answer" "http://127.0.0.1:${WEB_PORT:-8080}/healthz failed on the server itself."
fi
exit 0
