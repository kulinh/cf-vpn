#!/usr/bin/env bash
# mirror-agent-secret.sh — copy this node's AGENT_SHARED_SECRET into the
# Worker's D1 row (column nodes.agent_secret). Required after migration 0011
# so the panel sends the correct per-node bearer to /admin/v1/*. Idempotent.
#
# Usage (run on the node, as root):
#   sudo bash mirror-agent-secret.sh
#
# Reads CF_API_TOKEN, CF_ACCOUNT_ID, NODE_ID, AGENT_SHARED_SECRET from
# /etc/cfvpn/cfvpn.env. The token must have D1:Edit on the account that
# hosts the cfvpn_panel_prod database.
set -euo pipefail

ENV_FILE=/etc/cfvpn/cfvpn.env
D1_DB_ID="${CFVPN_D1_DATABASE_ID:-0649f07f-e2c0-47f3-b84a-273f7f67332e}"

[ "${EUID:-$(id -u)}" -eq 0 ] || { echo "run as root" >&2; exit 1; }
[ -r "$ENV_FILE" ] || { echo "$ENV_FILE not readable" >&2; exit 1; }

# Source securely (chmod 600, root-owned).
set -a; source "$ENV_FILE"; set +a

for v in CF_API_TOKEN CF_ACCOUNT_ID NODE_ID AGENT_SHARED_SECRET; do
  [ -n "${!v:-}" ] || { echo "$v missing in $ENV_FILE" >&2; exit 1; }
done

payload=$(jq -n --arg s "$AGENT_SHARED_SECRET" --arg id "$NODE_ID" \
  '{sql: "UPDATE nodes SET agent_secret=? WHERE id=?", params: [$s, $id]}')

# Token via curl --config so it never appears in argv / /proc/<pid>/cmdline.
resp=$(curl -sS --max-time 30 -H "Content-Type: application/json" --data-raw "$payload" --config - <<EOF
header = "Authorization: Bearer ${CF_API_TOKEN}"
url = "https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/d1/database/${D1_DB_ID}/query"
EOF
)

ok=$(printf '%s' "$resp" | jq -r '.success // false')
changes=$(printf '%s' "$resp" | jq -r '.result[0].meta.changes // 0')

if [ "$ok" != true ]; then
  echo "D1 update failed:" >&2
  printf '%s' "$resp" | jq -r '.errors[]?.message // "unknown error"' >&2
  exit 1
fi

if [ "$changes" -lt 1 ]; then
  echo "warning: 0 rows updated — does NODE_ID=$NODE_ID exist in D1?" >&2
  exit 2
fi

echo "ok: nodes.agent_secret mirrored for $NODE_ID"
