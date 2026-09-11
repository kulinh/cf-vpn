#!/usr/bin/env bash
# d1-set-node.sh — push one node's live env values into the panel's D1 row.
#
#   bash scripts/d1-set-node.sh <NODE_ID> hy2-off   # NULL hy2_host/hy2_port/hy2_obfs_pw
#   bash scripts/d1-set-node.sh <NODE_ID> hy2-on    # copy HY2_HOST/HY2_PORT/HY2_OBFS_PW from the node
#   bash scripts/d1-set-node.sh <NODE_ID> reality   # copy REALITY_* + PUBLIC_IP from the node
#   bash scripts/d1-set-node.sh <NODE_ID> xhttp-on|xhttp-off   # nodes.xhttp_enabled
#   bash scripts/d1-set-node.sh <NODE_ID> xhttp-direct   # copy XHTTP_DIRECT_HOST/PATH from the node (empty = NULL)
#
# Why: the Worker only persists reality_*/hy2_* when the panel itself calls
# the agent (node status / user sync), both behind Cloudflare Access. After a
# `cfvpnctl rotate-reality` or `cfvpnctl hy2 disable` run over SSH, this is the
# direct way to make the subscription the panel serves match the node.
# Verify afterwards with scripts/check-fleet-drift.sh.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NODE="${1:?usage: d1-set-node.sh <NODE_ID> hy2-off|hy2-on|reality}"
ACTION="${2:?usage: d1-set-node.sh <NODE_ID> hy2-off|hy2-on|reality}"
HOSTS_FILE="${CFVPN_FLEET_HOSTS:-/etc/cfvpn/fleet-hosts}"
SSH_KEY="${CFVPN_SSH_KEY:-/root/rwl01.key}"
LOCAL_TARGET="${CFVPN_LOCAL_TARGET:-root@100.78.174.15}"

[ -r "$HOSTS_FILE" ] || { echo "cannot read hosts file: $HOSTS_FILE" >&2; exit 2; }
[ -r /etc/cfvpn/cfvpn.env ] || { echo "cannot read /etc/cfvpn/cfvpn.env (need CF credentials)" >&2; exit 2; }
set -a; . /etc/cfvpn/cfvpn.env; set +a
# shellcheck disable=SC2034  # read by d1_query()
D1_DB_ID="${CFVPN_D1_DATABASE_ID:-0649f07f-e2c0-47f3-b84a-273f7f67332e}"
# shellcheck source=lib/cfvpn-d1.sh
. "$ROOT/scripts/lib/cfvpn-d1.sh"

TARGET="$(awk -v n="$NODE" '$1==n{print $2}' "$HOSTS_FILE")"
[ -n "$TARGET" ] || { echo "no ssh target for $NODE in $HOSTS_FILE" >&2; exit 2; }

node_env() {
  if [ "$TARGET" = "$LOCAL_TARGET" ]; then
    cat /etc/cfvpn/cfvpn.env
  else
    ssh -n -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=no \
      -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR "$TARGET" cat /etc/cfvpn/cfvpn.env
  fi
}

case "$ACTION" in
  hy2-off)
    payload="$(jq -cn --arg id "$NODE" \
      '{sql:"UPDATE nodes SET hy2_host=NULL, hy2_port=NULL, hy2_obfs_pw=NULL WHERE id=?", params:[$id]}')"
    ;;
  hy2-on)
    envtxt="$(node_env)"
    g() { printf '%s\n' "$envtxt" | awk -F= -v k="$1" '$1==k{print substr($0, length(k)+2); exit}'; }
    host="$(g HY2_HOST)"; port="$(g HY2_PORT)"; obfs="$(g HY2_OBFS_PW)"
    [ -n "$host" ] && [ -n "$port" ] && [ -n "$obfs" ] \
      || { echo "$NODE: HY2_* incomplete in cfvpn.env; refusing to write" >&2; exit 1; }
    payload="$(jq -cn --arg id "$NODE" --arg h "$host" --argjson p "$port" --arg o "$obfs" \
      '{sql:"UPDATE nodes SET hy2_host=?, hy2_port=?, hy2_obfs_pw=? WHERE id=?", params:[$h,$p,$o,$id]}')"
    ;;
  reality)
    envtxt="$(node_env)"
    g() { printf '%s\n' "$envtxt" | awk -F= -v k="$1" '$1==k{print substr($0, length(k)+2); exit}'; }
    pk="$(g REALITY_PUBLIC_KEY)"; sid="$(g REALITY_SHORT_ID)"; sni="$(g REALITY_SNI)"; dest="$(g REALITY_DEST)"; ip="$(g PUBLIC_IP)"
    [ -n "$pk" ] && [ -n "$sid" ] && [ -n "$sni" ] && [ -n "$dest" ] && [ -n "$ip" ] \
      || { echo "$NODE: REALITY_* / PUBLIC_IP incomplete in cfvpn.env; refusing to write" >&2; exit 1; }
    payload="$(jq -cn --arg id "$NODE" --arg pk "$pk" --arg sid "$sid" --arg sni "$sni" --arg dest "$dest" --arg ip "$ip" \
      '{sql:"UPDATE nodes SET reality_pubkey=?, reality_sid=?, reality_sni=?, reality_dest=?, public_ip=? WHERE id=?", params:[$pk,$sid,$sni,$dest,$ip,$id]}')"
    ;;
  xhttp-direct)
    envtxt="$(node_env)"
    g() { printf '%s\n' "$envtxt" | awk -F= -v k="$1" '$1==k{print substr($0, length(k)+2); exit}'; }
    dh="$(g XHTTP_DIRECT_HOST)"; dp="$(g XHTTP_DIRECT_PATH)"
    payload="$(jq -cn --arg id "$NODE" --arg h "$dh" --arg p "$dp" \
      '{sql:"UPDATE nodes SET xhttp_direct_host=NULLIF(?,\"\"), xhttp_direct_path=NULLIF(?,\"\") WHERE id=?", params:[$h,$p,$id]}')"
    ;;
  xhttp-on|xhttp-off)
    v=0; [ "$ACTION" = "xhttp-on" ] && v=1
    payload="$(jq -cn --arg id "$NODE" --argjson v "$v" '{sql:"UPDATE nodes SET xhttp_enabled=? WHERE id=?", params:[$v,$id]}')"
    ;;
  *) echo "unknown action: $ACTION (hy2-off|hy2-on|reality|xhttp-on|xhttp-off|xhttp-direct)" >&2; exit 2 ;;
esac

out="$(d1_query "$payload")"
jq -e '.success' <<<"$out" >/dev/null || { echo "D1 update failed: $out" >&2; exit 1; }
changes="$(jq -r '.result[0].meta.changes // 0' <<<"$out")"
[ "$changes" = "1" ] || { echo "D1 updated $changes rows for $NODE (expected 1)" >&2; exit 1; }
echo "D1 updated: $NODE $ACTION"
