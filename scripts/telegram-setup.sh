#!/usr/bin/env bash
# Registers the Telegram webhook + command menu for the cfvpn bot.
# Usage:
#   TELEGRAM_BOT_TOKEN=... TELEGRAM_WEBHOOK_SECRET=... PANEL_HOST=panel.rwl247.dev \
#     bash scripts/telegram-setup.sh
set -euo pipefail

die() { printf 'telegram-setup: ERROR: %s\n' "$*" >&2; exit 1; }

: "${TELEGRAM_BOT_TOKEN:?set TELEGRAM_BOT_TOKEN}"
: "${TELEGRAM_WEBHOOK_SECRET:?set TELEGRAM_WEBHOOK_SECRET}"
: "${PANEL_HOST:?set PANEL_HOST (e.g. panel.rwl247.dev)}"

# tg_call <method> <extra curl args...>
# The bot token (and any --data-urlencode secrets passed as args) travel via
# curl --config on stdin, so they never appear in argv / ps output.
tg_call() {
  local method="$1"; shift
  curl -sS --max-time 30 "$@" --config - <<EOF
url = "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/${method}"
EOF
}

echo "Setting webhook -> https://${PANEL_HOST}/telegram/webhook"
resp=$(tg_call setWebhook \
  --data-urlencode "url=https://${PANEL_HOST}/telegram/webhook" \
  --data-urlencode "secret_token=${TELEGRAM_WEBHOOK_SECRET}" \
  --data-urlencode 'allowed_updates=["message","callback_query"]') || resp=""
echo "$resp" | jq -e '.ok' >/dev/null 2>&1 \
  || die "setWebhook failed: ${resp:-<no response>}"
echo

echo "Setting command menu"
resp=$(tg_call setMyCommands \
  -H 'content-type: application/json' \
  -d '{"commands":[
    {"command":"help","description":"Danh sách lệnh"},
    {"command":"nodes","description":"Danh sách node"},
    {"command":"status","description":"Trạng thái node: /status <node>"},
    {"command":"health","description":"Healthcheck: /health <node>"},
    {"command":"sync","description":"Đồng bộ user lên node: /sync <node>"},
    {"command":"rotate","description":"Đổi domain node: /rotate <node>"},
    {"command":"users","description":"Danh sách user"},
    {"command":"adduser","description":"Thêm user: /adduser <tên>"},
    {"command":"deluser","description":"Xóa user: /deluser <tên>"},
    {"command":"sub","description":"Link subscription: /sub <tên>"},
    {"command":"upgrade","description":"Thêm user vào node mới: /upgrade <tên>"}
  ]}') || resp=""
echo "$resp" | jq -e '.ok' >/dev/null 2>&1 \
  || die "setMyCommands failed: ${resp:-<no response>}"
echo
echo "Done."
