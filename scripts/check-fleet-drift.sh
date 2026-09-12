#!/usr/bin/env bash
# check-fleet-drift.sh — report nodes whose served credentials no longer match
# the ones D1 hands to clients.
#
#   bash scripts/check-fleet-drift.sh [--hosts FILE] [--quiet]
#
# Exit status: 0 in sync, 1 drift found, 2 could not complete the check. 1 wins
# over 2 when both happen — confirmed drift is not a transient problem.
#
# Two comparisons run: the per-user credentials (vless uuid, hy2 password) and
# the per-node transport flags (HY2_ENABLED / XHTTP_ENABLED / XHTTP_DIRECT_HOST /
# XHTTP_H3_HOST on the node vs nodes.hy2_host / xhttp_enabled /
# xhttp_direct_host / xhttp_h3_host in D1).
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

# Which transports the panel believes each node runs. Kept as the RAW values
# (NULL -> empty) so drift_transport_compare owns the default-on/default-off
# rules and matches commands.Hy2Enabled / commands.XHTTPEnabled exactly.
NRESP="$(d1_query "$(jq -n '{sql:"SELECT id,hy2_host,xhttp_enabled,xhttp_direct_host,xhttp_h3_host FROM nodes ORDER BY id", params:[]}')")"
if [ "$(printf '%s' "$NRESP" | jq -r '.success // false')" != "true" ]; then
  echo "D1 nodes query failed: $(printf '%s' "$NRESP" | jq -r '.errors[0].message // "unknown"')" >&2
  exit 2
fi
printf '%s' "$NRESP" | jq -r '.result[0].results[] | [.id, (.hy2_host // ""), (.xhttp_enabled // ""), (.xhttp_direct_host // ""), (.xhttp_h3_host // "")] | @tsv' > "$WORK/d1.nodes.tsv"

# ----- 2. what each node actually serves --------------------------------------
# Emitted by the node itself so one ssh round-trip covers both configs.
REMOTE='
# One FLAGS line first: which transports this node is configured for. Read with
# split-on-first-= (like internal/state/store.go), never by sourcing cfvpn.env —
# that would execute any $(...) in a value as root on every node we check.
hy2_enabled=""; xhttp_enabled=""; xhttp_direct_host=""; xhttp_h3_host=""
if [ -r /etc/cfvpn/cfvpn.env ]; then
  while IFS="=" read -r k v; do
    case "$k" in
      HY2_ENABLED)       hy2_enabled="$v" ;;
      XHTTP_ENABLED)     xhttp_enabled="$v" ;;
      XHTTP_DIRECT_HOST) xhttp_direct_host="$v" ;;
      XHTTP_H3_HOST)     xhttp_h3_host="$v" ;;
    esac
  done < /etc/cfvpn/cfvpn.env
fi
printf "FLAGS\t%s\t%s\t%s\t%s\n" "$hy2_enabled" "$xhttp_enabled" "$xhttp_direct_host" "$xhttp_h3_host"

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
: > "$WORK/node.flags.tsv"
while read -r node target; do
  case "$node" in ''|\#*) continue ;; esac
  [ -n "$target" ] || continue
  if ! out="$(ssh -i "$SSH_KEY" -o StrictHostKeyChecking=no -o BatchMode=yes -o ConnectTimeout=15 \
                  "$target" "bash -s" <<< "$REMOTE" 2>/dev/null)"; then
    UNREACHABLE="$UNREACHABLE $node"
    continue
  fi
  [ -n "$out" ] || { UNREACHABLE="$UNREACHABLE $node"; continue; }
  # The FLAGS line is the transport row; everything else is a (user, uuid, pw) row.
  printf '%s\n' "$out" | awk -v n="$node" -F'\t' '$1=="FLAGS" {print n "\t" $2 "\t" $3 "\t" $4 "\t" $5}' >> "$WORK/node.flags.tsv"
  printf '%s\n' "$out" | awk -v n="$node" -F'\t' '$1!="FLAGS" && NF>=2 {print n "\t" $1 "\t" $2 "\t" ($3==""?"-":$3)}' >> "$WORK/node.tsv"
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
# The transport rows are compared over the same set of nodes, for the same
# reason: an unreachable host must not look like "every flag wrong".
if [ -s "$WORK/node.flags.tsv" ]; then
  cut -f1 "$WORK/node.flags.tsv" | sort -u > "$WORK/checked.flags"
  awk -F'\t' 'NR==FNR {ok[$1]=1; next} ($1 in ok)' "$WORK/checked.flags" "$WORK/d1.nodes.tsv" > "$WORK/d1.nodes.checked.tsv"
else
  : > "$WORK/d1.nodes.checked.tsv"
fi

# ----- 3. compare -------------------------------------------------------------
DRIFT="$(drift_compare "$WORK/d1.checked.tsv" "$WORK/node.tsv")"; CRC=$?
TDRIFT="$(drift_transport_compare "$WORK/d1.nodes.checked.tsv" "$WORK/node.flags.tsv")"; TRC=$?

# Exit status is a RANKING, not a maximum (see drift_rank_rc): real drift (1)
# outranks "could not check" (2). A run that found drift AND could not reach one
# host must still exit 1 — otherwise the caller reads a confirmed mismatch as a
# transient SSH problem and retries instead of fixing it.
RC=0
RC="$(drift_rank_rc "$RC" "$CRC")"
RC="$(drift_rank_rc "$RC" "$TRC")"

if [ -n "$DRIFT" ]; then
  echo "DRIFT — these nodes serve credentials the panel does not hand out:"
  printf '%s\n' "$DRIFT" | sed 's/^/  /'
  echo
  echo "Fix: re-sync the node with the values from D1 (D1 is what clients already have),"
  echo "then update UUID_USER1 / HY2_PASS_USER1 in /etc/cfvpn/cfvpn.env to match."
fi
if [ -n "$TDRIFT" ]; then
  echo "DRIFT — these nodes run different transports than the panel advertises:"
  printf '%s\n' "$TDRIFT" | sed 's/^/  /'
  echo
  echo "Fix: make D1 match the node with scripts/d1-set-node.sh <NODE> hy2-on|hy2-off|"
  echo "xhttp-on|xhttp-off|xhttp-direct|xhttp-h3 (or flip the node with cfvpnctl hy2/xhttp/xhttp-h3)."
fi
if [ -n "$UNREACHABLE" ]; then echo "UNREACHABLE (not checked):$UNREACHABLE"; RC="$(drift_rank_rc "$RC" 2)"; fi
if [ -n "$SKIPPED" ];     then echo "NOT IN HOSTS FILE (not checked):$SKIPPED"; RC="$(drift_rank_rc "$RC" 2)"; fi
[ -z "$DRIFT" ] && [ -z "$TDRIFT" ] && [ -z "$UNREACHABLE" ] && [ -z "$SKIPPED" ] && say "in sync: $(wc -l < "$WORK/node.tsv") binding(s) across $(wc -l < "$WORK/checked") node(s)"
exit "$RC"
