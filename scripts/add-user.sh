#!/usr/bin/env bash
# Add a new user to xray config, restart xray, print subscription.
# Usage: add-user.sh <name>
# shellcheck source-path=SCRIPTDIR
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/xray_config.sh
source "$SCRIPT_DIR/lib/xray_config.sh"

CONFIG_FILE="$PROJECT_ROOT/xray/config.json"
MAX_USERS=5

NAME="${1:-}"
[ -n "$NAME" ] || die "usage: add-user.sh <name>"
[[ "$NAME" =~ ^[a-zA-Z0-9_-]{1,32}$ ]] || die "name must match [A-Za-z0-9_-], 1-32 chars"
[ -f "$CONFIG_FILE" ] || die "xray/config.json not found — run install.sh first"

current=$(count_clients "$CONFIG_FILE")
if [ "$current" -ge "$MAX_USERS" ]; then
  die "user cap reached ($MAX_USERS). Remove a user first with remove-user.sh"
fi

existing_names=$(list_client_names "$CONFIG_FILE")
if grep -qxF "$NAME" <<<"$existing_names"; then
  die "user '$NAME' already exists"
fi

uuid=$(uuidgen)
password=$(openssl rand -base64 24 | tr -d '\n')

add_client "$CONFIG_FILE" "$NAME" "$uuid" "$password"
log "added user '$NAME' (uuid=$uuid)"

cd "$PROJECT_ROOT"
docker compose restart xray
log "xray restarted"

exec "$SCRIPT_DIR/gen-subscription.sh" "$NAME"
