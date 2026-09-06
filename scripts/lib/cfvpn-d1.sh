#!/usr/bin/env bash
# cfvpn-d1.sh — D1 + agent-sync helpers shared by scripts/install-node.sh and
# scripts/install-node-CN.sh. Source it, do not execute it.
#
# Requires from the caller:
#   - log() / warn() / die()            (fallbacks come from cfvpn-common.sh)
#   - CF_API_TOKEN, CF_ACCOUNT_ID, D1_DB_ID
#   - curl, jq
#
# Provides:
#   d1_query PAYLOAD_JSON               — always prints valid JSON
#   d1_zone_for_domain DOMAIN           — longest zone from D1 that suffixes DOMAIN
#   d1_upsert_node                      — reads the node vars listed below
#   d1_ensure_user                      — USER1_NAME, NOW_MS
#   d1_upsert_user_nodes                — USER1_NAME, DB_NODE_ID, UUID_USER1, HY2_PASS_USER1, NOW_MS
#   agent_sync                          — ADMIN_HOST, AGENT_SHARED_SECRET, USER1_NAME, UUID_USER1, HY2_PASS_USER1

[ -n "${_CFVPN_D1_SH:-}" ] && return 0
_CFVPN_D1_SH=1

# shellcheck source=lib/cfvpn-common.sh
. "$(dirname "${BASH_SOURCE[0]}")/cfvpn-common.sh"

# curl -f throws away the response body, which for D1 carries the actual SQL
# error. --fail-with-body (curl >= 7.76) keeps it; fall back to -f on older
# curl so the helper still refuses to hand HTML/WAF pages to jq.
if curl --fail-with-body --version >/dev/null 2>&1; then
  _CFVPN_CURL_FAIL=(--fail-with-body)
else
  _CFVPN_CURL_FAIL=(-f)
fi

# d1_query <json-payload>
# Always prints a syntactically valid JSON object on stdout and returns 0, so
# every caller's "check .success then warn and continue" path is reachable.
# A transport failure or a non-JSON body (CF 5xx page, WAF challenge) becomes
# {"success":false,"errors":[{"message":"curl failed"}]}.
d1_query() {
  local payload="$1" resp rc=0
  local _d1_tmpf; _d1_tmpf="$(mktemp)"
  # RETURN trap: the payload can carry the agent secret and user credentials;
  # it must not survive a Ctrl-C or a set -e abort between write and rm. The
  # trap clears itself, otherwise it would fire again for every enclosing
  # function that returns afterwards.
  trap 'rm -f "$_d1_tmpf"; trap - RETURN' RETURN
  chmod 600 "$_d1_tmpf"
  printf '%s' "$payload" >"$_d1_tmpf"
  resp="$(curl -sS "${_CFVPN_CURL_FAIL[@]}" --max-time 30 --config - <<EOF
header = "Authorization: Bearer ${CF_API_TOKEN}"
header = "Content-Type: application/json"
url = "https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/d1/database/${D1_DB_ID}/query"
data-binary = "@${_d1_tmpf}"
EOF
  )" || rc=$?
  if [ -n "$resp" ] && printf '%s' "$resp" | jq -e 'type == "object"' >/dev/null 2>&1; then
    printf '%s\n' "$resp"
    return 0
  fi
  warn "D1 API call failed (curl exit ${rc}, $( [ -n "$resp" ] && echo 'non-JSON body' || echo 'empty body' ))"
  printf '%s\n' '{"success":false,"errors":[{"message":"curl failed"}]}'
}

# d1_zone_for_domain <domain>
# The zone of a host is not "the last two labels" — that breaks on any
# multi-label public suffix (vpn.example.co.uk -> co.uk). Ask D1 for the zone
# pool and take the longest zone that is a suffix of DOMAIN. If D1 is
# unreachable, fall back to the old heuristic with a warning.
d1_zone_for_domain() {
  local domain="$1" resp ok zones z best=""
  [ -n "$domain" ] || return 0
  resp="$(d1_query "$(jq -n '{sql:"SELECT name FROM zones", params:[]}')")"
  ok="$(printf '%s' "$resp" | jq -r '.success // false')"
  if [ "$ok" = "true" ]; then
    zones="$(printf '%s' "$resp" | jq -r '.result[0].results // [] | .[].name // empty')"
    while IFS= read -r z; do
      [ -n "$z" ] || continue
      if [ "$domain" = "$z" ] || [ "${domain%".$z"}" != "$domain" ]; then
        [ "${#z}" -gt "${#best}" ] && best="$z"
      fi
    done <<<"$zones"
    if [ -n "$best" ]; then
      printf '%s\n' "$best"
      return 0
    fi
    warn "no zone in D1 is a suffix of $domain — falling back to last-two-labels heuristic"
  else
    warn "zone lookup in D1 failed ($(printf '%s' "$resp" | jq -r '.errors[0].message // "unknown"')) — falling back to last-two-labels heuristic"
  fi
  printf '%s\n' "$(echo "$domain" | rev | cut -d'.' -f1,2 | rev)"
}

# d1_upsert_node — INSERT OR REPLACE the node row.
# Reads: DB_NODE_ID NODE_LABEL ADMIN_HOST DOMAIN HY2_HOST HY2_PORT HY2_OBFS_PW
#        PUBLIC_IP ZONE MODE REALITY_PUBLIC_KEY REALITY_SHORT_ID REALITY_SNI
#        REALITY_DEST NOW_MS AGENT_SHARED_SECRET
d1_upsert_node() {
  local resp ok changes
  resp="$(d1_query "$(jq -n \
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
    --arg rpk   "${REALITY_PUBLIC_KEY:-}" \
    --arg rsid  "${REALITY_SHORT_ID:-}" \
    --arg rsni  "${REALITY_SNI:-}" \
    --arg rdest "${REALITY_DEST:-}" \
    --argjson ts "$NOW_MS" \
    --arg sec   "$AGENT_SHARED_SECRET" \
    '{
      sql: "INSERT OR REPLACE INTO nodes (id,label,admin_host,vpn_host,hy2_host,hy2_port,hy2_obfs_pw,public_ip,zone,mode,status,reality_pubkey,reality_sid,reality_sni,reality_dest,last_seen_at,latency_ms,created_at,agent_secret) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,null,null,?,?)",
      params: [$id,$label,$ah,$vh,$hh,$hp,$how,$ip,$zone,$mode,"active",$rpk,$rsid,$rsni,$rdest,$ts,$sec]
    }')")"
  ok="$(printf '%s' "$resp" | jq -r '.success // false')"
  changes="$(printf '%s' "$resp" | jq -r '.result[0].meta.changes // 0')"
  if [ "$ok" = "true" ] && [ "$changes" -ge 1 ]; then
    log "node $DB_NODE_ID upserted in D1 (label=$NODE_LABEL, zone=$ZONE)"
  else
    warn "node D1 upsert failed: $(printf '%s' "$resp" | jq -r '.errors[0].message // "unknown"')"
  fi
}

# d1_ensure_user — create USER1_NAME with a random sub_token when missing.
d1_ensure_user() {
  local resp ok exists sub_token cre_resp cre_ok
  resp="$(d1_query "$(jq -n --arg uid "$USER1_NAME" \
    '{sql:"SELECT id FROM users WHERE id=?", params:[$uid]}')")"
  ok="$(printf '%s' "$resp" | jq -r '.success // false')"
  if [ "$ok" != "true" ]; then
    warn "user existence check failed (non-fatal): $(printf '%s' "$resp" | jq -r '.errors[0].message // "unknown"')"
    # Treat as existing: an API error must not be read as "user absent" and
    # trigger a write against a row we cannot see.
    return 0
  fi
  exists="$(printf '%s' "$resp" | jq -r '.result[0].results | length')"
  if [ "$exists" -ge 1 ]; then
    log "user $USER1_NAME already in D1"
    return 0
  fi
  sub_token="$(openssl rand -hex 16)"
  cre_resp="$(d1_query "$(jq -n \
    --arg uid "$USER1_NAME" \
    --arg tok "$sub_token" \
    --argjson ts "$NOW_MS" \
    '{sql:"INSERT OR IGNORE INTO users (id,name,sub_token,created_at) VALUES (?,?,?,?)", params:[$uid,$uid,$tok,$ts]}')")"
  cre_ok="$(printf '%s' "$cre_resp" | jq -r '.success // false')"
  if [ "$cre_ok" = "true" ]; then
    # Never print the full sub_token: it is a bearer credential for /sub/.
    log "user $USER1_NAME created in D1 (sub_token=${sub_token:0:6}… stored in D1)"
  else
    warn "user create failed: $(printf '%s' "$cre_resp" | jq -r '.errors[0].message // "unknown"')"
  fi
}

# d1_upsert_user_nodes — bind USER1_NAME to DB_NODE_ID with this node's creds.
d1_upsert_user_nodes() {
  local resp ok
  resp="$(d1_query "$(jq -n \
    --arg uid   "$USER1_NAME" \
    --arg nid   "$DB_NODE_ID" \
    --arg uuid  "$UUID_USER1" \
    --arg hy2pw "$HY2_PASS_USER1" \
    --argjson ts "$NOW_MS" \
    '{sql:"INSERT OR REPLACE INTO user_nodes (user_id,node_id,vless_uuid,hy2_pw,created_at) VALUES (?,?,?,?,?)", params:[$uid,$nid,$uuid,$hy2pw,$ts]}')")"
  ok="$(printf '%s' "$resp" | jq -r '.success // false')"
  if [ "$ok" = "true" ]; then
    log "user_nodes $USER1_NAME → $DB_NODE_ID upserted (uuid=$UUID_USER1)"
  else
    warn "user_nodes upsert failed: $(printf '%s' "$resp" | jq -r '.errors[0].message // "unknown"')"
  fi
}

# agent_sync — confirm the agent is live through the admin tunnel.
# The agent expects syncUser objects ({name,vless_uuid,hy2_pw}), not bare name
# strings — strings fail with "cannot unmarshal string into syncUser".
agent_sync() {
  local body resp ok
  body="$(jq -n \
    --arg n "$USER1_NAME" \
    --arg u "$UUID_USER1" \
    --arg p "$HY2_PASS_USER1" \
    '{users:[{name:$n, vless_uuid:$u, hy2_pw:$p}]}')"
  local _sync_tmpf; _sync_tmpf="$(mktemp)"
  trap 'rm -f "$_sync_tmpf"; trap - RETURN' RETURN
  chmod 600 "$_sync_tmpf"
  printf '%s' "$body" >"$_sync_tmpf"
  # Keep the shared secret and the user credentials out of argv (visible in ps)
  # via curl --config on stdin.
  resp="$( { curl -sS --max-time 30 --config - <<EOF
request = "POST"
url = "https://${ADMIN_HOST}/admin/v1/sync"
header = "Authorization: Bearer ${AGENT_SHARED_SECRET}"
header = "Content-Type: application/json"
data-binary = "@${_sync_tmpf}"
EOF
  } 2>&1 )" || resp=""
  ok="$(printf '%s' "$resp" | jq -r '.ok // false' 2>/dev/null || echo false)"
  if [ "$ok" = "true" ]; then
    log "agent sync OK — $(printf '%s' "$resp" | jq -r '.users') user(s) active on node"
  else
    warn "agent sync non-OK: $resp"
  fi
}
