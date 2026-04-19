#!/usr/bin/env bats

setup() {
  PROJECT_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  # shellcheck source=/dev/null
  source "$PROJECT_ROOT/scripts/lib/common.sh"
}

@test "log prefixes message with [cf-vpn]" {
  run log "hello"
  [ "$status" -eq 0 ]
  [[ "$output" == *"[cf-vpn] hello"* ]]
}

@test "die exits non-zero with message on stderr" {
  run bash -c "source '$PROJECT_ROOT/scripts/lib/common.sh' && die 'boom'"
  [ "$status" -ne 0 ]
  [[ "$output" == *"boom"* ]]
}

@test "require_cmd succeeds for existing binary" {
  run require_cmd bash
  [ "$status" -eq 0 ]
}

@test "require_cmd fails for missing binary" {
  run require_cmd definitely-not-a-real-binary-xyz
  [ "$status" -ne 0 ]
  [[ "$output" == *"definitely-not-a-real-binary-xyz"* ]]
}

@test "env_write creates file and env_read parses key" {
  tmpdir="$(mktemp -d)"
  envfile="$tmpdir/.env"
  env_write "$envfile" "FOO" "bar baz"
  run env_read "$envfile" "FOO"
  [ "$status" -eq 0 ]
  [ "$output" = "bar baz" ]
  rm -rf "$tmpdir"
}

@test "env_write updates existing key in place" {
  tmpdir="$(mktemp -d)"
  envfile="$tmpdir/.env"
  env_write "$envfile" "K" "v1"
  env_write "$envfile" "K" "v2"
  run env_read "$envfile" "K"
  [ "$output" = "v2" ]
  # Only one line for K
  count=$(grep -c '^K=' "$envfile")
  [ "$count" -eq 1 ]
  rm -rf "$tmpdir"
}
