#!/usr/bin/env bash
# check-fleet-drift.sh — report nodes whose served credentials no longer match
# the ones D1 hands to clients.
#
#   bash scripts/check-fleet-drift.sh [--hosts FILE] [--quiet]
#
# Exit status: 0 in sync, 1 drift found, 2 could not complete the check.
#
# Why this exists: the panel builds every subscription link from D1
# `user_nodes`, while the node serves whatever is in its xray/hysteria config.
# Those are written by different paths at different times, so they drift. A
# wrong Hysteria2 password produces no auth error — the QUIC server simply
# never answers — so the user sees a *timeout* and the node looks healthy from
# every server-side check. Run this after anything that touches users, and
# before blaming the network.
#
# The hosts file maps a D1 node id to an SSH target, one per line, '#' comments
# allowed. Default: /etc/cfvpn/fleet-hosts
#
#   JPY-01  root@100.84.34.23
#   SIN-01  root@100.103.47.77
#
# Nodes in D1 with no entry are skipped and named in the summary: silently
# checking a subset would make a clean run meaningless.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LIB="$ROOT/scripts/lib"
HOSTS_FILE="${CFVPN_FLEET_HOSTS:-/etc/cfvpn/fleet-hosts}"
SSH_KEY="${CFVPN_SSH_KEY:-/root/rwl01.key}"
QUIET=0

while [ $# -gt 0 ]; do
  case "$1" in
    --hosts) HOSTS_FILE="${2:-}"; shift 2 ;;
    --quiet) QUIET=1; shift ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

# shellcheck source=lib/cfvpn-common.sh
. "$LIB/cfvpn-common.sh"
# shellcheck source=lib/cfvpn-drift.sh
. "$LIB/cfvpn-drift.sh"

say() { [ "$QUIET" -eq 1 ] || printf '%s\n' "$*"; }

[ -r "$HOSTS_FILE" ] || { echo "cannot read hosts file: $HOSTS_FILE" >&2; exit 2; }
[ -r /etc/cfvpn/cfvpn.env ] || { echo "cannot read /etc/cfvpn/cfvpn.env (need CF credentials)" >&2; exit 2; }

# CF_API_TOKEN / CF_ACCOUNT_ID / D1_DB_ID are what cfvpn-d1.sh needs.
set -a; . /etc/cfvpn/cfvpn.env; set +a
# shellcheck disable=SC2034  # read by d1_query()
D1_DB_ID="${CFVPN_D1_DATABASE_ID:-0649f07f-e2c0-47f3-b84a-273f7f67332e}"
# shellcheck source=lib/cfvpn-d1.sh
. "$LIB/cfvpn-d1.sh"

WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT INT TERM

# ----- 1. what D1 promises ----------------------------------------------------
RESP="$(d1_query "$(jq -n '{sql:"SELECT node_id,user_id,vless_uuid,hy2_pw FROM user_nodes ORDER BY node_id,user_id", params:[]}')")"
if [ "$(printf '%s' "$RESP" | jq -r '.success // false')" != "true" ]; then
  echo "D1 query failed: $(printf '%s' "$RESP" | jq -r '.errors[0].message // "unknown"')" >&2
  exit 2
fi
printf '%s' "$RESP" | jq -r '.result[0].results[] | [.node_id, .user_id, (.vless_uuid // "-"), (.hy2_pw // "-")] | @tsv' > "$WORK/d1.tsv"
say "D1: $(wc -l < "$WORK/d1.tsv") user/node binding(s)"

# ----- 2. what each node actually serves --------------------------------------
# Emitted by the node itself so one ssh round-trip covers both configs.
REMOTE='
xray=/etc/cfvpn/xray/config.json
hy=/etc/cfvpn/hysteria/config.yaml
jq -r ".inbounds[0].settings.clients[]? | [(.email|sub(\"@vpn$\";\"\")), .id] | @tsv" "$xray" 2>/dev/null | sort > /tmp/.d_x
sed -n "/userpass:/,/^[^ ]/p" "$hy" 2>/dev/null \
  | sed -nE "s/^[[:space:]]+\"?([A-Za-z0-9_-]+)\"?:[[:space:]]*\"?([^\"]+)\"?[[:space:]]*$/\1\t\2/p" | sort > /tmp/.d_h
join -t "$(printf "\t")" -a1 -e "-" -o 0,1.2,2.2 /tmp/.d_x /tmp/.d_h
rm -f /tmp/.d_x /tmp/.d_h
'

SKIPPED=""; UNREACHABLE=""
: > "$WORK/node.tsv"
while read -r node target; do
  case "$node" in ''|\#*) continue ;; esac
  [ -n "$target" ] || continue
  if ! out="$(ssh -i "$SSH_KEY" -o StrictHostKeyChecking=no -o BatchMode=yes -o ConnectTimeout=15 \
                  "$target" "bash -s" <<< "$REMOTE" 2>/dev/null)"; then
    UNREACHABLE="$UNREACHABLE $node"
    continue
  fi
  [ -n "$out" ] || { UNREACHABLE="$UNREACHABLE $node"; continue; }
  printf '%s\n' "$out" | awk -v n="$node" -F'\t' 'NF>=2 {print n "\t" $1 "\t" $2 "\t" ($3==""?"-":$3)}' >> "$WORK/node.tsv"
done < "$HOSTS_FILE"

# Nodes D1 knows about that the hosts file does not cover.
while read -r node _; do
  grep -q "^$node	" "$WORK/node.tsv" 2>/dev/null || case " $UNREACHABLE " in
    *" $node "*) ;; *) SKIPPED="$SKIPPED $node" ;;
  esac
done < <(cut -f1 "$WORK/d1.tsv" | sort -u)

# Only compare nodes that were actually read, so an unreachable host is
# reported as unreachable instead of as "every user missing".
if [ -s "$WORK/node.tsv" ]; then
  cut -f1 "$WORK/node.tsv" | sort -u > "$WORK/checked"
  awk -F'\t' 'NR==FNR {ok[$1]=1; next} ($1 in ok)' "$WORK/checked" "$WORK/d1.tsv" > "$WORK/d1.checked.tsv"
else
  : > "$WORK/d1.checked.tsv"
fi

# ----- 3. compare -------------------------------------------------------------
DRIFT="$(drift_compare "$WORK/d1.checked.tsv" "$WORK/node.tsv")"; RC=$?

if [ -n "$DRIFT" ]; then
  echo "DRIFT — these nodes serve credentials the panel does not hand out:"
  printf '%s\n' "$DRIFT" | sed 's/^/  /'
  echo
  echo "Fix: re-sync the node with the values from D1 (D1 is what clients already have),"
  echo "then update UUID_USER1 / HY2_PASS_USER1 in /etc/cfvpn/cfvpn.env to match."
fi
[ -n "$UNREACHABLE" ] && { echo "UNREACHABLE (not checked):$UNREACHABLE"; RC=2; }
[ -n "$SKIPPED" ]     && { echo "NOT IN HOSTS FILE (not checked):$SKIPPED"; RC=2; }
[ -z "$DRIFT" ] && [ -z "$UNREACHABLE" ] && [ -z "$SKIPPED" ] && say "in sync: $(wc -l < "$WORK/node.tsv") binding(s) across $(wc -l < "$WORK/checked") node(s)"
exit "$RC"
