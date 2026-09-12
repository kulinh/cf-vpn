#!/usr/bin/env bash
# fleet-backup.sh — snapshot every node's /etc/cfvpn, the D1 tables and the
# served subscriptions into one timestamped directory before touching the fleet.
#
#   bash scripts/fleet-backup.sh [--out DIR]     # default /root/cfvpn-backups
#
# Prints the backup directory as the last line. Exit 1 if any node could not
# be pulled (the rest of the backup is still written).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_ROOT=/root/cfvpn-backups
if [ "${1:-}" = "--out" ]; then
  OUT_ROOT="${2:?--out needs a directory}"
fi
HOSTS_FILE="${CFVPN_FLEET_HOSTS:-/etc/cfvpn/fleet-hosts}"
SSH_KEY="${CFVPN_SSH_KEY:-/root/rwl01.key}"
SSH_OPTS=(-n -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)
LOCAL_TARGET="${CFVPN_LOCAL_TARGET:-root@100.78.174.15}"   # this box: read /etc directly

[ -r "$HOSTS_FILE" ] || { echo "cannot read hosts file: $HOSTS_FILE" >&2; exit 2; }
[ -r /etc/cfvpn/cfvpn.env ] || { echo "cannot read /etc/cfvpn/cfvpn.env (need CF credentials)" >&2; exit 2; }
set -a; . /etc/cfvpn/cfvpn.env; set +a
# shellcheck disable=SC2034  # read by d1_query()
D1_DB_ID="${CFVPN_D1_DATABASE_ID:-0649f07f-e2c0-47f3-b84a-273f7f67332e}"
# shellcheck source=lib/cfvpn-d1.sh
. "$ROOT/scripts/lib/cfvpn-d1.sh"

TS="$(date -u +%Y%m%dT%H%M%SZ)"
DIR="$OUT_ROOT/$TS"
mkdir -p "$OUT_ROOT"
chmod 700 "$OUT_ROOT"
mkdir -p "$DIR/nodes" "$DIR/d1" "$DIR/sub"
chmod 700 "$DIR"

fail=0
while read -r node target _; do
  case "$node" in ''|'#'*) continue ;; esac
  mkdir -p "$DIR/nodes/$node"
  if [ "$target" = "$LOCAL_TARGET" ]; then
    # Guarded exactly like the remote branch below: under `set -e` an unguarded
    # tar failure here aborted the whole run, so one bad node also cost the
    # remaining nodes, the D1 dump and the subscription export — the opposite of
    # what the header promises.
    if ! tar -C /etc -czf "$DIR/nodes/$node/etc-cfvpn.tgz" cfvpn; then
      echo "FAIL $node: could not tar /etc/cfvpn on this box" >&2
      fail=1
      continue
    fi
    { ufw status numbered; systemctl list-units 'cfvpn-*' --no-pager --all; } \
      > "$DIR/nodes/$node/system.txt" 2>&1 || true
  else
    if ! ssh "${SSH_OPTS[@]}" "$target" 'tar -C /etc -czf - cfvpn' > "$DIR/nodes/$node/etc-cfvpn.tgz"; then
      echo "FAIL $node: could not pull /etc/cfvpn from $target" >&2
      fail=1
      continue
    fi
    ssh "${SSH_OPTS[@]}" "$target" 'ufw status numbered; systemctl list-units "cfvpn-*" --no-pager --all' \
      > "$DIR/nodes/$node/system.txt" 2>&1 || true
  fi
  echo "ok $node"
done < "$HOSTS_FILE"

for t in nodes users user_nodes; do
  d1_query "{\"sql\":\"SELECT * FROM $t\"}" > "$DIR/d1/$t.json"
  jq -e '.success' "$DIR/d1/$t.json" >/dev/null || { echo "FAIL d1 $t" >&2; fail=1; }
done

TOKEN="$(jq -r '.result[0].results[0].sub_token // empty' "$DIR/d1/users.json")"
if [ -n "$TOKEN" ]; then
  BASE="https://cp.rwl265.com/sub/$TOKEN"
  curl -fsS "$BASE" > "$DIR/sub/base64.txt"
  base64 -d "$DIR/sub/base64.txt" > "$DIR/sub/decoded.txt"
  curl -fsS "$BASE?format=clash" > "$DIR/sub/clash.yaml"
  curl -fsS "$BASE?target=shadowrocket" > "$DIR/sub/shadowrocket.txt" || true
else
  echo "FAIL: no sub_token in D1 users; subscription not saved" >&2
  fail=1
fi
chmod -R go-rwx "$DIR"
echo "$DIR"
exit $fail
