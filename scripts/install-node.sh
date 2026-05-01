#!/usr/bin/env bash
# install-node.sh — provision a fresh cf-vpn node end-to-end.
#
# This script:
#   - validates every input the Go installer requires (MODE, NODE_ID, …)
#   - builds AND installs both cfvpnctl and cfvpn-agent
#   - writes the full /etc/cfvpn/cfvpn.env that RunInstall expects
#   - falls back from direct → cloudflare if TCP/443 cannot be bound
#   - verifies all systemd units are active before exiting
#
# Usage:
#   sudo -E CF_API_TOKEN=... CF_ACCOUNT_ID=... NODE_ID=hk-01 \
#     [DOMAIN=vpn.example.com] [USER1_NAME=alice] [MODE=direct] \
#     bash scripts/install-node.sh
set -euo pipefail

log()  { printf '[install-node] %s\n' "$*"; }
warn() { printf '[install-node] WARN: %s\n' "$*" >&2; }
die()  { printf '[install-node] ERROR: %s\n' "$*" >&2; exit 1; }

require_env() {
  local name="$1"
  [ -n "${!name:-}" ] || die "$name is required"
}

# ----- 0. preflight -----------------------------------------------------------
[ "${EUID:-$(id -u)}" -eq 0 ] || die "run as root (use sudo -E)"
command -v apt-get >/dev/null || die "this script supports Debian/Ubuntu only"

require_env CF_API_TOKEN
require_env CF_ACCOUNT_ID
require_env NODE_ID

: "${USER1_NAME:=user1}"
: "${MODE:=auto}"
: "${DOMAIN:=}"

# NODE_ID must be a single DNS label; the installer rejects anything else.
if ! [[ "$NODE_ID" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]; then
  die "NODE_ID must be a DNS label: lowercase a-z, 0-9, '-', no leading/trailing dash, max 63 chars (got: $NODE_ID)"
fi

# USER1_NAME must match the xray validator: ^[A-Za-z0-9_-]{1,32}$.
if ! [[ "$USER1_NAME" =~ ^[A-Za-z0-9_-]{1,32}$ ]]; then
  die "USER1_NAME must match ^[A-Za-z0-9_-]{1,32}\$ (got: $USER1_NAME)"
fi

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ -f "$PROJECT_ROOT/go.mod" ] || die "go.mod not found; run from inside the cf-vpn repository"

# ----- 1. resolve MODE --------------------------------------------------------
# direct  : VPS terminates TCP/443 itself, faster path
# cloudflare : VLESS rides Cloudflare Tunnel, used when 443 is blocked
# auto    : try direct, fall back to cloudflare if 443 is busy / blocked
resolve_mode() {
  case "$MODE" in
    direct|cloudflare) return 0 ;;
    auto) ;;
    *) die "MODE must be direct, cloudflare, or auto (got: $MODE)" ;;
  esac

  if (echo >/dev/tcp/127.0.0.1/443) >/dev/null 2>&1; then
    warn "TCP/443 already accepting connections on this host; selecting MODE=cloudflare"
    MODE=cloudflare
    return 0
  fi
  if ss -tlnp 2>/dev/null | awk '{print $4}' | grep -qE '(^|:)443$'; then
    warn "Something is already listening on :443; selecting MODE=cloudflare"
    MODE=cloudflare
    return 0
  fi
  MODE=direct
}
resolve_mode
log "selected MODE=$MODE"

# ----- 2. OS dependencies -----------------------------------------------------
log "installing OS dependencies"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y

PACKAGES=(curl wget jq openssl uuid-runtime qrencode ca-certificates git iproute2 golang-go ufw tar gzip coreutils bash systemd dnsutils rsync)
TO_INSTALL=()
for pkg in "${PACKAGES[@]}"; do
  if dpkg -s "$pkg" >/dev/null 2>&1; then
    # Already installed — check if upgrade is needed
    if apt-get --just-print upgrade 2>/dev/null | grep -q "^Inst $pkg "; then
      TO_INSTALL+=("$pkg")
    fi
  else
    TO_INSTALL+=("$pkg")
  fi
done

if [ ${#TO_INSTALL[@]} -gt 0 ]; then
  log "installing/upgrading: ${TO_INSTALL[*]}"
  apt-get install -y "${TO_INSTALL[@]}"
else
  log "all packages already up-to-date"
fi

command -v go >/dev/null || die "go toolchain not on PATH after apt-get install"

# Some VPS firewalls allow recursive DNS but block UDP/53 to Cloudflare's
# authoritative nameservers; lego 4.35+ then times out unless it checks RNS.
if dig @173.245.59.111 cloudflare.com +time=3 +tries=1 >/dev/null 2>&1; then
  log "Cloudflare authoritative DNS reachable"
else
  warn "Cloudflare authoritative DNS unreachable; using recursive DNS propagation checks for lego"
  export LEGO_DISABLE_CP=1
  export LEGO_DNS_RESOLVERS="${LEGO_DNS_RESOLVERS:-1.1.1.1:53,8.8.8.8:53}"
  unset LEGO_PROPAGATION_WAIT
fi

# ----- 3. build & install both binaries ---------------------------------------
log "building cfvpnctl + cfvpn-agent"
(
  cd "$PROJECT_ROOT"
  go build -o bin/cfvpnctl  ./cmd/cfvpnctl
  go build -o bin/cfvpn-agent ./cmd/cfvpn-agent
)
install -m 0755 "$PROJECT_ROOT/bin/cfvpnctl"   /usr/local/bin/cfvpnctl
install -m 0755 "$PROJECT_ROOT/bin/cfvpn-agent" /usr/local/bin/cfvpn-agent

# ----- 4. check zone collision ----------------------------------------------
# Query D1 to show existing zone→node assignments so the operator can spot
# crowding before cfvpnctl picks a zone.  Non-fatal — informational only.
D1_DB_ID="0649f07f-e2c0-47f3-b84a-273f7f67332e"
D1_SQL="SELECT id,label,zone,vpn_host FROM nodes WHERE zone != ''"
D1_RESP=$(curl -sS --max-time 10 \
  -H "Authorization: Bearer ${CF_API_TOKEN}" \
  "https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/d1/database/${D1_DB_ID}/query" \
  -F "sql=${D1_SQL}")

D1_OK=$(echo "$D1_RESP" | jq -r '.success // false')
if [ "$D1_OK" != "true" ]; then
  warn "D1 zone check failed (non-fatal): $(echo "$D1_RESP" | jq -r '.errors[0].message // "unknown"')"
else
  D1_ROWS=$(echo "$D1_RESP" | jq -r '.result[0].results // []')
  NODE_COUNT=$(echo "$D1_ROWS" | jq -r 'length')
  log "D1 nodes: $NODE_COUNT"
  if [ "$NODE_COUNT" -gt 0 ]; then
    echo "$D1_ROWS" | jq -r 'group_by(.zone) | .[] | "  \(.[0].zone): \(map(.id+"("+.vpn_host+")") | join(", "))"'
  fi
fi

# ----- 5. write env file ------------------------------------------------------
# RunInstall (internal/commands/install.go) requires:
#   CF_API_TOKEN, CF_ACCOUNT_ID, NODE_ID, USER1_NAME, MODE
# DOMAIN is optional — when empty the installer picks a host from
# internal/zones.DefaultPool. ADMIN_HOST is always <NODE_ID>.rwl247.dev,
# so the CF token MUST also have access to the rwl247.dev zone.
log "writing /etc/cfvpn/cfvpn.env"
install -d -m 700 /etc/cfvpn
tmp_env="$(mktemp)"
{
  printf 'CF_API_TOKEN=%s\n'  "$CF_API_TOKEN"
  printf 'CF_ACCOUNT_ID=%s\n' "$CF_ACCOUNT_ID"
  printf 'NODE_ID=%s\n'       "$NODE_ID"
  printf 'USER1_NAME=%s\n'    "$USER1_NAME"
  printf 'MODE=%s\n'          "$MODE"
  [ -n "$DOMAIN" ] && printf 'DOMAIN=%s\n' "$DOMAIN"
} >"$tmp_env"
install -m 600 "$tmp_env" /etc/cfvpn/cfvpn.env
rm -f "$tmp_env"

# ----- 6. firewall hygiene ----------------------------------------------------
# UFW failures during `cfvpnctl install` are non-fatal warnings, but if the
# admin pre-enables UFW we must keep SSH open or we lose the box.
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q 'Status: active'; then
  log "ufw is active — ensuring SSH is allowed"
  ufw allow OpenSSH || ufw allow 22/tcp || warn "could not whitelist SSH; verify manually"
fi

# ----- 7. run installer -------------------------------------------------------
log "running cfvpnctl install (mode=$MODE)"
cfvpnctl install

log "installing healthcheck timer"
cfvpnctl healthcheck install

# ----- 8. verify --------------------------------------------------------------
log "verifying systemd units"
units=(cfvpn-xray cfvpn-hysteria cfvpn-cloudflared cfvpn-agent)
sleep 2
for u in "${units[@]}"; do
  state=$(systemctl is-active "$u" || true)
  if [ "$state" = active ]; then
    log "  $u: active"
  else
    warn "  $u: $state — check 'journalctl -u $u -n 80'"
  fi
done

log "running healthcheck"
cfvpnctl healthcheck run || warn "healthcheck reported failure (Cloudflare tunnel may still be registering — re-run in 60s)"

log "node ready"
log "next: sudo cfvpnctl status"
log "next: sudo cfvpnctl gen-sub $USER1_NAME"
