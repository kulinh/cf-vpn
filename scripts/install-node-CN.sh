#!/usr/bin/env bash
# install-node-CN.sh — deploy cf-vpn to a mainland-China (GFW) VPS.
#
# Runs on a LOCAL machine OUTSIDE the GFW (e.g. VNM-01).
# All GFW-blocked downloads happen locally; binaries and the repo are rsynced
# to the target, then the installation is driven over SSH — the China VPS never
# needs to reach github.com or any other blocked domain.
#
# GFW expectations (cfvpnctl install on target needs these reachable):
#   - api.cloudflare.com:443         (DNS records, tunnel CRUD)
#   - acme-v02.api.letsencrypt.org   (lego TLS cert issuance)
#   - System clock within 5min of UTC (ACME signs nonces with timestamps)
#
# Flow:
#   0. preflight LOCAL — tool checks + local CF API reachability
#   1. SSH/MODE — verify SSH + probe target /proc/net to pick direct/cloudflare
#   2. OS deps on target — minimal apt: ca-certificates + curl + ufw
#   3. preflight TARGET — DNS / TCP443 / HTTPS / clock-skew (FAIL-FAST before
#                         any heavy download or resource creation)
#   4. download — fetch GFW-blocked binaries/packages to local STAGE_DIR.
#        Every payload is verified against its upstream sha256 before it is
#        shipped to the target and installed as root (see cfvpn-common.sh):
#        [1/5] xray binary          github.com/XTLS/Xray-core
#        [2/5] xray geo data        github.com/v2fly
#        [3/5] cloudflared .deb     pkg.cloudflare.com (apt repo)
#        [4/5] hysteria2            github.com/apernet/hysteria
#        [5/5] jq                   github.com/jqlang/jq
#   5. build locally (linux/amd64):
#        cfvpnctl + cfvpn-agent  (Go, CGO_ENABLED=0)
#        lego                    (go install, CGO_ENABLED=0)
#        openssl CLI             (C, from openssl source)
#   6. D1 zone check — informational
#   7. rsync — push repo + STAGE_DIR to target
#   8. stage install — install binaries on target; cloudflared via dpkg
#   9. env — write /etc/cfvpn/cfvpn.env (with LEGO_DNS_RESOLVERS for fast DNS-01)
#  10. firewall — ensure SSH stays open if ufw is active
#  11. cfvpnctl install — runs on target (all binaries pre-staged)
#  12. verify — check systemd units + run healthcheck
#  13. D1 sync — upsert node + user + user_nodes, call agent sync
#  14. cleanup — rm STAGE_DIR (always, via single EXIT trap chain)
#
# Usage (from machine OUTSIDE GFW):
#   CF_API_TOKEN=... CF_ACCOUNT_ID=... NODE_ID=chn-02 \
#     TARGET_HOST=root@121.41.196.104 [SSH_KEY=/tmp/rwl247] \
#     [NODE_LABEL="CN-02 (Aliyun SZ)"] \
#     [USER1_NAME=kulinh] [MODE=direct] [DOMAIN=] \
#     bash scripts/install-node-CN.sh
#
# NODE_ID  : DNS label (case-insensitive); stored UPPERCASE in D1.
# NODE_LABEL: human-readable name for the panel (defaults to uppercase NODE_ID).
# FORCE_REINSTALL=1 : re-provision a target that already holds node secrets.
#   This regenerates the Reality keypair and every user credential (breaking all
#   existing clients); the target's env file is backed up to
#   /etc/cfvpn/cfvpn.env.bak-<unixtime> first. Prefer `cfvpnctl upgrade`.
# CFVPN_ALLOW_UNVERIFIED_DOWNLOADS=1 : continue when an upstream sha256 file
#   cannot be fetched. A checksum MISMATCH always aborts.
#
# Target requirements: root over SSH, x86_64.

set -euo pipefail

log()  { printf '[install-node-CN] %s\n' "$*"; }
warn() { printf '[install-node-CN] WARN: %s\n' "$*" >&2; }
die()  { printf '[install-node-CN] ERROR: %s\n' "$*" >&2; exit 1; }

require_env() {
  local name="$1"
  [ -n "${!name:-}" ] || die "$name is required"
}

# ----- 0. preflight LOCAL -----------------------------------------------------
for cmd in curl python3 go gcc make unzip rsync ssh openssl jq; do
  command -v "$cmd" >/dev/null || die "$cmd is required on this local machine"
done

require_env CF_API_TOKEN
require_env CF_ACCOUNT_ID
require_env NODE_ID
require_env TARGET_HOST   # e.g. root@121.41.196.104

# Local network sanity — script is useless without these
log "checking local network reachability"
curl -fsS --max-time 10 -o /dev/null https://api.cloudflare.com/client/v4/ips \
  || die "local cannot reach api.cloudflare.com — check internet on this machine"
curl -fsS --max-time 10 -o /dev/null \
  https://pkg.cloudflare.com/cloudflared/dists/any/InRelease \
  || die "local cannot reach pkg.cloudflare.com (needed for cloudflared .deb)"
# proxy.golang.org is needed by `go install`; warn-only since GOPROXY can be reset
if ! curl -fsS --max-time 10 -o /dev/null https://proxy.golang.org 2>/dev/null; then
  warn "proxy.golang.org unreachable from local — go install may be slow"
  warn "if local is in China, set: export GOPROXY=https://goproxy.cn,direct"
fi
log "local network OK"

SSH_KEY="${SSH_KEY:-}"
# accept-new (not "no"): still convenient on a fresh VPS, but a CHANGED host key
# now aborts instead of handing CF_API_TOKEN + the agent secret to whoever
# answers. IdentitiesOnly=yes always: it also stops ssh-agent from offering
# unrelated keys when no -i is given.
_ssh_common=(-o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 \
             -o BatchMode=yes -o IdentitiesOnly=yes)
_ssh_opts=("${_ssh_common[@]}")
# Quote SSH_KEY in case it contains spaces; rsync passes -e as a single string
if [ -n "$SSH_KEY" ]; then
  _ssh_opts+=(-i "$SSH_KEY")
  _rsync_e="ssh -i \"$SSH_KEY\" ${_ssh_common[*]}"
else
  _rsync_e="ssh ${_ssh_common[*]}"
fi

# ssh_run lives in scripts/lib/cfvpn-common.sh (sourced below, after
# PROJECT_ROOT is known) and is ARGV-ONLY: pass a command and its arguments,
# never a shell snippet. Remote scripts go on stdin: ssh_run bash -s <<'EOF'.

: "${USER1_NAME:=user1}"
: "${MODE:=auto}"
: "${DOMAIN:=}"

# Normalize: lowercase for DNS/cfvpnctl, UPPERCASE for D1 storage
NODE_ID="$(echo "$NODE_ID" | tr '[:upper:]' '[:lower:]')"
DB_NODE_ID="$(echo "$NODE_ID" | tr '[:lower:]' '[:upper:]')"

# NODE_LABEL is the panel display name; defaults to DB_NODE_ID if not provided
: "${NODE_LABEL:=$DB_NODE_ID}"

if ! [[ "$NODE_ID" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]; then
  die "NODE_ID must be a DNS label: a-z, 0-9, '-', no leading/trailing dash, max 63 chars (got: $NODE_ID)"
fi

if ! [[ "$USER1_NAME" =~ ^[A-Za-z0-9_-]{1,32}$ ]]; then
  die "USER1_NAME must match ^[A-Za-z0-9_-]{1,32}\$ (got: $USER1_NAME)"
fi

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ -f "$PROJECT_ROOT/go.mod" ] || die "go.mod not found; run from inside the cf-vpn repository"
LIB_DIR="$PROJECT_ROOT/scripts/lib"
# Path of the env-file helper AFTER the repo has been rsynced to the target.
REMOTE_PROJ="/opt/cf-vpn"
REMOTE_ENV_HELPER="$REMOTE_PROJ/scripts/lib/cfvpn-env-file.sh"

# shellcheck source=lib/cfvpn-common.sh
. "$LIB_DIR/cfvpn-common.sh"

FORCE_REINSTALL="${FORCE_REINSTALL:-0}"

if [ -z "${AGENT_SHARED_SECRET:-}" ]; then
  AGENT_SHARED_SECRET="$(openssl rand -hex 32)"
fi

# The env file is written unquoted (internal/state/store.go keeps everything
# after the first '=' verbatim) and is re-read by systemd EnvironmentFile=.
# Reject values that cannot survive that instead of corrupting the file — and,
# since these values are interpolated into a remote root shell's stdin, this
# also removes the last place a '$' or backtick could matter.
cfvpn_require_env_value CF_API_TOKEN  "$CF_API_TOKEN"
cfvpn_require_env_value CF_ACCOUNT_ID "$CF_ACCOUNT_ID"
cfvpn_require_env_value AGENT_SHARED_SECRET "$AGENT_SHARED_SECRET"
[ -z "$DOMAIN" ] || cfvpn_require_env_value DOMAIN "$DOMAIN"

# shellcheck disable=SC2034  # read by d1_query() in scripts/lib/cfvpn-d1.sh
D1_DB_ID="${CFVPN_D1_DATABASE_ID:-0649f07f-e2c0-47f3-b84a-273f7f67332e}"

# d1_query / d1_zone_for_domain / node+user upserts / agent_sync
# shellcheck source=lib/cfvpn-d1.sh
. "$LIB_DIR/cfvpn-d1.sh"

# Pin the ACME client so two nodes provisioned a week apart get the same lego.
LEGO_VERSION="${LEGO_VERSION:-latest}"

# Single EXIT trap — chains stage cleanup with orphan-tunnel detection.
# Replaces the multi-trap-replacement pattern that was leaking STAGE_DIR
# whenever cfvpnctl install or any later step failed.
INSTALL_PHASE_STARTED=0
INSTALL_PHASE_DONE=0
STAGE_DIR=""
TUNNELS_BEFORE=""
TUNNELS_SNAPSHOT_OK=0

cleanup_local_stage() {
  [ -n "$STAGE_DIR" ] && [ -d "$STAGE_DIR" ] || return 0
  log "removing local stage dir: $STAGE_DIR"
  rm -rf "$STAGE_DIR"
}

list_admin_tunnels_local() {
  local resp
  resp=$(curl -sS --max-time 10 --config - <<EOF
header = "Authorization: Bearer ${CF_API_TOKEN}"
url = "https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/cfd_tunnel?is_deleted=false&per_page=100"
EOF
)
  # Return non-zero (no output) on transport/API error so callers can tell "no
  # tunnels" apart from "couldn't list" — otherwise a failed pre-install snapshot
  # would make every existing tunnel look like a new orphan.
  [ -n "$resp" ] && [ "$(printf '%s' "$resp" | jq -r '.success // false' 2>/dev/null)" = "true" ] || return 1
  printf '%s' "$resp" | jq -r '.result // [] | .[] | select(.name | startswith("cfvpn-admin-")) | "\(.id) \(.name)"'
}

_EXIT_HANDLED=0
exit_handler() {
  local rc=$?
  # On Ctrl-C bash runs the INT trap and then, because this handler exits, the
  # EXIT trap as well — without this guard the whole diff would run twice.
  [ "$_EXIT_HANDLED" -eq 1 ] && exit $rc
  _EXIT_HANDLED=1
  cleanup_local_stage
  if [ "$rc" -ne 0 ] && [ "$INSTALL_PHASE_STARTED" -eq 1 ] && [ "$INSTALL_PHASE_DONE" -eq 0 ]; then
    warn "cfvpnctl install was interrupted (exit $rc) — checking for orphan admin tunnels"
    local after new tid tname
    if [ "${TUNNELS_SNAPSHOT_OK:-0}" -ne 1 ]; then
      warn "  pre-install tunnel snapshot unavailable — skipping orphan diff (check CF dashboard)"
      exit $rc
    fi
    if ! after="$(list_admin_tunnels_local)"; then
      warn "  could not list admin tunnels after interruption — check CF dashboard manually"
      exit $rc
    fi
    after="$(printf '%s\n' "$after" | sort)"
    new="$(comm -13 <(printf '%s\n' "$TUNNELS_BEFORE") <(printf '%s\n' "$after"))"
    if [ -n "$new" ]; then
      warn "orphan admin tunnels detected:"
      while IFS= read -r line; do
        [ -z "$line" ] && continue
        tid=$(echo "$line" | cut -d' ' -f1)
        tname=$(echo "$line" | cut -d' ' -f2-)
        warn "  $tid ($tname)"
        warn "  clean up: ssh${SSH_KEY:+ -i $SSH_KEY} $TARGET_HOST cfvpnctl rotate-domain --cleanup $tid --yes"
      done <<< "$new"
    fi
  fi
  exit $rc
}
# INT/TERM as well: a Ctrl-C mid-install leaks both the local stage dir (which
# holds no secrets but 300MB) and a half-created admin tunnel.
trap exit_handler EXIT INT TERM

log "verifying SSH access to $TARGET_HOST"
ssh_run true || die "SSH to $TARGET_HOST failed — check TARGET_HOST / SSH_KEY"
log "SSH OK"

# Everything below installs into /usr/local/bin and /etc on the target.
target_uid="$(ssh_run id -u)"
[ "$target_uid" = "0" ] || die "TARGET_HOST must connect as root (got uid=$target_uid)"

# Every staged binary is linux/amd64. On any other arch they install cleanly
# and then fail with "Exec format error" at service start.
target_arch="$(ssh_run uname -m)"
[ "$target_arch" = "x86_64" ] || die "target arch is $target_arch; this installer stages linux/amd64 binaries only (need x86_64)"
log "target: root@$target_arch"

# Refuse to clobber an already-provisioned node BEFORE downloading ~300MB and
# creating Cloudflare resources. Mirrors scripts/lib/cfvpn-env-file.sh check —
# the helper itself is not on the target yet (it arrives with the rsync).
if [ "$FORCE_REINSTALL" != "1" ]; then
  if ssh_run bash -s <<'REMOTE' >/dev/null 2>&1
[ -f /etc/cfvpn/.installed ] && exit 0
grep -qE '^(REALITY_PRIVATE_KEY|ADMIN_TUNNEL_UUID)=' /etc/cfvpn/cfvpn.env 2>/dev/null
REMOTE
  then
    die "$TARGET_HOST is already provisioned (/etc/cfvpn/.installed, or an env
  file holding installer-generated keys).
  Re-running this installer regenerates the Reality keypair and every user
  credential, breaking every client already configured for this node.
  Upgrade in place with:  ssh$([ -n "$SSH_KEY" ] && echo " -i $SSH_KEY") $TARGET_HOST cfvpnctl upgrade
  Or re-run with FORCE_REINSTALL=1 (the env file is backed up first)."
  fi
fi

# ----- 1. resolve MODE --------------------------------------------------------
resolve_mode() {
  case "$MODE" in
    direct|cloudflare) return 0 ;;
    auto) ;;
    *) die "MODE must be direct, cloudflare, or auto (got: $MODE)" ;;
  esac
  # The probe is a SCRIPT, so it goes on stdin. Passing it as a single argv
  # element made the remote look for a command literally named `awk "NR>1 …"`,
  # exit 127, and MODE=auto always resolved to direct.
  local rc=0
  ssh_run bash -s <<'REMOTE' >/dev/null 2>&1 || rc=$?
set -u
# /proc/net/tcp{,6}: col 2 is local_address (hex ip:port), col 4 is state.
# :01BB = port 443, state 0A = TCP_LISTEN.
awk 'NR>1 && toupper($2) ~ /:01BB$/ && $4 == "0A" { found = 1 }
     END { exit !found }' /proc/net/tcp /proc/net/tcp6 2>/dev/null
REMOTE
  case "$rc" in
    0) warn "port :443 already in LISTEN on target; selecting MODE=cloudflare"
       MODE=cloudflare ;;
    1) MODE=direct ;;
    *) die "could not probe :443 on $TARGET_HOST (ssh/awk exit $rc) — refusing to guess MODE; pass MODE=direct or MODE=cloudflare" ;;
  esac
}
resolve_mode
log "selected MODE=$MODE"

# ----- 2. OS dependencies on target (minimal) ---------------------------------
# curl is required: step 3's GFW preflight does its HTTPS checks with the
# target's own curl, and nothing else ships one any more (the unverifiable
# static curl that used to be staged here was dropped). ca-certificates must
# land in the same step so those HTTPS checks can complete a TLS handshake.
log "installing minimal OS dependencies on $TARGET_HOST"
ssh_run bash <<'REMOTE'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -y -q
PACKAGES=(ca-certificates curl ufw)
TO_INSTALL=()
for pkg in "${PACKAGES[@]}"; do
  dpkg -s "$pkg" >/dev/null 2>&1 || TO_INSTALL+=("$pkg")
done
if [ ${#TO_INSTALL[@]} -gt 0 ]; then
  echo "[install-node-CN] installing: ${TO_INSTALL[*]}"
  apt-get install -y "${TO_INSTALL[@]}"
else
  echo "[install-node-CN] apt packages already present"
fi
REMOTE

# ----- 3. preflight TARGET (GFW connectivity) --------------------------------
# Fail FAST before downloading 100MB of binaries or creating any CF resources.
# All checks below run on the China VPS; ca-certificates is now in place so
# system curl can do TLS handshakes.
log "running GFW preflight on $TARGET_HOST"

# 3a. Clock skew local↔target — ACME requires <5min drift
local_ts=$(date -u +%s)
remote_ts=$(ssh_run date -u +%s)
skew=$((local_ts - remote_ts))
abs_skew=${skew#-}
if [ "$abs_skew" -gt 300 ]; then
  die "clock skew local↔target = ${skew}s exceeds 5min — TLS/ACME will fail. Fix: ssh${SSH_KEY:+ -i $SSH_KEY} $TARGET_HOST timedatectl set-ntp true"
elif [ "$abs_skew" -gt 60 ]; then
  warn "clock skew local↔target = ${skew}s (acceptable but tight)"
else
  log "  clock skew local↔target = ${skew}s (OK)"
fi

# 3b. Network reachability checks on target
preflight_rc=0
ssh_run bash <<'REMOTE' || preflight_rc=$?
# NOTE: deliberately NOT using set -e — we want to count ALL failures,
# not exit on the first one, so the operator sees the full picture.
PASS=0; FAIL=0; WARN=0
ok()   { printf '  [OK]   %s\n' "$*"; PASS=$((PASS+1)); }
fail_(){ printf '  [FAIL] %s\n' "$*" >&2; FAIL=$((FAIL+1)); }
warn_(){ printf '  [WARN] %s\n' "$*" >&2; WARN=$((WARN+1)); }

# DNS resolution — uses /etc/resolv.conf, no external tool needed
for h in api.cloudflare.com acme-v02.api.letsencrypt.org pkg.cloudflare.com; do
  if getent hosts "$h" >/dev/null 2>&1; then
    ok "DNS resolves $h"
  else
    fail_ "DNS cannot resolve $h"
  fi
done

# TCP 443 reachability — bash builtin, no nc needed
tcp_check() {
  local host="$1" port="$2"
  timeout 5 bash -c "echo >/dev/tcp/$host/$port" >/dev/null 2>&1
}
for h in api.cloudflare.com acme-v02.api.letsencrypt.org; do
  if tcp_check "$h" 443; then
    ok "TCP $h:443 reachable"
  else
    fail_ "TCP $h:443 unreachable (likely GFW)"
  fi
done

# HTTPS — verifies TLS handshake works (depends on ca-certificates + correct clock)
api_code=$(curl -sS --max-time 10 -o /dev/null -w '%{http_code}' \
  https://api.cloudflare.com/client/v4/ips 2>/dev/null || echo 000)
if [ "$api_code" = "200" ]; then
  ok "HTTPS api.cloudflare.com → 200"
else
  fail_ "HTTPS api.cloudflare.com → $api_code (TLS or GFW issue)"
fi

acme_code=$(curl -sS --max-time 15 -o /dev/null -w '%{http_code}' \
  https://acme-v02.api.letsencrypt.org/directory 2>/dev/null || echo 000)
if [ "$acme_code" = "200" ]; then
  ok "HTTPS acme-v02.api.letsencrypt.org → 200"
else
  fail_ "HTTPS Let's Encrypt → $acme_code (cert issuance will fail)"
fi

# Cloudflare authoritative DNS UDP/53 — used by lego DNS-01 propagation check
# Falls back to recursive DNS via LEGO_DNS_RESOLVERS if blocked
if timeout 3 bash -c 'echo >/dev/udp/173.245.59.111/53' >/dev/null 2>&1; then
  ok "Cloudflare auth DNS 173.245.59.111:53 reachable"
else
  warn_ "Cloudflare auth DNS UDP blocked — using recursive DNS for lego (slower)"
fi

# NTP sync status (best-effort — informational)
if command -v timedatectl >/dev/null 2>&1; then
  # capture before grep -q: with a closed pipe the left-hand side's SIGPIPE
  # status would decide the test under pipefail
  td_status="$(timedatectl status 2>/dev/null || true)"
  if grep -qi "synchronized: yes" <<<"$td_status"; then
    ok "system clock NTP-synchronized"
  else
    warn_ "system clock not NTP-synchronized — fix: timedatectl set-ntp true"
  fi
fi

echo
echo "[install-node-CN] preflight summary: pass=$PASS warn=$WARN fail=$FAIL"
exit $FAIL
REMOTE
if [ "$preflight_rc" -ne 0 ]; then
  die "target preflight: $preflight_rc critical failure(s) — fix GFW connectivity before proceeding"
fi
log "target preflight passed"

# ----- 4. download GFW-blocked binaries to local STAGE_DIR --------------------
STAGE_DIR="$(mktemp -d /tmp/cfvpn-cn-stage.XXXXXX)"
STAGE_BIN="$STAGE_DIR/bin"
STAGE_SHARE="$STAGE_DIR/share/xray"
mkdir -p "$STAGE_BIN" "$STAGE_SHARE" "$STAGE_DIR/tmp"

log "downloading binaries locally → $STAGE_DIR"

gh_latest_tag() {
  curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --retry 3 --retry-connrefused --max-time 15 \
    "https://api.github.com/repos/$1/releases/latest" \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['tag_name'])"
}

# ---- [1/5] xray binary -------------------------------------------------------
log "  [1/5] xray   (github.com/XTLS/Xray-core)"
xray_ver=$(gh_latest_tag XTLS/Xray-core)
xray_base="https://github.com/XTLS/Xray-core/releases/download/${xray_ver}"
cfvpn_download_verified \
  "$xray_base/Xray-linux-64.zip" "$STAGE_DIR/tmp/xray.zip" "Xray-linux-64.zip" \
  "$xray_base/Xray-linux-64.zip.dgst"
unzip -p "$STAGE_DIR/tmp/xray.zip" xray > "$STAGE_BIN/xray"
chmod 755 "$STAGE_BIN/xray"
log "    xray ${xray_ver} ($(du -sh "$STAGE_BIN/xray" | cut -f1))"

# ---- [2/5] xray geo data -----------------------------------------------------
log "  [2/5] xray geo data  (github.com/v2fly)"
cfvpn_download_verified \
  "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat" \
  "$STAGE_SHARE/geoip.dat" "geoip.dat" \
  "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat.sha256sum"
cfvpn_download_verified \
  "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat" \
  "$STAGE_SHARE/geosite.dat" "dlc.dat" \
  "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat.sha256sum"
log "    geoip $(du -sh "$STAGE_SHARE/geoip.dat" | cut -f1), geosite $(du -sh "$STAGE_SHARE/geosite.dat" | cut -f1)"

# ---- [3/5] cloudflared .deb (from Cloudflare official apt repo) -------------
# The apt Packages index is itself the checksum source: it carries SHA256 for
# every .deb, so no extra fetch is needed.
log "  [3/5] cloudflared   (pkg.cloudflare.com apt repo)"
CF_APT_BASE="https://pkg.cloudflare.com/cloudflared"
CF_PACKAGES=$(curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --retry 3 --retry-connrefused --max-time 30 \
  "$CF_APT_BASE/dists/any/main/binary-amd64/Packages")
CF_FILENAME=$(printf '%s\n' "$CF_PACKAGES" | awk '/^Filename:/{print $2; exit}')
[ -z "$CF_FILENAME" ] && die "cloudflared: cannot parse Packages index from $CF_APT_BASE"
# Take Version/SHA256 from the stanza that actually declares the file we are
# about to download (records are blank-line separated) — pairing our Filename
# with another package's hash would be worse than not checking at all.
CF_STANZA=$(printf '%s\n' "$CF_PACKAGES" \
  | awk -v want="Filename: $CF_FILENAME" 'BEGIN{RS="";ORS=""} index($0, want){print $0"\n"; exit}')
CF_VER=$(printf '%s\n'    "$CF_STANZA" | awk '/^Version:/{print $2; exit}')
CF_SHA256=$(printf '%s\n' "$CF_STANZA" | awk '/^SHA256:/{print $2; exit}')
cfvpn_curl_dl "$CF_APT_BASE/$CF_FILENAME" "$STAGE_DIR/tmp/cloudflared.deb"
if [ -n "$CF_SHA256" ]; then
  cfvpn_verify_sha256 "$STAGE_DIR/tmp/cloudflared.deb" "$CF_SHA256" "cloudflared.deb"
elif [ "${CFVPN_ALLOW_UNVERIFIED_DOWNLOADS:-0}" = "1" ]; then
  warn "cloudflared.deb: Packages index carries no SHA256 — continuing because CFVPN_ALLOW_UNVERIFIED_DOWNLOADS=1"
else
  die "cloudflared.deb: Packages index carries no SHA256 for $CF_FILENAME — refusing to install it unverified"
fi
log "    cloudflared ${CF_VER} deb ($(du -sh "$STAGE_DIR/tmp/cloudflared.deb" | cut -f1))"

# ---- [4/5] hysteria2 ---------------------------------------------------------
log "  [4/5] hysteria2   (github.com/apernet/hysteria)"
hy_url=$(curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --retry 3 --retry-connrefused --max-time 15 \
  https://api.github.com/repos/apernet/hysteria/releases/latest \
  | python3 -c "
import sys,json
r=json.load(sys.stdin)
print(next((a['browser_download_url'] for a in r.get('assets',[])
            if a['name']=='hysteria-linux-amd64'),''))
")
[ -z "$hy_url" ] && die "hysteria linux-amd64 asset not found"
cfvpn_download_verified "$hy_url" "$STAGE_BIN/hysteria" "hysteria-linux-amd64" \
  "${hy_url}.sha256" "${hy_url%/*}/hashes.txt"
chmod 755 "$STAGE_BIN/hysteria"
log "    hysteria2 ($(du -sh "$STAGE_BIN/hysteria" | cut -f1))"

# ---- [5/5] jq static binary --------------------------------------------------
log "  [5/5] jq   (github.com/jqlang/jq)"
jq_ver=$(gh_latest_tag jqlang/jq)
jq_base="https://github.com/jqlang/jq/releases/download/${jq_ver}"
cfvpn_download_verified "$jq_base/jq-linux-amd64" "$STAGE_BIN/jq" "jq-linux-amd64" \
  "$jq_base/sha256sum.txt" "$jq_base/jq-linux-amd64.sha256"
chmod 755 "$STAGE_BIN/jq"
log "    jq ${jq_ver} ($(du -sh "$STAGE_BIN/jq" | cut -f1))"

# NOTE: the static curl from stunnel/static-curl used to be staged here as
# [6/6]. It was dropped: that project publishes NO checksum asset (verified),
# so it could only ever be installed unverified as root, and nothing in the
# flow needs it — the target gets curl + ca-certificates from apt in step 2
# (which is what the GFW preflight uses), and cfvpnctl / cfvpn-agent are Go
# binaries
# that speak HTTPS themselves. All D1 and agent calls run on the operator box.

# ----- 5. build locally (linux/amd64, CGO_ENABLED=0) -------------------------
# CGO_ENABLED=0 forces static binaries — avoids glibc-version mismatch on
# Aliyun/Tencent Cloud images that may run older glibc than the local builder.

log "building cfvpnctl + cfvpn-agent  (GOOS=linux GOARCH=amd64 CGO_ENABLED=0)"
(
  cd "$PROJECT_ROOT"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/cfvpnctl    ./cmd/cfvpnctl
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/cfvpn-agent ./cmd/cfvpn-agent
)
install -m 755 "$PROJECT_ROOT/bin/cfvpnctl"    "$STAGE_BIN/cfvpnctl"
install -m 755 "$PROJECT_ROOT/bin/cfvpn-agent" "$STAGE_BIN/cfvpn-agent"

log "building lego via go install  ($LEGO_VERSION, CGO_ENABLED=0)"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOBIN="$STAGE_BIN" \
  go install "github.com/go-acme/lego/v4/cmd/lego@${LEGO_VERSION}"
log "    lego ($(du -sh "$STAGE_BIN/lego" | cut -f1))"

log "building openssl CLI from source  (github.com/openssl/openssl)"
ossl_tag=$(gh_latest_tag openssl/openssl)
ossl_ver="${ossl_tag#openssl-}"
ossl_base="https://github.com/openssl/openssl/releases/download/${ossl_tag}"
log "  openssl source: ${ossl_tag}"
cfvpn_download_verified \
  "$ossl_base/openssl-${ossl_ver}.tar.gz" "$STAGE_DIR/tmp/openssl.tar.gz" \
  "openssl-${ossl_ver}.tar.gz" \
  "$ossl_base/openssl-${ossl_ver}.tar.gz.sha256" \
  "$ossl_base/openssl-${ossl_ver}.tar.gz.sha256sum"
tar -xzf "$STAGE_DIR/tmp/openssl.tar.gz" -C "$STAGE_DIR/tmp/"
(
  cd "$STAGE_DIR/tmp/openssl-${ossl_ver}"
  ./Configure linux-x86_64 no-shared no-tests --prefix=/tmp/ossl-unused
  make -j"$(nproc)" build_programs 2>&1 | tail -5
)
install -m 755 "$STAGE_DIR/tmp/openssl-${ossl_ver}/apps/openssl" "$STAGE_BIN/openssl"
log "  openssl ${ossl_ver} built ($(du -sh "$STAGE_BIN/openssl" | cut -f1))"

log "local stage ready: $(ls "$STAGE_BIN/" | tr '\n' ' ')"

# ----- 6. D1 zone check (informational, local) -------------------------------
# d1_query always prints valid JSON, so the guards below are enough.
D1_RESP=$(d1_query "$(jq -n '{sql:"SELECT id,label,zone,vpn_host FROM nodes WHERE zone != ?", params:[""]}')")
D1_OK=$(echo "$D1_RESP" | jq -r '.success // false')
if [ "$D1_OK" = "true" ]; then
  D1_ROWS=$(echo "$D1_RESP" | jq '.result[0].results // []')
  NODE_COUNT=$(echo "$D1_ROWS" | jq 'length')
  log "D1 existing nodes: $NODE_COUNT"
  [ "$NODE_COUNT" -gt 0 ] && \
    echo "$D1_ROWS" | jq -r \
      'group_by(.zone) | .[] | "  \(.[0].zone): \(map(.id+"("+.vpn_host+")") | join(", "))"' \
    || true
else
  warn "D1 zone check failed (non-fatal)"
fi

# ----- 7. rsync repo + stage to target ---------------------------------------
# Predictable /tmp paths are guessable by any local user on the target; let the
# target pick the name.
REMOTE_STAGE="$(ssh_run mktemp -d /tmp/cfvpn-stage.XXXXXX)"
[ -n "$REMOTE_STAGE" ] || die "could not create a stage dir on $TARGET_HOST"

log "rsyncing repo → $TARGET_HOST:$REMOTE_PROJ"
# rsync does NOT honour .gitignore on its own: without the filter this pushes
# node_modules/ and .wrangler/ (~300MB) and, worse, any local .env /
# .dev.vars / *.key the operator keeps in the checkout, to a VPS in China.
rsync -az --delete \
  --filter=':- .gitignore' \
  --exclude='.git/' \
  --exclude='bin/' \
  --exclude='*.log' \
  --exclude='node_modules/' \
  --exclude='.wrangler/' \
  --exclude='.env*' \
  --exclude='.dev.vars*' \
  --exclude='*.key' \
  --exclude='*.pem' \
  -e "$_rsync_e" \
  "$PROJECT_ROOT/" "$TARGET_HOST:$REMOTE_PROJ/"

log "rsyncing stage → $TARGET_HOST:$REMOTE_STAGE"
rsync -az \
  -e "$_rsync_e" \
  "$STAGE_DIR/" "$TARGET_HOST:$REMOTE_STAGE/"

# ----- 8. install staged binaries on target ----------------------------------
log "installing staged binaries on $TARGET_HOST"
# CFVPN_STAGE is not a secret; the heredoc stays quoted so nothing else can be
# interpolated into the remote root shell.
ssh_run env "CFVPN_STAGE=$REMOTE_STAGE" bash -s <<'REMOTE'
set -euo pipefail
S="${CFVPN_STAGE:?stage dir not passed}"
echo "[install-node-CN] installing cloudflared from .deb"
# dpkg -i can fail if cloudflared declares deps not yet installed; apt -f
# resolves them from the local apt cache (Aliyun/Tencent mirror).
if ! dpkg -i "$S/tmp/cloudflared.deb"; then
  echo "[install-node-CN] resolving missing deps with apt-get install -f"
  apt-get install -y -f
fi
for b in xray hysteria lego cfvpnctl cfvpn-agent jq openssl; do
  install -m 755 "$S/bin/$b" "/usr/local/bin/$b"
done
install -d /usr/local/share/xray /usr/local/etc/xray /var/log/xray
install -m 644 "$S/share/xray/geoip.dat"   /usr/local/share/xray/geoip.dat
install -m 644 "$S/share/xray/geosite.dat" /usr/local/share/xray/geosite.dat
echo "[install-node-CN] installed binaries:"
for b in xray cloudflared lego hysteria cfvpnctl cfvpn-agent jq openssl; do
  # cloudflared comes from the .deb and lands in /usr/bin, not /usr/local/bin —
  # resolving through PATH stops the banner from printing '?' for it.
  p=$(command -v "$b" || true)
  if [ -z "$p" ]; then
    printf "  %-14s %s\n" "$b" "MISSING"
    continue
  fi
  ver=$("$p" --version 2>&1 | head -1 || true)
  [ -n "$ver" ] || ver=$("$p" version 2>&1 | head -1 || true)
  printf "  %-14s %s\n" "$b" "${ver:-?}"
done
REMOTE

# ----- 9. write /etc/cfvpn/cfvpn.env on target -------------------------------
# Includes LEGO_DNS_RESOLVERS + LEGO_DISABLE_CP to make DNS-01 fast behind GFW
# (Cloudflare auth DNS may be UDP-blocked → fall back to recursive resolvers).
#
# The file is composed LOCALLY and streamed over ssh's stdin. Nothing is
# interpolated into a remote shell (the old unquoted heredoc expanded
# CF_API_TOKEN etc. locally and let a '$', backtick or quote inside a token run
# as root on the target), and no secret ever appears in argv on either side.
# The remote writer is the same scripts/lib/cfvpn-env-file.sh the plain
# installer uses, so the re-install guard, the backup and the key-preserving
# merge behave identically on both paths.
log "writing /etc/cfvpn/cfvpn.env on $TARGET_HOST"
{
  printf 'CF_API_TOKEN=%s\n'        "$CF_API_TOKEN"
  printf 'CF_ACCOUNT_ID=%s\n'       "$CF_ACCOUNT_ID"
  printf 'NODE_ID=%s\n'             "$NODE_ID"
  printf 'USER1_NAME=%s\n'          "$USER1_NAME"
  printf 'MODE=%s\n'                "$MODE"
  printf 'AGENT_SHARED_SECRET=%s\n' "$AGENT_SHARED_SECRET"
  printf 'LEGO_DNS_RESOLVERS=%s\n'  "1.1.1.1:53,8.8.8.8:53"
  printf 'LEGO_DISABLE_CP=%s\n'     "1"
  # `|| true`: this group is the left side of a pipe and `set -o pipefail` is
  # on, so a false AND-list here would abort the whole script.
  { [ -n "$DOMAIN" ] && printf 'DOMAIN=%s\n' "$DOMAIN"; } || true
} | ssh_run env "FORCE_REINSTALL=$FORCE_REINSTALL" bash "$REMOTE_ENV_HELPER" write

# ----- 10. firewall hygiene on target ----------------------------------------
ssh_run bash <<'REMOTE'
# Capture first: `ufw status | grep -q` closes the pipe on the first match, and
# under `set -o pipefail` the SIGPIPE from ufw becomes the status of the test —
# skipping the SSH allow rule on exactly the hosts that have ufw enabled.
if command -v ufw >/dev/null 2>&1; then
  ufw_status="$(ufw status 2>/dev/null || true)"
  if grep -q 'Status: active' <<<"$ufw_status"; then
    echo "[install-node-CN] ufw active — ensuring SSH stays open"
    ufw allow OpenSSH || ufw allow 22/tcp \
      || echo "[install-node-CN] WARN: could not whitelist SSH; verify manually"
  fi
fi
REMOTE

# ----- 11. run cfvpnctl install on target ------------------------------------
log "running cfvpnctl install on $TARGET_HOST (mode=$MODE)"
if TUNNELS_BEFORE="$(list_admin_tunnels_local)"; then
  TUNNELS_BEFORE="$(printf '%s\n' "$TUNNELS_BEFORE" | sort)"
  TUNNELS_SNAPSHOT_OK=1
else
  warn "could not list admin tunnels before install (CF API) — orphan detection disabled"
fi
INSTALL_PHASE_STARTED=1
ssh_run cfvpnctl install
INSTALL_PHASE_DONE=1
# Arm the re-install guard only now: everything irreplaceable (Reality keypair,
# admin tunnel, user credentials) exists from this point on. A run that died
# earlier stays retryable.
ssh_run bash "$REMOTE_ENV_HELPER" mark-installed

log "installing healthcheck timer"
ssh_run cfvpnctl healthcheck install

# ----- 12. verify systemd units ----------------------------------------------
log "verifying systemd units on $TARGET_HOST"
# `Restart=on-failure` + `RestartSec=3` means a crash-looping unit still reports
# `active` two seconds in — wait long enough for a restart to show up, and fail
# the run instead of publishing a broken node to D1.
ssh_run bash <<'REMOTE' || die "systemd units on the target are unhealthy — node not published to D1; inspect with: journalctl -u cfvpn-xray -n 80"
sleep 8
failures=0
for u in cfvpn-xray cfvpn-hysteria cfvpn-cloudflared cfvpn-agent; do
  state=$(systemctl is-active "$u" 2>/dev/null || echo unknown)
  restarts=$(systemctl show -p NRestarts --value "$u" 2>/dev/null || echo 0)
  case "$restarts" in ''|*[!0-9]*) restarts=0 ;; esac
  if [ "$state" = active ] && [ "$restarts" -eq 0 ]; then
    echo "[install-node-CN]   $u: active (NRestarts=0)"
  else
    echo "[install-node-CN] WARN:   $u: state=$state NRestarts=$restarts — check: journalctl -u $u -n 80"
    failures=$((failures + 1))
  fi
done
exit "$failures"
REMOTE

ssh_run cfvpnctl healthcheck run \
  || warn "healthcheck reported failure (tunnel may still be registering — re-run in 60s)"

# ----- 13. D1 sync: upsert node + user + user_nodes --------------------------
log "syncing $DB_NODE_ID + user $USER1_NAME to D1"

# Read runtime values written by cfvpnctl install.
#
# This MUST be a heredoc on stdin, not `ssh_run bash -c '<multiline>'`: ssh
# joins argv with spaces and the remote login shell re-parses the result, so
# the local quotes were eaten and the remote ran `bash -c .` followed by a
# dozen printfs against unset variables — every value came back EMPTY with
# rc=0, and every CN node provisioned fine and then never reached D1.
REMOTE_ENV=$(ssh_run bash -s <<'REMOTE'
set -euo pipefail
[ -r /etc/cfvpn/cfvpn.env ] || { echo "cfvpn.env unreadable on target" >&2; exit 1; }
. /etc/cfvpn/cfvpn.env
printf "DOMAIN=%s\n"              "${DOMAIN:-}"
printf "HY2_HOST=%s\n"            "${HY2_HOST:-}"
printf "HY2_PORT=%s\n"            "${HY2_PORT:-}"
printf "HY2_OBFS_PW=%s\n"         "${HY2_OBFS_PW:-}"
printf "HY2_PASS_USER1=%s\n"      "${HY2_PASS_USER1:-}"
printf "UUID_USER1=%s\n"          "${UUID_USER1:-}"
printf "PUBLIC_IP=%s\n"           "${PUBLIC_IP:-}"
printf "ADMIN_HOST=%s\n"          "${ADMIN_HOST:-}"
printf "REALITY_PUBLIC_KEY=%s\n"  "${REALITY_PUBLIC_KEY:-}"
printf "REALITY_SHORT_ID=%s\n"    "${REALITY_SHORT_ID:-}"
printf "REALITY_SNI=%s\n"         "${REALITY_SNI:-}"
printf "REALITY_DEST=%s\n"        "${REALITY_DEST:-}"
REMOTE
) || die "could not read /etc/cfvpn/cfvpn.env back from $TARGET_HOST"
while IFS='=' read -r key val; do
  [ -z "$key" ] && continue
  case "$key" in
    DOMAIN|HY2_HOST|HY2_PORT|HY2_OBFS_PW|HY2_PASS_USER1|UUID_USER1| \
    PUBLIC_IP|ADMIN_HOST|REALITY_PUBLIC_KEY|REALITY_SHORT_ID|REALITY_SNI|REALITY_DEST)
      export "$key=$val" ;;
  esac
done <<< "$REMOTE_ENV"

# Validate every value the D1 row needs. An empty one means the read above came
# back blank (the C3 failure mode) — never write a half-empty node row.
# REALITY_* are deliberately not required: cfvpnctl only writes them in
# MODE=direct.
for _k in DOMAIN HY2_HOST HY2_PORT HY2_OBFS_PW HY2_PASS_USER1 UUID_USER1 PUBLIC_IP ADMIN_HOST; do
  [ -n "${!_k:-}" ] || die "$_k not populated by cfvpnctl install (read back empty from $TARGET_HOST) — refusing to write an incomplete node row to D1"
done

# shellcheck disable=SC2034  # read by the d1_* helpers in scripts/lib/cfvpn-d1.sh
NOW_MS=$(date +%s%3N)
ZONE="$(d1_zone_for_domain "$DOMAIN")"
# A blank DOMAIN yields a blank ZONE, which would write the node to D1 with
# zone='' and silently defeat the zone-collision check. Fail loudly instead.
if [ -z "$DOMAIN" ] || [ -z "$ZONE" ]; then
  die "DOMAIN is empty after cfvpnctl install — cannot derive zone; refusing to upsert node with zone=''"
fi

# 13a. node row / 13b. user row / 13c. user_nodes binding / 13d. agent sync.
# All four live in scripts/lib/cfvpn-d1.sh, shared with install-node.sh — the
# CN copy used to drift (full sub_token in the log, secrets on the curl argv,
# no .success guard on the user lookup).
d1_upsert_node
d1_ensure_user
d1_upsert_user_nodes
log "calling agent sync via $ADMIN_HOST"
agent_sync

# ----- 14. cleanup remote stage dir -------------------------------------------
log "removing remote stage dir on $TARGET_HOST"
ssh_run rm -rf "$REMOTE_STAGE"
# Local STAGE_DIR removed by single EXIT trap (exit_handler → cleanup_local_stage)

log "node ready: $DB_NODE_ID ($NODE_LABEL)"
log "next: ssh${SSH_KEY:+ -i $SSH_KEY} $TARGET_HOST cfvpnctl status"
log "next: ssh${SSH_KEY:+ -i $SSH_KEY} $TARGET_HOST cfvpnctl gen-sub $USER1_NAME"
