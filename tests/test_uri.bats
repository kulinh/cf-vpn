#!/usr/bin/env bats

setup() {
  PROJECT_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  # shellcheck source=/dev/null
  source "$PROJECT_ROOT/scripts/lib/uri.sh"
}

@test "build_vless_uri produces correct scheme and query" {
  run build_vless_uri "11111111-2222-3333-4444-555555555555" "vpn.example.com" "alice"
  [ "$status" -eq 0 ]
  [[ "$output" == vless://11111111-2222-3333-4444-555555555555@vpn.example.com:443* ]]
  [[ "$output" == *"encryption=none"* ]]
  [[ "$output" == *"security=tls"* ]]
  [[ "$output" == *"type=ws"* ]]
  [[ "$output" == *"host=vpn.example.com"* ]]
  [[ "$output" == *"path=%2Fvless"* ]]
  [[ "$output" == *"sni=vpn.example.com"* ]]
  [[ "$output" == *"#alice-VLESS"* ]]
}

@test "build_trojan_uri produces correct scheme and query" {
  run build_trojan_uri "pass%word with spaces" "vpn.example.com" "bob"
  [ "$status" -eq 0 ]
  [[ "$output" == trojan://* ]]
  [[ "$output" == *"@vpn.example.com:443"* ]]
  [[ "$output" == *"type=ws"* ]]
  [[ "$output" == *"path=%2Ftrojan"* ]]
  [[ "$output" == *"#bob-Trojan"* ]]
  # Password must be URL-encoded (space becomes %20)
  [[ "$output" == *"pass%25word%20with%20spaces"* ]]
}

@test "build_subscription_b64 concatenates and base64-encodes two URIs" {
  vless="vless://a@h:443"
  trojan="trojan://b@h:443"
  run build_subscription_b64 "$vless" "$trojan"
  [ "$status" -eq 0 ]
  decoded=$(echo "$output" | base64 -d)
  [[ "$decoded" == *"$vless"* ]]
  [[ "$decoded" == *"$trojan"* ]]
}

@test "urlencode encodes reserved characters" {
  run urlencode "a b/c?d=e"
  [ "$output" = "a%20b%2Fc%3Fd%3De" ]
}
