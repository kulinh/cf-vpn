#!/usr/bin/env bash
# Remove a user from xray config and restart xray.
# Usage: remove-user.sh <name>
# shellcheck source-path=SCRIPTDIR
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/xray_config.sh
source "$SCRIPT_DIR/lib/xray_config.sh"

CONFIG_FILE="$PROJECT_ROOT/xray/config.json"

NAME="${1:-}"
[ -n "$NAME" ] || die "usage: remove-user.sh <name>"
[ -f "$CONFIG_FILE" ] || die "xray/config.json not found"

existing_names=$(list_client_names "$CONFIG_FILE")
if ! grep -qxF "$NAME" <<<"$existing_names"; then
  die "user '$NAME' not found"
fi

remove_client "$CONFIG_FILE" "$NAME"
log "removed user '$NAME'"

# Also remove subscription file
rm -f "$PROJECT_ROOT/subscriptions/${NAME}.txt"

cd "$PROJECT_ROOT"
docker compose restart xray
log "xray restarted"
