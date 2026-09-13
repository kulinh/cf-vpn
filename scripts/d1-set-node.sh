#!/usr/bin/env bash
# d1-set-node.sh — push one node's live env values into the panel's D1 row.
#
#   bash scripts/d1-set-node.sh <NODE_ID> hy2-off   # NULL hy2_host/hy2_port/hy2_obfs_pw
#   bash scripts/d1-set-node.sh <NODE_ID> hy2-on    # copy HY2_HOST/HY2_PORT/HY2_OBFS_PW from the node
#   bash scripts/d1-set-node.sh <NODE_ID> reality   # copy REALITY_* + PUBLIC_IP from the node
#   bash scripts/d1-set-node.sh <NODE_ID> xhttp-on|xhttp-off   # nodes.xhttp_enabled
#   bash scripts/d1-set-node.sh <NODE_ID> xhttp-direct   # copy XHTTP_DIRECT_HOST/PATH from the node (empty = NULL)
#   bash scripts/d1-set-node.sh <NODE_ID> xhttp-h3       # copy XHTTP_H3_HOST/PATH from the node (empty = NULL)
#   bash scripts/d1-set-node.sh <NODE_ID> naive          # copy NAIVE_HOST/USER/PASS from the node (empty = NULL)
#   bash scripts/d1-set-node.sh <NODE_ID> ipv6           # copy PUBLIC_IPV6 from the node (empty = NULL)
#
# Why: the Worker only persists reality_*/hy2_* when the panel itself calls
# the agent (node status / user sync), both behind Cloudflare Access. After a
# `cfvpnctl rotate-reality` or `cfvpnctl hy2 disable` run over SSH, this is the
# direct way to make the subscription the panel serves match the node.
# Verify afterwards with scripts/check-fleet-drift.sh.
#
# Tests (scripts/tests/run-tests.sh) run this with CFVPN_ENV_FILE and
# CFVPN_FLEET_HOSTS pointed at fixtures and fake `ssh`/`curl` first on PATH, so
# the real d1_query builds the request and nothing leaves the box.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NODE="${1:?usage: d1-set-node.sh <NODE_ID> hy2-off|hy2-on|reality}"
ACTION="${2:?usage: d1-set-node.sh <NODE_ID> hy2-off|hy2-on|reality}"
HOSTS_FILE="${CFVPN_FLEET_HOSTS:-/etc/cfvpn/fleet-hosts}"
SSH_KEY="${CFVPN_SSH_KEY:-/root/rwl01.key}"
LOCAL_TARGET="${CFVPN_LOCAL_TARGET:-root@100.78.174.15}"

[ -r "$HOSTS_FILE" ] || { echo "cannot read hosts file: $HOSTS_FILE" >&2; exit 2; }
# CFVPN_ENV_FILE: test seam only; in production this is /etc/cfvpn/cfvpn.env.
ENV_FILE="${CFVPN_ENV_FILE:-/etc/cfvpn/cfvpn.env}"
[ -r "$ENV_FILE" ] || { echo "cannot read $ENV_FILE (need CF credentials)" >&2; exit 2; }
set -a
# shellcheck source=/dev/null
. "$ENV_FILE"
set +a
# shellcheck disable=SC2034  # read by d1_query()
D1_DB_ID="${CFVPN_D1_DATABASE_ID:-0649f07f-e2c0-47f3-b84a-273f7f67332e}"
# shellcheck source=lib/cfvpn-d1.sh
. "$ROOT/scripts/lib/cfvpn-d1.sh"

TARGET="$(awk -v n="$NODE" '$1==n{print $2}' "$HOSTS_FILE")"
[ -n "$TARGET" ] || { echo "no ssh target for $NODE in $HOSTS_FILE" >&2; exit 2; }

node_env() {
  if [ "$TARGET" = "$LOCAL_TARGET" ]; then
    cat "$ENV_FILE"
  else
    ssh -n -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=no \
      -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR "$TARGET" cat /etc/cfvpn/cfvpn.env
  fi
}

# Only the actions that copy values from the node read its env (hy2-off and
# xhttp-on|off must work without reaching the node at all).
envtxt=""
case "$ACTION" in
  hy2-on|reality|xhttp-direct|xhttp-h3|naive|ipv6) envtxt="$(node_env)" ;;
esac
g() { printf '%s\n' "$envtxt" | awk -F= -v k="$1" '$1==k{print substr($0, length(k)+2); exit}'; }

# ipv6_ok <value> — a plain IPv6 address, as the subscription puts
# it inside [...]. No zone id (%eth0), no brackets, no IPv4-mapped (::ffff:a.b.c.d).
ipv6_ok() {
  python3 - "$1" <<'PY'
import ipaddress, sys
v = sys.argv[1]
if "%" in v or "[" in v or "]" in v:
    sys.exit(1)
try:
    a = ipaddress.IPv6Address(v)
except ValueError:
    sys.exit(1)
sys.exit(1 if a.ipv4_mapped is not None else 0)
PY
}

case "$ACTION" in
  hy2-off)
    payload="$(jq -cn --arg id "$NODE" \
      '{sql:"UPDATE nodes SET hy2_host=NULL, hy2_port=NULL, hy2_obfs_pw=NULL WHERE id=?", params:[$id]}')"
    ;;
  hy2-on)
    host="$(g HY2_HOST)"; port="$(g HY2_PORT)"; obfs="$(g HY2_OBFS_PW)"
    [ -n "$host" ] && [ -n "$port" ] && [ -n "$obfs" ] \
      || { echo "$NODE: HY2_* incomplete in cfvpn.env; refusing to write" >&2; exit 1; }
    payload="$(jq -cn --arg id "$NODE" --arg h "$host" --argjson p "$port" --arg o "$obfs" \
      '{sql:"UPDATE nodes SET hy2_host=?, hy2_port=?, hy2_obfs_pw=? WHERE id=?", params:[$h,$p,$o,$id]}')"
    ;;
  reality)
    pk="$(g REALITY_PUBLIC_KEY)"; sid="$(g REALITY_SHORT_ID)"; sni="$(g REALITY_SNI)"; dest="$(g REALITY_DEST)"; ip="$(g PUBLIC_IP)"
    [ -n "$pk" ] && [ -n "$sid" ] && [ -n "$sni" ] && [ -n "$dest" ] && [ -n "$ip" ] \
      || { echo "$NODE: REALITY_* / PUBLIC_IP incomplete in cfvpn.env; refusing to write" >&2; exit 1; }
    payload="$(jq -cn --arg id "$NODE" --arg pk "$pk" --arg sid "$sid" --arg sni "$sni" --arg dest "$dest" --arg ip "$ip" \
      '{sql:"UPDATE nodes SET reality_pubkey=?, reality_sid=?, reality_sni=?, reality_dest=?, public_ip=? WHERE id=?", params:[$pk,$sid,$sni,$dest,$ip,$id]}')"
    ;;
  xhttp-direct)
    dh="$(g XHTTP_DIRECT_HOST)"; dp="$(g XHTTP_DIRECT_PATH)"
    payload="$(jq -cn --arg id "$NODE" --arg h "$dh" --arg p "$dp" \
      '{sql:"UPDATE nodes SET xhttp_direct_host=NULLIF(?,\"\"), xhttp_direct_path=NULLIF(?,\"\") WHERE id=?", params:[$h,$p,$id]}')"
    ;;
  xhttp-h3)
    hh="$(g XHTTP_H3_HOST)"; hp="$(g XHTTP_H3_PATH)"
    payload="$(jq -cn --arg id "$NODE" --arg h "$hh" --arg p "$hp" \
      '{sql:"UPDATE nodes SET xhttp_h3_host=NULLIF(?,\"\"), xhttp_h3_path=NULLIF(?,\"\") WHERE id=?", params:[$h,$p,$id]}')"
    ;;
  naive)
    nh="$(g NAIVE_HOST)"; nu="$(g NAIVE_USER)"; np="$(g NAIVE_PASS)"
    payload="$(jq -cn --arg id "$NODE" --arg h "$nh" --arg u "$nu" --arg p "$np" \
      '{sql:"UPDATE nodes SET naive_host=NULLIF(?,\"\"), naive_user=NULLIF(?,\"\"), naive_pass=NULLIF(?,\"\") WHERE id=?", params:[$h,$u,$p,$id]}')"
    ;;
  ipv6)
    v6="$(g PUBLIC_IPV6)"
    if [ -n "$v6" ] && ! ipv6_ok "$v6"; then
      echo "$NODE: PUBLIC_IPV6=$v6 is not a plain IPv6 address; refusing to write" >&2; exit 1
    fi
    payload="$(jq -cn --arg id "$NODE" --arg v "$v6" \
      '{sql:"UPDATE nodes SET public_ipv6=NULLIF(?,\"\") WHERE id=?", params:[$v,$id]}')"
    ;;
  xhttp-on|xhttp-off)
    v=0; [ "$ACTION" = "xhttp-on" ] && v=1
    payload="$(jq -cn --arg id "$NODE" --argjson v "$v" '{sql:"UPDATE nodes SET xhttp_enabled=? WHERE id=?", params:[$v,$id]}')"
    ;;
  *) echo "unknown action: $ACTION (hy2-off|hy2-on|reality|xhttp-on|xhttp-off|xhttp-direct|xhttp-h3|naive|ipv6)" >&2; exit 2 ;;
esac

out="$(d1_query "$payload")"
jq -e '.success' <<<"$out" >/dev/null || { echo "D1 update failed: $out" >&2; exit 1; }
changes="$(jq -r '.result[0].meta.changes // 0' <<<"$out")"
[ "$changes" = "1" ] || { echo "D1 updated $changes rows for $NODE (expected 1)" >&2; exit 1; }
echo "D1 updated: $NODE $ACTION"
