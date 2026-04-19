#!/usr/bin/env bash
# Probe the tunnel from the VPS itself. On 3 consecutive failures, restart.
# Intended to run via cron every 5 minutes.
# Usage: healthcheck.sh            (run probe, restart if needed)
#        healthcheck.sh --install  (install cron entry)
# shellcheck source-path=SCRIPTDIR
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"

ENV_FILE="$PROJECT_ROOT/.env"
STATE_FILE="$PROJECT_ROOT/.healthcheck.state"
LOG_FILE="/var/log/cf-vpn-health.log"
FAIL_THRESHOLD=3

install_cron() {
  local script_path="$SCRIPT_DIR/healthcheck.sh"
  local line="*/5 * * * * $script_path >> $LOG_FILE 2>&1"
  # Remove any existing cf-vpn lines first (idempotent)
  local tmp
  tmp=$(mktemp)
  crontab -l 2>/dev/null | grep -v 'healthcheck.sh' > "$tmp" || true
  printf '%s\n' "$line" >> "$tmp"
  crontab "$tmp"
  rm -f "$tmp"
  log "installed cron: $line"
}

if [ "${1:-}" = "--install" ]; then
  install_cron
  exit 0
fi

[ -f "$ENV_FILE" ] || die ".env not found"
set -a
# shellcheck source=/dev/null
source "$ENV_FILE"
set +a
[ -n "${DOMAIN:-}" ] || die "DOMAIN not set"

code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "https://${DOMAIN}/vless" || printf '000')
ts=$(date -Iseconds)

case "$code" in
  400|426)
    # Healthy — reset counter
    printf '0' > "$STATE_FILE"
    printf '[%s] OK code=%s\n' "$ts" "$code"
    ;;
  *)
    fails=0
    if [ -f "$STATE_FILE" ]; then
      raw=$(cat "$STATE_FILE")
      [[ "$raw" =~ ^[0-9]+$ ]] && fails=$raw
    fi
    fails=$((fails + 1))
    printf '%d' "$fails" > "$STATE_FILE"
    printf '[%s] FAIL code=%s fails=%d\n' "$ts" "$code" "$fails"
    if [ "$fails" -ge "$FAIL_THRESHOLD" ]; then
      printf '[%s] RESTART triggered after %d consecutive failures\n' "$ts" "$fails"
      cd "$PROJECT_ROOT"
      docker compose restart
      printf '0' > "$STATE_FILE"
    fi
    ;;
esac
