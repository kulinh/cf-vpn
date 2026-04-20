#!/usr/bin/env bash
set -euo pipefail

log() {
  printf '[bootstrap] %s\n' "$*"
}

die() {
  printf '[bootstrap] ERROR: %s\n' "$*" >&2
  exit 1
}

require_env() {
  local name="$1"
  [ -n "${!name:-}" ] || die "$name is required"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

if [ "${EUID:-$(id -u)}" -ne 0 ]; then
  die "run as root (example: sudo -E bash scripts/bootstrap-vps.sh)"
fi

require_env CF_API_TOKEN
require_env CF_ACCOUNT_ID
require_env DOMAIN
: "${USER1_NAME:=user1}"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ -f "$PROJECT_ROOT/go.mod" ] || die "go.mod not found (run inside cf-vpn repository)"

if ! command -v apt-get >/dev/null 2>&1; then
  die "this script supports Debian/Ubuntu (apt-get)"
fi

log "installing OS dependencies"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y curl jq openssl uuid-runtime qrencode ca-certificates git golang-go

require_cmd go

log "building cfvpnctl"
(
  cd "$PROJECT_ROOT"
  go build -o bin/cfvpnctl ./cmd/cfvpnctl
)
install -m 0755 "$PROJECT_ROOT/bin/cfvpnctl" /usr/local/bin/cfvpnctl

log "writing /etc/cfvpn/cfvpn.env"
install -d -m 700 /etc/cfvpn
tmp_env="$(mktemp)"
cat > "$tmp_env" <<EOF
CF_API_TOKEN=$CF_API_TOKEN
CF_ACCOUNT_ID=$CF_ACCOUNT_ID
DOMAIN=$DOMAIN
USER1_NAME=$USER1_NAME
EOF
install -m 600 "$tmp_env" /etc/cfvpn/cfvpn.env
rm -f "$tmp_env"

log "running install"
cfvpnctl install

log "installing healthcheck timer"
cfvpnctl healthcheck install

log "done"
log "next: sudo cfvpnctl status"
log "next: sudo cfvpnctl gen-sub $USER1_NAME"
