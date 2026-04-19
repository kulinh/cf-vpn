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

tunnel_name() {
  # Derive from DOMAIN: replace dots with dashes, prefix cf-vpn-
  printf 'cf-vpn-%s' "${DOMAIN//./-}"
}

ensure_tunnel() {
  local name tid
  name=$(tunnel_name)
  tid=$(get_tunnel_by_name "$name" || true)
  if [ -n "$tid" ]; then
    log "tunnel '$name' exists: $tid"
  else
    log "creating tunnel '$name'"
    local cred_file_tmp
    cred_file_tmp=$(mktemp)
    # Capture stdout (tunnel_id) separately from FD 3 (credentials)
    tid=$(create_tunnel "$name" 3>"$cred_file_tmp")
    [ -n "$tid" ] || die "failed to create tunnel"
    local cred_dest="$PROJECT_ROOT/cloudflared/${tid}.json"
    mv "$cred_file_tmp" "$cred_dest"
    chmod 600 "$cred_dest"
    log "tunnel created: $tid (credentials at $cred_dest)"
  fi
  env_write "$ENV_FILE" "TUNNEL_UUID" "$tid"
  export TUNNEL_UUID="$tid"

  # Ensure credentials file exists (user may have deleted; detect missing)
  if [ ! -f "$PROJECT_ROOT/cloudflared/${tid}.json" ]; then
    die "credentials file missing at cloudflared/${tid}.json — delete tunnel in CF dashboard and re-run"
  fi
}

ensure_dns() {
  local zone_id
  zone_id=$(get_zone_id "$DOMAIN")
  log "zone id for $DOMAIN: $zone_id"
  upsert_dns_cname "$zone_id" "$DOMAIN" "${TUNNEL_UUID}.cfargotunnel.com"
  log "DNS CNAME $DOMAIN -> ${TUNNEL_UUID}.cfargotunnel.com (proxied)"
}

render_templates() {
  set -a
  # shellcheck source=/dev/null
  source "$ENV_FILE"
  set +a
  envsubst < "$PROJECT_ROOT/xray/config.template.json" > "$PROJECT_ROOT/xray/config.json"
  jq . "$PROJECT_ROOT/xray/config.json" >/dev/null || die "rendered xray config is not valid JSON"
  envsubst < "$PROJECT_ROOT/cloudflared/config.template.yml" > "$PROJECT_ROOT/cloudflared/config.yml"
  log "templates rendered"
}

compose_up() {
  cd "$PROJECT_ROOT"
  docker compose up -d
  log "waiting 15s for services to settle"
  sleep 15
  docker compose ps
}

probe_tunnel() {
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "https://${DOMAIN}/vless" || true)
  case "$code" in
    400|426) log "probe OK: /vless returned $code (WS upgrade expected)" ;;
    *) log "WARN: /vless returned $code — tunnel may still be propagating; retry in 1-2 minutes" ;;
  esac
}

main() {
  check_prereqs
  load_env
  ensure_user1_secrets
  ensure_tunnel
  ensure_dns
  render_templates
  compose_up
  probe_tunnel
  log "install complete. Run scripts/gen-subscription.sh to print subscription for ${USER1_NAME:-user1}."
}

main "$@"
