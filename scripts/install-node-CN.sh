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
#   2. OS deps on target — minimal apt: ca-certificates + ufw
#   3. preflight TARGET — DNS / TCP443 / HTTPS / clock-skew (FAIL-FAST before
#                         any heavy download or resource creation)
#   4. download — fetch GFW-blocked binaries/packages to local STAGE_DIR:
#        [1/6] xray binary          github.com/XTLS/Xray-core
#        [2/6] xray geo data        github.com/v2fly
#        [3/6] cloudflared .deb     pkg.cloudflare.com (apt repo)
#        [4/6] hysteria2            github.com/apernet/hysteria
#        [5/6] jq                   github.com/jqlang/jq
#        [6/6] curl (static glibc)  github.com/stunnel/static-curl
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

set -euo pipefail

log()  { printf '[install-node-CN] %s\n' "$*"; }
warn() { printf '[install-node-CN] WARN: %s\n' "$*" >&2; }
die()  { printf '[install-node-CN] ERROR: %s\n' "$*" >&2; exit 1; }

require_env() {
  local name="$1"
  [ -n "${!name:-}" ] || die "$name is required"
}

# ----- 0. preflight LOCAL -----------------------------------------------------
for cmd in curl python3 go gcc make unzip rsync ssh openssl jq xz; do
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
  https://pkg.cloudflare.com/cloudflared/debian/dists/any/InRelease \
  || die "local cannot reach pkg.cloudflare.com (needed for cloudflared .deb)"
# proxy.golang.org is needed by `go install`; warn-only since GOPROXY can be reset
if ! curl -fsS --max-time 10 -o /dev/null https://proxy.golang.org 2>/dev/null; then
  warn "proxy.golang.org unreachable from local — go install may be slow"
  warn "if local is in China, set: export GOPROXY=https://goproxy.cn,direct"
fi
log "local network OK"

SSH_KEY="${SSH_KEY:-}"
_ssh_opts=(-o StrictHostKeyChecking=no -o ConnectTimeout=15 -o BatchMode=yes)
# Quote SSH_KEY in case it contains spaces; rsync passes -e as a single string
if [ -n "$SSH_KEY" ]; then
  _ssh_opts+=(-i "$SSH_KEY")
  _rsync_e="ssh -i \"$SSH_KEY\" -o StrictHostKeyChecking=no -o ConnectTimeout=15 -o BatchMode=yes"
else
  _rsync_e="ssh -o StrictHostKeyChecking=no -o ConnectTimeout=15 -o BatchMode=yes"
fi

ssh_run() { ssh "${_ssh_opts[@]}" "$TARGET_HOST" "$@"; }

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

SECRET_AUTOGENERATED=0
if [ -z "${AGENT_SHARED_SECRET:-}" ]; then
  AGENT_SHARED_SECRET="$(openssl rand -hex 32)"
  SECRET_AUTOGENERATED=1
fi

D1_DB_ID="${CFVPN_D1_DATABASE_ID:-0649f07f-e2c0-47f3-b84a-273f7f67332e}"

# Local D1 query helper — token stays out of argv via --config
d1_query() {
  local payload="$1"
  local tmpf; tmpf="$(mktemp)"; chmod 600 "$tmpf"
  printf '%s' "$payload" >"$tmpf"
  local resp
  resp=$(curl -sS --max-time 30 --config - <<EOF
header = "Authorization: Bearer ${CF_API_TOKEN}"
header = "Content-Type: application/json"
url = "https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/d1/database/${D1_DB_ID}/query"
data-binary = "@${tmpf}"
EOF
)
  rm -f "$tmpf"
  echo "$resp"
}

# Single EXIT trap — chains stage cleanup with orphan-tunnel detection.
# Replaces the multi-trap-replacement pattern that was leaking STAGE_DIR
# whenever cfvpnctl install or any later step failed.
INSTALL_PHASE_STARTED=0
INSTALL_PHASE_DONE=0
STAGE_DIR=""
TUNNELS_BEFORE=""

cleanup_local_stage() {
  [ -n "$STAGE_DIR" ] && [ -d "$STAGE_DIR" ] || return 0
  log "removing local stage dir: $STAGE_DIR"
  rm -rf "$STAGE_DIR"
}

list_admin_tunnels_local() {
  curl -sS --max-time 10 --config - <<EOF | jq -r '.result // [] | .[] | select(.name | startswith("cfvpn-admin-")) | "\(.id) \(.name)"' 2>/dev/null || true
header = "Authorization: Bearer ${CF_API_TOKEN}"
url = "https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/cfd_tunnel?is_deleted=false&per_page=100"
EOF
}

exit_handler() {
  local rc=$?
  cleanup_local_stage
  if [ "$rc" -ne 0 ] && [ "$INSTALL_PHASE_STARTED" -eq 1 ] && [ "$INSTALL_PHASE_DONE" -eq 0 ]; then
    warn "cfvpnctl install was interrupted (exit $rc) — checking for orphan admin tunnels"
    local after new tid tname
    after="$(list_admin_tunnels_local | sort)"
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
trap exit_handler EXIT

log "verifying SSH access to $TARGET_HOST"
ssh_run true || die "SSH to $TARGET_HOST failed — check TARGET_HOST / SSH_KEY"
log "SSH OK"

# ----- 1. resolve MODE --------------------------------------------------------
resolve_mode() {
  case "$MODE" in
    direct|cloudflare) return 0 ;;
    auto) ;;
    *) die "MODE must be direct, cloudflare, or auto (got: $MODE)" ;;
  esac
  if ssh_run \
    'awk "NR>1 && toupper(\$2)~/:01BB\$/ && \$4==\"0A\"{found=1} END{exit !found}" \
     /proc/net/tcp /proc/net/tcp6 2>/dev/null' 2>/dev/null; then
    warn "port :443 already in LISTEN on target; selecting MODE=cloudflare"
    MODE=cloudflare
  else
    MODE=direct
  fi
}
resolve_mode
log "selected MODE=$MODE"

# ----- 2. OS dependencies on target (minimal) ---------------------------------
log "installing minimal OS dependencies on $TARGET_HOST"
ssh_run bash <<'REMOTE'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -y -q
PACKAGES=(ca-certificates ufw)
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
  if timedatectl status 2>/dev/null | grep -qi "synchronized: yes"; then
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
  curl -fsSL --max-time 15 "https://api.github.com/repos/$1/releases/latest" \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['tag_name'])"
}

# ---- [1/6] xray binary -------------------------------------------------------
log "  [1/6] xray   (github.com/XTLS/Xray-core)"
xray_ver=$(gh_latest_tag XTLS/Xray-core)
curl -fsSL --retry 3 --max-time 180 \
  "https://github.com/XTLS/Xray-core/releases/download/${xray_ver}/Xray-linux-64.zip" \
  -o "$STAGE_DIR/tmp/xray.zip"
unzip -p "$STAGE_DIR/tmp/xray.zip" xray > "$STAGE_BIN/xray"
chmod 755 "$STAGE_BIN/xray"
log "    xray ${xray_ver} ($(du -sh "$STAGE_BIN/xray" | cut -f1))"

# ---- [2/6] xray geo data -----------------------------------------------------
log "  [2/6] xray geo data  (github.com/v2fly)"
curl -fsSL --retry 3 --max-time 120 \
  "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat" \
  -o "$STAGE_SHARE/geoip.dat"
curl -fsSL --retry 3 --max-time 120 \
  "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat" \
  -o "$STAGE_SHARE/geosite.dat"
log "    geoip $(du -sh "$STAGE_SHARE/geoip.dat" | cut -f1), geosite $(du -sh "$STAGE_SHARE/geosite.dat" | cut -f1)"

# ---- [3/6] cloudflared .deb (from Cloudflare official apt repo) -------------
log "  [3/6] cloudflared   (pkg.cloudflare.com apt repo)"
CF_APT_BASE="https://pkg.cloudflare.com/cloudflared/debian"
CF_PACKAGES=$(curl -fsSL --retry 3 --max-time 30 \
  "$CF_APT_BASE/dists/any/main/binary-amd64/Packages")
CF_FILENAME=$(printf '%s' "$CF_PACKAGES" | awk '/^Filename:/{print $2; exit}')
CF_VER=$(printf '%s'      "$CF_PACKAGES" | awk '/^Version:/{print $2; exit}')
[ -z "$CF_FILENAME" ] && die "cloudflared: cannot parse Packages index from $CF_APT_BASE"
curl -fsSL --retry 3 --max-time 180 \
  "$CF_APT_BASE/$CF_FILENAME" \
  -o "$STAGE_DIR/tmp/cloudflared.deb"
log "    cloudflared ${CF_VER} deb ($(du -sh "$STAGE_DIR/tmp/cloudflared.deb" | cut -f1))"

# ---- [4/6] hysteria2 ---------------------------------------------------------
log "  [4/6] hysteria2   (github.com/apernet/hysteria)"
hy_url=$(curl -fsSL --max-time 15 \
  https://api.github.com/repos/apernet/hysteria/releases/latest \
  | python3 -c "
import sys,json
r=json.load(sys.stdin)
print(next((a['browser_download_url'] for a in r.get('assets',[])
            if a['name']=='hysteria-linux-amd64'),''))
")
[ -z "$hy_url" ] && die "hysteria linux-amd64 asset not found"
curl -fsSL --retry 3 --max-time 180 "$hy_url" -o "$STAGE_BIN/hysteria"
chmod 755 "$STAGE_BIN/hysteria"
log "    hysteria2 ($(du -sh "$STAGE_BIN/hysteria" | cut -f1))"

# ---- [5/6] jq static binary --------------------------------------------------
log "  [5/6] jq   (github.com/jqlang/jq)"
jq_ver=$(gh_latest_tag jqlang/jq)
curl -fsSL --retry 3 --max-time 60 \
  "https://github.com/jqlang/jq/releases/download/${jq_ver}/jq-linux-amd64" \
  -o "$STAGE_BIN/jq"
chmod 755 "$STAGE_BIN/jq"
log "    jq ${jq_ver} ($(du -sh "$STAGE_BIN/jq" | cut -f1))"

# ---- [6/6] curl static (glibc, linux/amd64) ---------------------------------
log "  [6/6] curl static   (github.com/stunnel/static-curl)"
curl_ver=$(gh_latest_tag stunnel/static-curl)
curl -fsSL --retry 3 --max-time 180 \
  "https://github.com/stunnel/static-curl/releases/download/${curl_ver}/curl-linux-x86_64-glibc-${curl_ver}.tar.xz" \
  -o "$STAGE_DIR/tmp/curl.tar.xz"
mkdir -p "$STAGE_DIR/tmp/curl-extract"
tar -xJf "$STAGE_DIR/tmp/curl.tar.xz" -C "$STAGE_DIR/tmp/curl-extract"
curl_bin=$(find "$STAGE_DIR/tmp/curl-extract" -type f -name 'curl' -perm /111 | head -1)
[ -z "$curl_bin" ] && die "curl binary not found inside stunnel/static-curl tarball"
install -m 755 "$curl_bin" "$STAGE_BIN/curl"
log "    curl ${curl_ver} ($(du -sh "$STAGE_BIN/curl" | cut -f1))"

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

log "building lego via go install  (CGO_ENABLED=0)"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOBIN="$STAGE_BIN" \
  go install github.com/go-acme/lego/v4/cmd/lego@latest
log "    lego ($(du -sh "$STAGE_BIN/lego" | cut -f1))"

log "building openssl CLI from source  (github.com/openssl/openssl)"
ossl_tag=$(gh_latest_tag openssl/openssl)
ossl_ver="${ossl_tag#openssl-}"
log "  openssl source: ${ossl_tag}"
curl -fsSL --retry 3 --max-time 300 \
  "https://github.com/openssl/openssl/releases/download/${ossl_tag}/openssl-${ossl_ver}.tar.gz" \
  -o "$STAGE_DIR/tmp/openssl.tar.gz"
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
D1_RESP=$(d1_query "$(jq -n '{sql:"SELECT id,label,zone,vpn_host FROM nodes WHERE zone != ?", params:[""]}')" 2>/dev/null || echo '{}')
D1_OK=$(echo "$D1_RESP" | jq -r '.success // false' 2>/dev/null || echo false)
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
REMOTE_PROJ="/opt/cf-vpn"
REMOTE_STAGE="/tmp/cfvpn-stage"

log "rsyncing repo → $TARGET_HOST:$REMOTE_PROJ"
rsync -az --delete \
  --exclude='.git' --exclude='bin/' --exclude='*.log' \
  -e "$_rsync_e" \
  "$PROJECT_ROOT/" "$TARGET_HOST:$REMOTE_PROJ/"

log "rsyncing stage → $TARGET_HOST:$REMOTE_STAGE"
rsync -az \
  -e "$_rsync_e" \
  "$STAGE_DIR/" "$TARGET_HOST:$REMOTE_STAGE/"

# ----- 8. install staged binaries on target ----------------------------------
log "installing staged binaries on $TARGET_HOST"
ssh_run bash <<'REMOTE'
set -euo pipefail
S="/tmp/cfvpn-stage"
echo "[install-node-CN] installing cloudflared from .deb"
# dpkg -i can fail if cloudflared declares deps not yet installed; apt -f
# resolves them from the local apt cache (Aliyun/Tencent mirror).
if ! dpkg -i "$S/tmp/cloudflared.deb"; then
  echo "[install-node-CN] resolving missing deps with apt-get install -f"
  apt-get install -y -f
fi
for b in xray hysteria lego cfvpnctl cfvpn-agent jq curl openssl; do
  install -m 755 "$S/bin/$b" "/usr/local/bin/$b"
done
install -d /usr/local/share/xray /usr/local/etc/xray /var/log/xray
install -m 644 "$S/share/xray/geoip.dat"   /usr/local/share/xray/geoip.dat
install -m 644 "$S/share/xray/geosite.dat" /usr/local/share/xray/geosite.dat
echo "[install-node-CN] /usr/local/bin/ after install:"
for b in xray cloudflared lego hysteria cfvpnctl cfvpn-agent jq curl openssl; do
  ver=$(/usr/local/bin/$b --version 2>&1 | head -1 \
     || /usr/local/bin/$b version 2>&1 | head -1 \
     || echo '?')
  printf "  %-14s %s\n" "$b" "$ver"
done
REMOTE

# ----- 9. write /etc/cfvpn/cfvpn.env on target -------------------------------
# Includes LEGO_DNS_RESOLVERS + LEGO_PROPAGATION_WAIT to make DNS-01 fast
# behind GFW (Cloudflare auth DNS may be UDP-blocked → fall back to recursive).
log "writing /etc/cfvpn/cfvpn.env on $TARGET_HOST"
ssh_run bash -s <<REMOTE
set -euo pipefail
install -d -m 700 /etc/cfvpn
tmp_env="\$(mktemp)"
{
  printf 'CF_API_TOKEN=%s\n'         "${CF_API_TOKEN}"
  printf 'CF_ACCOUNT_ID=%s\n'        "${CF_ACCOUNT_ID}"
  printf 'NODE_ID=%s\n'              "${NODE_ID}"
  printf 'USER1_NAME=%s\n'           "${USER1_NAME}"
  printf 'MODE=%s\n'                 "${MODE}"
  printf 'AGENT_SHARED_SECRET=%s\n'  "${AGENT_SHARED_SECRET}"
  printf 'LEGO_DNS_RESOLVERS=%s\n'   "1.1.1.1:53,8.8.8.8:53"
  printf 'LEGO_DISABLE_CP=%s\n'      "1"
  [ -n "${DOMAIN}" ] && printf 'DOMAIN=%s\n' "${DOMAIN}" || true
} >"\$tmp_env"
install -m 600 "\$tmp_env" /etc/cfvpn/cfvpn.env
rm -f "\$tmp_env"
echo "[install-node-CN] /etc/cfvpn/cfvpn.env written"
REMOTE

# ----- 10. firewall hygiene on target ----------------------------------------
ssh_run bash <<'REMOTE'
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q 'Status: active'; then
  echo "[install-node-CN] ufw active — ensuring SSH stays open"
  ufw allow OpenSSH || ufw allow 22/tcp \
    || echo "[install-node-CN] WARN: could not whitelist SSH; verify manually"
fi
REMOTE

# ----- 11. run cfvpnctl install on target ------------------------------------
log "running cfvpnctl install on $TARGET_HOST (mode=$MODE)"
TUNNELS_BEFORE="$(list_admin_tunnels_local | sort)"
INSTALL_PHASE_STARTED=1
ssh_run cfvpnctl install
INSTALL_PHASE_DONE=1

log "installing healthcheck timer"
ssh_run cfvpnctl healthcheck install

# ----- 12. verify systemd units ----------------------------------------------
log "verifying systemd units on $TARGET_HOST"
ssh_run bash <<'REMOTE'
sleep 2
for u in cfvpn-xray cfvpn-hysteria cfvpn-cloudflared cfvpn-agent; do
  state=$(systemctl is-active "$u" 2>/dev/null || echo unknown)
  if [ "$state" = active ]; then
    echo "[install-node-CN]   $u: active"
  else
    echo "[install-node-CN] WARN:   $u: $state — check: journalctl -u $u -n 80"
  fi
done
REMOTE

ssh_run cfvpnctl healthcheck run \
  || warn "healthcheck reported failure (tunnel may still be registering — re-run in 60s)"

# ----- 13. D1 sync: upsert node + user + user_nodes --------------------------
log "syncing $DB_NODE_ID + user $USER1_NAME to D1"

# Read runtime values written by cfvpnctl install
REMOTE_ENV=$(ssh_run bash -c '. /etc/cfvpn/cfvpn.env
printf "DOMAIN=%s\n"              "$DOMAIN"
printf "HY2_HOST=%s\n"            "$HY2_HOST"
printf "HY2_PORT=%s\n"            "$HY2_PORT"
printf "HY2_OBFS_PW=%s\n"         "$HY2_OBFS_PW"
printf "HY2_PASS_USER1=%s\n"      "$HY2_PASS_USER1"
printf "UUID_USER1=%s\n"          "$UUID_USER1"
printf "PUBLIC_IP=%s\n"           "$PUBLIC_IP"
printf "ADMIN_HOST=%s\n"          "$ADMIN_HOST"
printf "REALITY_PUBLIC_KEY=%s\n"  "$REALITY_PUBLIC_KEY"
printf "REALITY_SHORT_ID=%s\n"    "$REALITY_SHORT_ID"
printf "REALITY_SNI=%s\n"         "$REALITY_SNI"
printf "REALITY_DEST=%s\n"        "$REALITY_DEST"
')
while IFS='=' read -r key val; do
  [ -z "$key" ] && continue
  case "$key" in
    DOMAIN|HY2_HOST|HY2_PORT|HY2_OBFS_PW|HY2_PASS_USER1|UUID_USER1| \
    PUBLIC_IP|ADMIN_HOST|REALITY_PUBLIC_KEY|REALITY_SHORT_ID|REALITY_SNI|REALITY_DEST)
      export "$key=$val" ;;
  esac
done <<< "$REMOTE_ENV"

# Validate required values populated (would crash jq --argjson if empty)
[ -n "${HY2_PORT:-}" ] || die "HY2_PORT not populated by cfvpnctl install"
[ -n "${UUID_USER1:-}" ] || die "UUID_USER1 not populated by cfvpnctl install"

NOW_MS=$(date +%s%3N)
ZONE="$(echo "$DOMAIN" | rev | cut -d'.' -f1,2 | rev)"

# 13a. Upsert node
NODE_RESP=$(d1_query "$(jq -n \
  --arg id    "$DB_NODE_ID" \
  --arg label "$NODE_LABEL" \
  --arg ah    "$ADMIN_HOST" \
  --arg vh    "$DOMAIN" \
  --arg hh    "$HY2_HOST" \
  --argjson hp "$HY2_PORT" \
  --arg how   "$HY2_OBFS_PW" \
  --arg ip    "$PUBLIC_IP" \
  --arg zone  "$ZONE" \
  --arg mode  "$MODE" \
  --arg rpk   "$REALITY_PUBLIC_KEY" \
  --arg rsid  "$REALITY_SHORT_ID" \
  --arg rsni  "$REALITY_SNI" \
  --arg rdest "$REALITY_DEST" \
  --argjson ts "$NOW_MS" \
  --arg sec   "$AGENT_SHARED_SECRET" \
  '{
    sql: "INSERT OR REPLACE INTO nodes (id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,reality_pubkey,reality_sid,reality_sni,reality_dest,last_seen_at,latency_ms,created_at,agent_secret) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,null,null,?,?)",
    params: [$id,$label,$ah,$vh,$hh,$hp,$how,$ip,$zone,$mode,"active",$rpk,$rsid,$rsni,$rdest,$ts,$sec]
  }')")
NODE_OK=$(echo "$NODE_RESP" | jq -r '.success // false')
NODE_CHANGES=$(echo "$NODE_RESP" | jq -r '.result[0].meta.changes // 0')
if [ "$NODE_OK" = "true" ] && [ "$NODE_CHANGES" -ge 1 ]; then
  log "node $DB_NODE_ID upserted in D1 (label=$NODE_LABEL, zone=$ZONE)"
else
  warn "node D1 upsert failed: $(echo "$NODE_RESP" | jq -r '.errors[0].message // "unknown"')"
fi

# 13b. Ensure user exists
USER_RESP=$(d1_query "$(jq -n --arg uid "$USER1_NAME" \
  '{sql:"SELECT id FROM users WHERE id=?", params:[$uid]}')")
USER_EXISTS=$(echo "$USER_RESP" | jq -r '.result[0].results | length')
if [ "$USER_EXISTS" -lt 1 ]; then
  SUB_TOKEN="$(openssl rand -hex 16)"
  CRE_RESP=$(d1_query "$(jq -n \
    --arg uid "$USER1_NAME" \
    --arg tok "$SUB_TOKEN" \
    --argjson ts "$NOW_MS" \
    '{sql:"INSERT OR IGNORE INTO users (id,name,sub_token,created_at) VALUES (?,?,?,?)", params:[$uid,$uid,$tok,$ts]}')")
  CRE_OK=$(echo "$CRE_RESP" | jq -r '.success // false')
  if [ "$CRE_OK" = "true" ]; then
    log "user $USER1_NAME created in D1 (sub_token=$SUB_TOKEN)"
  else
    warn "user create failed: $(echo "$CRE_RESP" | jq -r '.errors[0].message // "unknown"')"
  fi
else
  log "user $USER1_NAME already in D1"
fi

# 13c. Upsert user_nodes
UN_RESP=$(d1_query "$(jq -n \
  --arg uid   "$USER1_NAME" \
  --arg nid   "$DB_NODE_ID" \
  --arg uuid  "$UUID_USER1" \
  --arg hy2pw "$HY2_PASS_USER1" \
  --argjson ts "$NOW_MS" \
  '{sql:"INSERT OR REPLACE INTO user_nodes (user_id,node_id,vless_uuid,hy2_pw,created_at) VALUES (?,?,?,?,?)", params:[$uid,$nid,$uuid,$hy2pw,$ts]}')")
UN_OK=$(echo "$UN_RESP" | jq -r '.success // false')
if [ "$UN_OK" = "true" ]; then
  log "user_nodes $USER1_NAME → $DB_NODE_ID upserted (uuid=$UUID_USER1)"
else
  warn "user_nodes upsert failed: $(echo "$UN_RESP" | jq -r '.errors[0].message // "unknown"')"
fi

# 13d. Agent sync via admin tunnel
# Agent /admin/v1/sync expects objects {name,vless_uuid,hy2_pw}, not bare name
# strings — the old string-array form fails with "cannot unmarshal string into
# syncUser" and silently leaves the user inactive. Mirror install-node.sh.
log "calling agent sync via $ADMIN_HOST"
SYNC_BODY=$(jq -n --arg n "$USER1_NAME" --arg u "$UUID_USER1" --arg p "$HY2_PASS_USER1" \
  '{users:[{name:$n,vless_uuid:$u,hy2_pw:$p}]}')
SYNC_RESP=$(curl -sS --max-time 30 \
  -X POST "https://$ADMIN_HOST/admin/v1/sync" \
  -H "Authorization: Bearer $AGENT_SHARED_SECRET" \
  -H "Content-Type: application/json" \
  --data "$SYNC_BODY" 2>&1) || SYNC_RESP=""
SYNC_OK=$(echo "$SYNC_RESP" | jq -r '.ok // false' 2>/dev/null || echo false)
if [ "$SYNC_OK" = "true" ]; then
  log "agent sync OK — $(echo "$SYNC_RESP" | jq -r '.users') user(s) active on node"
else
  warn "agent sync non-OK: $SYNC_RESP"
fi

# ----- 14. cleanup remote stage dir -------------------------------------------
log "removing remote stage dir on $TARGET_HOST"
ssh_run rm -rf "$REMOTE_STAGE"
# Local STAGE_DIR removed by single EXIT trap (exit_handler → cleanup_local_stage)

log "node ready: $DB_NODE_ID ($NODE_LABEL)"
log "next: ssh${SSH_KEY:+ -i $SSH_KEY} $TARGET_HOST cfvpnctl status"
log "next: ssh${SSH_KEY:+ -i $SSH_KEY} $TARGET_HOST cfvpnctl gen-sub $USER1_NAME"
