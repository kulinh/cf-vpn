#!/usr/bin/env bash
# cfvpn-env-file.sh — guard + write /etc/cfvpn/cfvpn.env for the installers.
#
# Executed (not sourced) so install-node-CN.sh can run the exact same logic on
# the remote target over SSH:
#
#   bash scripts/lib/cfvpn-env-file.sh check
#       Exit 0 if it is safe to (re)install, 3 if the env file already holds
#       node secrets and FORCE_REINSTALL is not 1.
#
#   printf 'KEY=VALUE\n...' | bash scripts/lib/cfvpn-env-file.sh write
#       Write the bootstrap keys from stdin. Keys already in the file that are
#       NOT on stdin are preserved (cfvpnctl install reads this file back and
#       expects operator-supplied keys to survive). With FORCE_REINSTALL=1 the
#       old file is backed up to <file>.bak-<unixtime> (0600) and then dropped.
#
# Environment:
#   FORCE_REINSTALL=1   allow re-installing over a provisioned node
#   CFVPN_ENV_FILE      override the target path (tests)
set -euo pipefail

ENV_FILE="${CFVPN_ENV_FILE:-/etc/cfvpn/cfvpn.env}"
FORCE="${FORCE_REINSTALL:-0}"

# Secrets that are generated once per node and can never be recovered from
# anywhere else. If either is present, the node is already provisioned.
SECRET_KEYS_RE='^(REALITY_PRIVATE_KEY|AGENT_SHARED_SECRET)='

# Everything a re-install would regenerate, i.e. what the operator loses.
LOSS_KEYS='REALITY_PRIVATE_KEY REALITY_PUBLIC_KEY REALITY_SHORT_ID UUID_USER1 HY2_PASS_USER1 HY2_OBFS_PW AGENT_SHARED_SECRET ADMIN_TUNNEL_UUID'

log()  { printf '[cfvpn-env] %s\n' "$*"; }
die()  { printf '[cfvpn-env] ERROR: %s\n' "$*" >&2; exit 1; }

node_is_provisioned() {
  [ -f "$ENV_FILE" ] && grep -qE "$SECRET_KEYS_RE" "$ENV_FILE"
}

refuse() {
  local k
  printf '[cfvpn-env] ERROR: %s already exists and holds this node'"'"'s secrets.\n' "$ENV_FILE" >&2
  printf '  Re-running the installer regenerates them, which permanently breaks every\n' >&2
  printf '  client already configured for this node. It would replace:\n' >&2
  for k in $LOSS_KEYS; do
    grep -qE "^${k}=" "$ENV_FILE" 2>/dev/null && printf '    - %s\n' "$k" >&2
  done
  printf '  To upgrade an existing node use:  sudo cfvpnctl upgrade\n' >&2
  printf '  To change its domain use:         sudo cfvpnctl rotate-domain\n' >&2
  printf '  If you really mean to re-provision from scratch, re-run with\n' >&2
  printf '  FORCE_REINSTALL=1 (the current env file is backed up first).\n' >&2
  exit 3
}

cmd_check() {
  if node_is_provisioned && [ "$FORCE" != "1" ]; then
    refuse
  fi
  return 0
}

cmd_write() {
  local body tmp bak preserve_re key
  body="$(cat)"
  [ -n "$body" ] || die "no env content on stdin"

  install -d -m 700 "$(dirname "$ENV_FILE")"

  if node_is_provisioned; then
    [ "$FORCE" = "1" ] || refuse
    bak="${ENV_FILE}.bak-$(date +%s)"
    install -m 600 "$ENV_FILE" "$bak"
    log "FORCE_REINSTALL=1 — backed up $ENV_FILE -> $bak"
    # Forced re-provision: start from an empty file so nothing stale survives.
    : >"$ENV_FILE"
    chmod 600 "$ENV_FILE"
  fi

  # Keys we are about to write; anything else in the existing file is carried
  # over verbatim so `cfvpnctl install` still sees what it wrote last time.
  preserve_re=""
  while IFS= read -r key; do
    key="${key%%=*}"
    [ -n "$key" ] || continue
    preserve_re="${preserve_re:+$preserve_re|}${key}"
  done <<<"$body"

  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' EXIT INT TERM
  chmod 600 "$tmp"
  if [ -s "$ENV_FILE" ]; then
    grep -vE "^(${preserve_re})=" "$ENV_FILE" >>"$tmp" || true
  fi
  printf '%s\n' "$body" >>"$tmp"
  install -m 600 "$tmp" "$ENV_FILE"
  rm -f "$tmp"
  trap - EXIT INT TERM
  log "$ENV_FILE written"
}

case "${1:-}" in
  check) cmd_check ;;
  write) cmd_write ;;
  *) die "usage: $0 check|write  (write reads KEY=VALUE lines from stdin)" ;;
esac
