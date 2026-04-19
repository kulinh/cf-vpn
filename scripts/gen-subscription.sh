#!/usr/bin/env bash
# Print VLESS + Trojan URIs, base64 subscription, and QR codes for a user.
# Usage: gen-subscription.sh [user-name]  (default: $USER1_NAME from .env)
# shellcheck source-path=SCRIPTDIR
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/xray_config.sh
source "$SCRIPT_DIR/lib/xray_config.sh"
# shellcheck source=lib/uri.sh
source "$SCRIPT_DIR/lib/uri.sh"

ENV_FILE="$PROJECT_ROOT/.env"
CONFIG_FILE="$PROJECT_ROOT/xray/config.json"

[ -f "$ENV_FILE" ] || die ".env not found"
[ -f "$CONFIG_FILE" ] || die "xray/config.json not found — run install.sh first"

set -a
# shellcheck source=/dev/null
source "$ENV_FILE"
set +a

[ -n "${DOMAIN:-}" ] || die "DOMAIN not set in .env"

NAME="${1:-${USER1_NAME:-user1}}"

uuid=$(get_client_uuid "$CONFIG_FILE" "$NAME")
pw=$(get_client_password "$CONFIG_FILE" "$NAME")
[ -n "$uuid" ] || die "no vless client for user '$NAME'"
[ -n "$pw" ] || die "no trojan client for user '$NAME'"

vless_uri=$(build_vless_uri "$uuid" "$DOMAIN" "$NAME")
trojan_uri=$(build_trojan_uri "$pw" "$DOMAIN" "$NAME")
sub_b64=$(build_subscription_b64 "$vless_uri" "$trojan_uri")

mkdir -p "$PROJECT_ROOT/subscriptions"
OUT="$PROJECT_ROOT/subscriptions/${NAME}.txt"
{
  printf '# User: %s\n' "$NAME"
  printf '# Domain: %s\n\n' "$DOMAIN"
  printf '## VLESS\n%s\n\n' "$vless_uri"
  printf '## Trojan\n%s\n\n' "$trojan_uri"
  printf '## Base64 Subscription (paste into v2rayN/v2rayNG "Add Subscription")\n%s\n' "$sub_b64"
} > "$OUT"
chmod 600 "$OUT"

log "saved to $OUT"
echo
echo "=== VLESS URI ==="
echo "$vless_uri"
echo
echo "=== VLESS QR ==="
printf '%s' "$vless_uri" | qrencode -t UTF8
echo
echo "=== Trojan URI ==="
echo "$trojan_uri"
echo
echo "=== Trojan QR ==="
printf '%s' "$trojan_uri" | qrencode -t UTF8
echo
echo "=== Base64 Subscription ==="
echo "$sub_b64"
