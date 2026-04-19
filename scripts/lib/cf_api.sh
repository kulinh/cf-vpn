#!/usr/bin/env bash
# Cloudflare API wrappers. Source-only.
# Requires env: CF_API_TOKEN, CF_ACCOUNT_ID.
# All calls go through cf_req so tests can stub it.

CF_API_BASE="${CF_API_BASE:-https://api.cloudflare.com/client/v4}"

# cf_req <method> <path> [json-body]
# Prints raw response body. Does NOT validate success — use cf_req_check on the output.
# Uses --fail-with-body so non-2xx still yields the JSON error body AND a non-zero exit.
cf_req() {
  local method="$1" path="$2" body="${3:-}"
  local url="${CF_API_BASE}${path}"
  local rc=0
  if [ -n "$body" ]; then
    curl -sS --fail-with-body -X "$method" "$url" \
      -H "Authorization: Bearer ${CF_API_TOKEN}" \
      -H "Content-Type: application/json" \
      --data "$body" || rc=$?
  else
    curl -sS --fail-with-body -X "$method" "$url" \
      -H "Authorization: Bearer ${CF_API_TOKEN}" || rc=$?
  fi
  if [ "$rc" -ne 0 ]; then
    printf 'cf_req: curl exit %d for %s %s\n' "$rc" "$method" "$path" >&2
    return "$rc"
  fi
}

# cf_req_check <response-json>
# Exits non-zero with error messages if success != true.
cf_req_check() {
  local resp="$1"
  local ok
  ok=$(printf '%s' "$resp" | jq -r '.success // false')
  if [ "$ok" != "true" ]; then
    local msgs
    msgs=$(printf '%s' "$resp" | jq -r '.errors[]? | "\(.code): \(.message)"' 2>/dev/null)
    printf 'cf api error: %s\n' "$msgs" >&2
    return 1
  fi
  return 0
}

# get_zone_id <domain>
# Matches the zone whose name is an apex suffix of <domain>.
get_zone_id() {
  local domain="$1"
  # Walk progressively shorter suffixes until a matching zone is found.
  # CF API: GET /zones?name=<apex>
  local apex="$domain"
  local resp id
  while [ -n "$apex" ]; do
    resp=$(cf_req GET "/zones?name=${apex}")
    cf_req_check "$resp" || return 1
    id=$(printf '%s' "$resp" | jq -r '.result[0].id // empty')
    if [ -n "$id" ]; then
      printf '%s' "$id"
      return 0
    fi
    # Strip first label
    if [[ "$apex" == *.* ]]; then
      apex="${apex#*.}"
    else
      break
    fi
  done
  printf 'zone not found for domain: %s\n' "$domain" >&2
  return 1
}

# get_tunnel_by_name <name>
# Prints tunnel id if a non-deleted tunnel with that name exists, else empty.
get_tunnel_by_name() {
  local name="$1"
  local resp
  resp=$(cf_req GET "/accounts/${CF_ACCOUNT_ID}/cfd_tunnel?name=${name}")
  cf_req_check "$resp" || return 1
  printf '%s' "$resp" | jq -r --arg n "$name" \
    '[.result[]? | select(.name==$n) | select(.deleted_at==null)] | .[0].id // empty'
}

# create_tunnel <name>
# Returns tunnel id on stdout. Generates a random 32-byte base64 secret.
# Credentials JSON is written to FD 3, which the caller MUST open to a secure file.
create_tunnel() {
  local name="$1"
  local secret tunnel_id resp
  secret=$(openssl rand -base64 32 | tr -d '\n')
  local body
  body=$(jq -n --arg n "$name" --arg s "$secret" \
    '{name: $n, tunnel_secret: $s, config_src: "local"}')
  resp=$(cf_req POST "/accounts/${CF_ACCOUNT_ID}/cfd_tunnel" "$body")
  cf_req_check "$resp" || return 1
  tunnel_id=$(printf '%s' "$resp" | jq -r '.result.id')
  local cred
  cred=$(jq -n --arg acct "$CF_ACCOUNT_ID" --arg tid "$tunnel_id" --arg s "$secret" \
    '{AccountTag: $acct, TunnelID: $tid, TunnelSecret: $s}')
  # Credentials MUST be written to a file descriptor opened by the caller.
  # Usage: tunnel_id=$(create_tunnel "$name" 3>"$CREDS_PATH")
  if ! { true >&3; } 2>/dev/null; then
    printf 'create_tunnel: FD 3 not open — caller must redirect 3>credentials.json to avoid leaking tunnel_secret to logs\n' >&2
    return 1
  fi
  printf '%s\n' "$cred" >&3
  printf '%s' "$tunnel_id"
}

# delete_tunnel <tunnel-id>
delete_tunnel() {
  local tid="$1"
  local resp
  resp=$(cf_req DELETE "/accounts/${CF_ACCOUNT_ID}/cfd_tunnel/${tid}")
  cf_req_check "$resp"
}

# upsert_dns_cname <zone-id> <name> <target>
# Creates CNAME proxied=true if missing; updates if present.
upsert_dns_cname() {
  local zone_id="$1" name="$2" target="$3"
  local resp existing_id
  resp=$(cf_req GET "/zones/${zone_id}/dns_records?type=CNAME&name=${name}")
  cf_req_check "$resp" || return 1
  existing_id=$(printf '%s' "$resp" | jq -r '.result[0].id // empty')
  local body
  body=$(jq -n --arg name "$name" --arg content "$target" \
    '{type: "CNAME", name: $name, content: $content, proxied: true, ttl: 1}')
  if [ -n "$existing_id" ]; then
    resp=$(cf_req PUT "/zones/${zone_id}/dns_records/${existing_id}" "$body")
  else
    resp=$(cf_req POST "/zones/${zone_id}/dns_records" "$body")
  fi
  cf_req_check "$resp"
}
