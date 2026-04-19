#!/usr/bin/env bats

setup() {
  PROJECT_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  # shellcheck source=/dev/null
  source "$PROJECT_ROOT/scripts/lib/xray_config.sh"
  TMP="$(mktemp -d)"
  cp "$PROJECT_ROOT/tests/fixtures/xray_config.empty.json" "$TMP/config.json"
  cp "$PROJECT_ROOT/tests/fixtures/xray_config.2users.json" "$TMP/2users.json"
}

teardown() {
  rm -rf "$TMP"
}

@test "count_clients returns 0 for empty config" {
  run count_clients "$TMP/config.json"
  [ "$status" -eq 0 ]
  [ "$output" = "0" ]
}

@test "count_clients returns 2 for 2-user config" {
  run count_clients "$TMP/2users.json"
  [ "$output" = "2" ]
}

@test "add_client appends to both inbounds" {
  add_client "$TMP/config.json" "charlie" "ccccccc1-cccc-cccc-cccc-cccccccccccc" "charlie-secret"
  run count_clients "$TMP/config.json"
  [ "$output" = "1" ]
  # Verify UUID in vless
  run jq -r '.inbounds[] | select(.tag=="vless-ws") | .settings.clients[0].id' "$TMP/config.json"
  [ "$output" = "ccccccc1-cccc-cccc-cccc-cccccccccccc" ]
  # Verify password in trojan
  run jq -r '.inbounds[] | select(.tag=="trojan-ws") | .settings.clients[0].password' "$TMP/config.json"
  [ "$output" = "charlie-secret" ]
  # Verify email matches
  run jq -r '.inbounds[] | select(.tag=="vless-ws") | .settings.clients[0].email' "$TMP/config.json"
  [ "$output" = "charlie@vpn" ]
}

@test "add_client rejects duplicate name" {
  add_client "$TMP/2users.json" "alice" "new-uuid" "new-pass"
  # count should still be 2 (no-op or error)
  run count_clients "$TMP/2users.json"
  [ "$output" = "2" ]
}

@test "remove_client drops user from both inbounds" {
  remove_client "$TMP/2users.json" "alice"
  run count_clients "$TMP/2users.json"
  [ "$output" = "1" ]
  run jq -r '.inbounds[] | select(.tag=="vless-ws") | .settings.clients[0].email' "$TMP/2users.json"
  [ "$output" = "bob@vpn" ]
}

@test "remove_client no-op for missing user" {
  remove_client "$TMP/2users.json" "zach"
  run count_clients "$TMP/2users.json"
  [ "$output" = "2" ]
}

@test "list_client_names returns sorted names" {
  run list_client_names "$TMP/2users.json"
  [ "${lines[0]}" = "alice" ]
  [ "${lines[1]}" = "bob" ]
}

@test "get_client_uuid returns uuid by name" {
  run get_client_uuid "$TMP/2users.json" "alice"
  [ "$output" = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" ]
}

@test "get_client_password returns trojan password by name" {
  run get_client_password "$TMP/2users.json" "bob"
  [ "$output" = "bob-pass" ]
}
