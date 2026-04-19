#!/usr/bin/env bats

setup() {
  PROJECT_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  # shellcheck source=/dev/null
  source "$PROJECT_ROOT/scripts/lib/cf_api.sh"
  export CF_API_TOKEN="fake-token"
  export CF_ACCOUNT_ID="fake-account"
}

# Mock cf_req: reads fixture from $CF_MOCK_RESPONSE env var
mock_cf_req() {
  printf '%s' "$CF_MOCK_RESPONSE"
  return 0
}

@test "get_zone_id extracts id from API response" {
  export CF_MOCK_RESPONSE='{"success":true,"result":[{"id":"zone-abc","name":"example.com"}]}'
  cf_req() { mock_cf_req "$@"; }
  run get_zone_id "example.com"
  [ "$status" -eq 0 ]
  [ "$output" = "zone-abc" ]
}

@test "get_zone_id fails when zone not found" {
  export CF_MOCK_RESPONSE='{"success":true,"result":[]}'
  cf_req() { mock_cf_req "$@"; }
  run get_zone_id "missing.com"
  [ "$status" -ne 0 ]
  [[ "$output" == *"missing.com"* ]]
}

@test "get_tunnel_by_name returns tunnel id when present" {
  export CF_MOCK_RESPONSE='{"success":true,"result":[{"id":"t-123","name":"cf-vpn-example","deleted_at":null}]}'
  cf_req() { mock_cf_req "$@"; }
  run get_tunnel_by_name "cf-vpn-example"
  [ "$output" = "t-123" ]
}

@test "get_tunnel_by_name returns empty when not present" {
  export CF_MOCK_RESPONSE='{"success":true,"result":[{"id":"t-999","name":"other","deleted_at":null}]}'
  cf_req() { mock_cf_req "$@"; }
  run get_tunnel_by_name "cf-vpn-example"
  [ "$output" = "" ]
}

@test "get_tunnel_by_name ignores deleted tunnels" {
  export CF_MOCK_RESPONSE='{"success":true,"result":[{"id":"t-del","name":"cf-vpn-example","deleted_at":"2024-01-01"}]}'
  cf_req() { mock_cf_req "$@"; }
  run get_tunnel_by_name "cf-vpn-example"
  [ "$output" = "" ]
}

@test "cf_req_check errors on success=false" {
  run cf_req_check '{"success":false,"errors":[{"code":1000,"message":"bad token"}]}'
  [ "$status" -ne 0 ]
  [[ "$output" == *"bad token"* ]]
}

@test "cf_req_check passes on success=true" {
  run cf_req_check '{"success":true,"result":{}}'
  [ "$status" -eq 0 ]
}

@test "get_zone_id returns error when token is invalid" {
  export CF_MOCK_RESPONSE='{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}'
  cf_req() { mock_cf_req "$@"; }
  run get_zone_id "vpn.example.com"
  [ "$status" -ne 0 ]
  [[ "$output" == *"Authentication error"* ]]
}
