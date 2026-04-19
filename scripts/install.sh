#!/usr/bin/env bash
# Bootstrap the cf-vpn stack. Idempotent — safe to re-run.
# shellcheck source-path=SCRIPTDIR
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="$PROJECT_ROOT/.env"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/cf_api.sh
source "$SCRIPT_DIR/lib/cf_api.sh"
# shellcheck source=lib/xray_config.sh
source "$SCRIPT_DIR/lib/xray_config.sh"
# shellcheck source=lib/uri.sh
source "$SCRIPT_DIR/lib/uri.sh"

check_prereqs() {
  log "checking prerequisites"
  for cmd in docker jq curl openssl uuidgen envsubst qrencode; do
    require_cmd "$cmd"
  done
  docker compose version >/dev/null 2>&1 || die "docker compose v2 required"
  log "prereqs OK"
}

load_env() {
  [ -f "$ENV_FILE" ] || die ".env not found. Copy .env.example to .env and fill CF_API_TOKEN, CF_ACCOUNT_ID, DOMAIN."
  set -a
  # shellcheck source=/dev/null
  source "$ENV_FILE"
  set +a
  [ -n "${CF_API_TOKEN:-}" ] || die "CF_API_TOKEN not set in .env"
  [ -n "${CF_ACCOUNT_ID:-}" ] || die "CF_ACCOUNT_ID not set in .env"
  [ -n "${DOMAIN:-}" ] || die "DOMAIN not set in .env"
}

ensure_user1_secrets() {
  local name="${USER1_NAME:-user1}"
  env_write "$ENV_FILE" "USER1_NAME" "$name"
  if [ -z "$(env_read "$ENV_FILE" UUID_USER1)" ]; then
    local uuid
    uuid=$(uuidgen)
    env_write "$ENV_FILE" "UUID_USER1" "$uuid"
    log "generated UUID_USER1"
  fi
  if [ -z "$(env_read "$ENV_FILE" TROJAN_PASS_USER1)" ]; then
    local pw
    pw=$(openssl rand -base64 24 | tr -d '\n')
    env_write "$ENV_FILE" "TROJAN_PASS_USER1" "$pw"
    log "generated TROJAN_PASS_USER1"
  fi
}

main() {
  check_prereqs
  load_env
  ensure_user1_secrets
  log "prereq + secrets ready; tunnel setup in next step"
}

main "$@"
