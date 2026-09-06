#!/usr/bin/env bash
# install-node.sh — provision a fresh cf-vpn node end-to-end.
#
# This script:
#   - refuses to run on an already-provisioned node (see FORCE_REINSTALL below)
#   - validates every input the Go installer requires (MODE, NODE_ID, …)
#   - pre-installs cloudflared (Cloudflare apt repo) and lego (go install)
#   - builds AND installs both cfvpnctl and cfvpn-agent
#   - writes the full /etc/cfvpn/cfvpn.env that RunInstall expects
#   - falls back from direct → cloudflare if TCP/443 cannot be bound
#   - upserts node + user into D1 and syncs via agent after install
#   - verifies all systemd units are healthy before exiting
#
# Usage:
#   sudo -E CF_API_TOKEN=... CF_ACCOUNT_ID=... NODE_ID=us-01 \
#     [NODE_LABEL="US-01 (Dallas)"] [DOMAIN=vpn.example.com] \
#     [USER1_NAME=alice] [MODE=direct] \
#     bash scripts/install-node.sh
#
# NODE_ID  : DNS label (case-insensitive); stored UPPERCASE in D1.
# NODE_LABEL: human-readable name for the panel (defaults to uppercase NODE_ID).
# FORCE_REINSTALL=1 : re-provision a node that already has credentials. This
#   regenerates the Reality keypair and every user credential (breaking all
#   existing clients); the old env file is backed up to
#   /etc/cfvpn/cfvpn.env.bak-<unixtime> first. To upgrade in place use
#   `cfvpnctl upgrade` instead.
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

# Normalize: lowercase for DNS/cfvpnctl, UPPERCASE for D1 storage
NODE_ID="$(echo "$NODE_ID" | tr '[:upper:]' '[:lower:]')"
DB_NODE_ID="$(echo "$NODE_ID" | tr '[:lower:]' '[:upper:]')"

# NODE_LABEL is the panel display name; defaults to DB_NODE_ID if not provided
: "${NODE_LABEL:=$DB_NODE_ID}"

# NODE_ID must be a single DNS label
if ! [[ "$NODE_ID" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]; then
  die "NODE_ID must be a DNS label: a-z, 0-9, '-', no leading/trailing dash, max 63 chars (got: $NODE_ID)"
fi

# USER1_NAME must match the xray validator: ^[A-Za-z0-9_-]{1,32}$
if ! [[ "$USER1_NAME" =~ ^[A-Za-z0-9_-]{1,32}$ ]]; then
  die "USER1_NAME must match ^[A-Za-z0-9_-]{1,32}\$ (got: $USER1_NAME)"
fi

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ -f "$PROJECT_ROOT/go.mod" ] || die "go.mod not found; run from inside the cf-vpn repository"
LIB_DIR="$PROJECT_ROOT/scripts/lib"
ENV_FILE_HELPER="$LIB_DIR/cfvpn-env-file.sh"
[ -f "$ENV_FILE_HELPER" ] || die "missing $ENV_FILE_HELPER"

# shellcheck source=lib/cfvpn-common.sh
. "$LIB_DIR/cfvpn-common.sh"

# ----- 0b. refuse to clobber an already-provisioned node ----------------------
# Runs before apt/go/cfvpnctl touch anything: a second run of this script
# regenerates the Reality keypair and every user credential.
FORCE_REINSTALL="${FORCE_REINSTALL:-0}"
export FORCE_REINSTALL
bash "$ENV_FILE_HELPER" check

# Values are written to /etc/cfvpn/cfvpn.env unquoted (internal/state/store.go
# keeps everything after the first '=' verbatim — it does not strip quotes),
# and the same file is read by systemd EnvironmentFile=. Reject anything that
# cannot survive that round-trip rather than writing a file nobody can parse.
cfvpn_require_env_value CF_API_TOKEN  "$CF_API_TOKEN"
cfvpn_require_env_value CF_ACCOUNT_ID "$CF_ACCOUNT_ID"
[ -z "$DOMAIN" ] || cfvpn_require_env_value DOMAIN "$DOMAIN"

# AGENT_SHARED_SECRET gates /admin/v1/* on the admin tunnel
if [ -z "${AGENT_SHARED_SECRET:-}" ]; then
  AGENT_SHARED_SECRET="$(openssl rand -hex 32)"
fi
cfvpn_require_env_value AGENT_SHARED_SECRET "$AGENT_SHARED_SECRET"

# shellcheck disable=SC2034  # read by d1_query() in scripts/lib/cfvpn-d1.sh
D1_DB_ID="${CFVPN_D1_DATABASE_ID:-0649f07f-e2c0-47f3-b84a-273f7f67332e}"

# d1_query / d1_zone_for_domain / node+user upserts / agent_sync
# shellcheck source=lib/cfvpn-d1.sh
. "$LIB_DIR/cfvpn-d1.sh"

# Pin the ACME client so two nodes provisioned a week apart get the same lego.
# Override with LEGO_VERSION=v4.x.y (or "latest") when you deliberately move it.
LEGO_VERSION="${LEGO_VERSION:-latest}"

# ----- 1. resolve MODE --------------------------------------------------------
resolve_mode() {
  case "$MODE" in
    direct|cloudflare) return 0 ;;
    auto) ;;
    *) die "MODE must be direct, cloudflare, or auto (got: $MODE)" ;;
  esac
  if (echo >/dev/tcp/127.0.0.1/443) >/dev/null 2>&1; then
    warn "TCP/443 already accepting connections on this host; selecting MODE=cloudflare"
    MODE=cloudflare; return 0
  fi
  # Capture first: `ss | awk | grep -q` under `set -o pipefail` reports the
  # SIGPIPE of the left-hand side, not the match.
  local listening
  listening="$(ss -tlnp 2>/dev/null | awk '{print $4}' || true)"
  if grep -qE '(^|:)443$' <<<"$listening"; then
    warn "Something is already listening on :443; selecting MODE=cloudflare"
    MODE=cloudflare; return 0
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
# Capture the upgrade plan once. Piping it straight into `grep -q` closes the
# pipe on the first match; with `set -o pipefail` the resulting SIGPIPE (141)
# from apt-get became the status of the whole `if`, so every installed package
# looked up-to-date and was never upgraded.
UPGRADE_PLAN="$(apt-get --just-print upgrade 2>/dev/null || true)"
TO_INSTALL=()
for pkg in "${PACKAGES[@]}"; do
  if dpkg -s "$pkg" >/dev/null 2>&1; then
    if grep -q "^Inst $pkg " <<<"$UPGRADE_PLAN"; then
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

# Persisted into cfvpn.env below when set: the cert-renew unit runs from
# EnvironmentFile=, ~60 days later, with no shell environment to inherit.
LEGO_FALLBACK_DNS=0
if dig @173.245.59.111 cloudflare.com +time=3 +tries=1 >/dev/null 2>&1; then
  log "Cloudflare authoritative DNS reachable"
else
  warn "Cloudflare authoritative DNS unreachable; using recursive DNS propagation checks for lego"
  export LEGO_DISABLE_CP=1
  export LEGO_DNS_RESOLVERS="${LEGO_DNS_RESOLVERS:-1.1.1.1:53,8.8.8.8:53}"
  unset LEGO_PROPAGATION_WAIT
  cfvpn_require_env_value LEGO_DNS_RESOLVERS "$LEGO_DNS_RESOLVERS"
  LEGO_FALLBACK_DNS=1
fi

# ----- 2b. pre-install cloudflared via Cloudflare official apt repo -----------
# Pre-installing means cfvpnctl's EnsureCloudflared (binary.Exists check) will
# skip its GitHub download entirely.
if ! command -v cloudflared >/dev/null 2>&1; then
  log "installing cloudflared via Cloudflare apt repo"
  install -d -m 0755 /usr/share/keyrings
  curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    https://pkg.cloudflare.com/cloudflare-public-v2.gpg \
    | tee /usr/share/keyrings/cloudflare-public-v2.gpg >/dev/null
  echo 'deb [signed-by=/usr/share/keyrings/cloudflare-public-v2.gpg] https://pkg.cloudflare.com/cloudflared any main' \
    | tee /etc/apt/sources.list.d/cloudflared.list >/dev/null
  apt-get update -y
  apt-get install -y cloudflared
  log "cloudflared installed: $(cloudflared --version 2>&1 | head -1)"
else
  log "cloudflared already installed: $(cloudflared --version 2>&1 | head -1)"
fi

# ----- 2c. pre-install lego via go install (no GitHub API) --------------------
# GOBIN=/usr/local/bin places the binary directly where cfvpnctl expects it.
if ! command -v lego >/dev/null 2>&1; then
  log "installing lego via go install ($LEGO_VERSION)"
  GOBIN=/usr/local/bin go install "github.com/go-acme/lego/v4/cmd/lego@${LEGO_VERSION}"
  log "lego installed: $(lego --version 2>&1 | head -1)"
else
  log "lego already installed: $(lego --version 2>&1 | head -1)"
fi

# ----- 3. build & install both binaries ---------------------------------------
log "building cfvpnctl + cfvpn-agent"
(
  cd "$PROJECT_ROOT"
  go build -o bin/cfvpnctl    ./cmd/cfvpnctl
  go build -o bin/cfvpn-agent ./cmd/cfvpn-agent
)
install -m 0755 "$PROJECT_ROOT/bin/cfvpnctl"    /usr/local/bin/cfvpnctl
install -m 0755 "$PROJECT_ROOT/bin/cfvpn-agent" /usr/local/bin/cfvpn-agent

# ----- 4. check zone collision ------------------------------------------------
D1_RESP=$(d1_query "$(jq -n '{sql:"SELECT id,label,zone,vpn_host FROM nodes WHERE zone != ?", params:[""]}')")
D1_OK=$(echo "$D1_RESP" | jq -r '.success // false')
if [ "$D1_OK" != "true" ]; then
  warn "D1 zone check failed (non-fatal): $(echo "$D1_RESP" | jq -r '.errors[0].message // "unknown"')"
else
  D1_ROWS=$(echo "$D1_RESP" | jq '.result[0].results // []')
  NODE_COUNT=$(echo "$D1_ROWS" | jq 'length')
  log "D1 nodes: $NODE_COUNT"
  if [ "$NODE_COUNT" -gt 0 ]; then
    echo "$D1_ROWS" | jq -r 'group_by(.zone) | .[] | "  \(.[0].zone): \(map(.id+"("+.vpn_host+")") | join(", "))"'
  fi
fi

# ----- 5. write env file ------------------------------------------------------
# cfvpnctl reads NODE_ID (lowercase) from env for DNS label use.
# DB_NODE_ID (uppercase) is used only by this script for D1 writes.
# Values are written unquoted on purpose — see cfvpn_require_env_value above.
log "writing /etc/cfvpn/cfvpn.env"
{
  printf 'CF_API_TOKEN=%s\n'        "$CF_API_TOKEN"
  printf 'CF_ACCOUNT_ID=%s\n'       "$CF_ACCOUNT_ID"
  printf 'NODE_ID=%s\n'             "$NODE_ID"
  printf 'USER1_NAME=%s\n'          "$USER1_NAME"
  printf 'MODE=%s\n'                "$MODE"
  printf 'AGENT_SHARED_SECRET=%s\n' "$AGENT_SHARED_SECRET"
  if [ "$LEGO_FALLBACK_DNS" -eq 1 ]; then
    printf 'LEGO_DNS_RESOLVERS=%s\n' "$LEGO_DNS_RESOLVERS"
    printf 'LEGO_DISABLE_CP=%s\n'    "1"
  fi
  # `|| true`: this group is the left side of a pipe and `set -o pipefail` is
  # on, so a false AND-list here would abort the whole script.
  { [ -n "$DOMAIN" ] && printf 'DOMAIN=%s\n' "$DOMAIN"; } || true
} | bash "$ENV_FILE_HELPER" write

# ----- 6. firewall hygiene ----------------------------------------------------
if command -v ufw >/dev/null 2>&1; then
  UFW_STATUS="$(ufw status 2>/dev/null || true)"
  if grep -q 'Status: active' <<<"$UFW_STATUS"; then
    log "ufw is active — ensuring SSH is allowed"
    ufw allow OpenSSH || ufw allow 22/tcp || warn "could not whitelist SSH; verify manually"
  fi
fi

# ----- 7. run installer -------------------------------------------------------
list_admin_tunnels() {
  local resp
  resp=$(curl -sS --max-time 10 --config - <<EOF
header = "Authorization: Bearer ${CF_API_TOKEN}"
url = "https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/cfd_tunnel?is_deleted=false&per_page=100"
EOF
)
  # Return non-zero (and no output) on transport/API error so callers can tell
  # "no tunnels" apart from "couldn't list". Silently treating a failed query as
  # an empty list would flag every existing tunnel as a new orphan.
  [ -n "$resp" ] && [ "$(printf '%s' "$resp" | jq -r '.success // false' 2>/dev/null)" = "true" ] || return 1
  printf '%s' "$resp" | jq -r '.result // [] | .[] | select(.name | startswith("cfvpn-admin-")) | "\(.id) \(.name)"'
}
TUNNELS_SNAPSHOT_OK=0
if TUNNELS_BEFORE="$(list_admin_tunnels)"; then
  TUNNELS_BEFORE="$(printf '%s\n' "$TUNNELS_BEFORE" | sort)"
  TUNNELS_SNAPSHOT_OK=1
else
  warn "could not list admin tunnels before install (CF API) — orphan detection disabled"
fi

cleanup_orphan_tunnels() {
  local rc=$?
  [ $rc -eq 0 ] && return 0
  warn "cfvpnctl install exited with status $rc — checking for orphan admin tunnels"
  if [ "${TUNNELS_SNAPSHOT_OK:-0}" -ne 1 ]; then
    warn "  pre-install tunnel snapshot unavailable — skipping orphan diff (check CF dashboard)"
    exit $rc
  fi
  local tunnels_after new
  if ! tunnels_after="$(list_admin_tunnels)"; then
    warn "  could not list admin tunnels after failure — check CF dashboard manually"
    exit $rc
  fi
  tunnels_after="$(printf '%s\n' "$tunnels_after" | sort)"
  new="$(comm -13 <(printf '%s\n' "$TUNNELS_BEFORE") <(printf '%s\n' "$tunnels_after"))"
  if [ -n "$new" ]; then
    warn "orphan admin tunnels created during failed install:"
    printf '%s\n' "$new" | while read -r tid tname; do
      [ -z "$tid" ] && continue
      warn "  $tid ($tname) — clean up with: sudo cfvpnctl rotate-domain --cleanup $tid --yes"
    done
  fi
  exit $rc
}
# INT/TERM too: a Ctrl-C during `cfvpnctl install` leaks a tunnel just as a
# failure does.
trap cleanup_orphan_tunnels EXIT INT TERM

log "running cfvpnctl install (mode=$MODE)"
cfvpnctl install
trap - EXIT INT TERM

log "installing healthcheck timer"
cfvpnctl healthcheck install

# ----- 8. verify systemd units ------------------------------------------------
# `Restart=on-failure` + `RestartSec=3` means a crash-looping unit still reports
# `active` a couple of seconds in — wait long enough for a restart to show up
# and treat any restart as a failure.
log "verifying systemd units"
units=(cfvpn-xray cfvpn-hysteria cfvpn-cloudflared cfvpn-agent)
sleep 8
unit_failures=0
for u in "${units[@]}"; do
  state=$(systemctl is-active "$u" 2>/dev/null || true)
  restarts=$(systemctl show -p NRestarts --value "$u" 2>/dev/null || true)
  [[ "$restarts" =~ ^[0-9]+$ ]] || restarts=0
  if [ "$state" = active ] && [ "$restarts" -eq 0 ]; then
    log "  $u: active (NRestarts=0)"
  else
    warn "  $u: state=$state NRestarts=$restarts — check 'journalctl -u $u -n 80'"
    unit_failures=$((unit_failures + 1))
  fi
done
if [ "$unit_failures" -gt 0 ]; then
  die "$unit_failures systemd unit(s) unhealthy — refusing to publish this node to D1.
  Inspect with: journalctl -u <unit> -n 80
  Then repair in place with: sudo cfvpnctl upgrade"
fi

log "running healthcheck"
cfvpnctl healthcheck run || warn "healthcheck reported failure (Cloudflare tunnel may still be registering — re-run in 60s)"

# ----- 9. sync node + user to D1 & control panel -----------------------------
log "syncing $DB_NODE_ID + user $USER1_NAME to D1"

# Load the runtime values written by cfvpnctl install. Parsed the same way
# internal/state/store.go parses them (split on the first '='), NOT sourced —
# `. cfvpn.env` would execute any `$(...)` that ended up in a value.
while IFS='=' read -r _k _v; do
  case "$_k" in
    DOMAIN|HY2_HOST|HY2_PORT|HY2_OBFS_PW|HY2_PASS_USER1|UUID_USER1| \
    PUBLIC_IP|ADMIN_HOST|REALITY_PUBLIC_KEY|REALITY_SHORT_ID|REALITY_SNI|REALITY_DEST)
      export "$_k=$_v" ;;
  esac
done < /etc/cfvpn/cfvpn.env

for _k in DOMAIN HY2_HOST HY2_PORT HY2_OBFS_PW HY2_PASS_USER1 UUID_USER1 PUBLIC_IP ADMIN_HOST; do
  [ -n "${!_k:-}" ] || die "$_k not populated by cfvpnctl install — refusing to write a half-empty node row to D1"
done

# shellcheck disable=SC2034  # read by the d1_* helpers in scripts/lib/cfvpn-d1.sh
NOW_MS=$(date +%s%3N)
ZONE="$(d1_zone_for_domain "$DOMAIN")"
# A blank DOMAIN yields a blank ZONE, which would write the node to D1 with
# zone='' and silently defeat the zone-collision check. Fail loudly instead.
if [ -z "$DOMAIN" ] || [ -z "$ZONE" ]; then
  die "DOMAIN is empty after cfvpnctl install — cannot derive zone; refusing to upsert node with zone=''"
fi

d1_upsert_node          # 9a. node row (INSERT OR REPLACE handles re-installs)
d1_ensure_user          # 9b. user row, created with a random sub_token if new
d1_upsert_user_nodes    # 9c. user_nodes binding (vless_uuid + hy2_pw)
log "calling agent sync via $ADMIN_HOST"
agent_sync              # 9d. confirm the agent is live via the admin tunnel

log "node ready: $DB_NODE_ID ($NODE_LABEL)"
log "next: sudo cfvpnctl status"
log "next: sudo cfvpnctl gen-sub $USER1_NAME"
