#!/usr/bin/env bash
# Rotate the tunnel to a new domain (must be in the same CF account).
# Keeps the old tunnel for 24h grace — caller can run with --cleanup <old-uuid> later.
# Usage: rotate-domain.sh <new-domain>
#        rotate-domain.sh --cleanup <tunnel-uuid>
# shellcheck source-path=SCRIPTDIR
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/cf_api.sh
source "$SCRIPT_DIR/lib/cf_api.sh"

ENV_FILE="$PROJECT_ROOT/.env"

usage() {
  cat <<EOF
usage: rotate-domain.sh <new-domain>
       rotate-domain.sh --cleanup <old-tunnel-uuid>
EOF
  exit 1
}

[ $# -ge 1 ] || usage
[ -f "$ENV_FILE" ] || die ".env not found"
set -a
# shellcheck source=/dev/null
source "$ENV_FILE"
set +a

if [ "$1" = "--cleanup" ]; then
  [ -n "${2:-}" ] || usage
  log "deleting old tunnel $2"
  delete_tunnel "$2"
  rm -f "$PROJECT_ROOT/cloudflared/${2}.json"
  log "cleanup complete"
  exit 0
fi

NEW_DOMAIN="$1"
OLD_DOMAIN="${DOMAIN:-}"
OLD_TUNNEL="${TUNNEL_UUID:-}"

[ -n "$OLD_DOMAIN" ] || die "no current DOMAIN in .env — run install.sh first"
[ "$NEW_DOMAIN" != "$OLD_DOMAIN" ] || die "new-domain matches current DOMAIN"

# Validate new domain has a zone in this account
log "validating $NEW_DOMAIN belongs to CF account"
get_zone_id "$NEW_DOMAIN" >/dev/null

log "rotating: $OLD_DOMAIN -> $NEW_DOMAIN (old tunnel $OLD_TUNNEL kept for grace)"

# Swap DOMAIN in .env, then run install.sh (it will create a new tunnel since name differs)
env_write "$ENV_FILE" "DOMAIN" "$NEW_DOMAIN"
# Force install to create a new tunnel by clearing TUNNEL_UUID
env_write "$ENV_FILE" "TUNNEL_UUID" ""

bash "$SCRIPT_DIR/install.sh"

log "rotation complete. Old tunnel $OLD_TUNNEL is still active."
log "After verifying clients work on $NEW_DOMAIN, run:"
log "  bash scripts/rotate-domain.sh --cleanup $OLD_TUNNEL"

# Regenerate subscriptions for all existing users
source "$SCRIPT_DIR/lib/xray_config.sh"
while IFS= read -r name; do
  bash "$SCRIPT_DIR/gen-subscription.sh" "$name" >/dev/null
  log "regenerated subscription for $name"
done < <(list_client_names "$PROJECT_ROOT/xray/config.json")
