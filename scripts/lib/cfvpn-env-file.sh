#!/usr/bin/env bash
# cfvpn-env-file.sh — guard + write /etc/cfvpn/cfvpn.env for the installers.
#
# Executed (not sourced) so install-node-CN.sh can run the exact same logic on
# the remote target over SSH:
#
#   bash scripts/lib/cfvpn-env-file.sh check
#       Exit 0 if it is safe to (re)install, 3 if the node is already
#       provisioned and FORCE_REINSTALL is not 1.
#
#   printf 'KEY=VALUE\n...' | bash scripts/lib/cfvpn-env-file.sh write
#       Write the bootstrap keys from stdin. Keys already in the file that are
#       NOT on stdin are preserved (cfvpnctl install reads this file back and
#       expects operator-supplied keys to survive). With FORCE_REINSTALL=1 the
#       old file is backed up to <file>.bak-<unixtime> (0600); only the keys
#       that must not be regenerated (ADMIN_TUNNEL_UUID, CF_*) are carried over.
#
#   bash scripts/lib/cfvpn-env-file.sh mark-installed
#       Called by the installers ONLY after `cfvpnctl install` succeeded. This
#       is what arms the guard: a run that dies before that point (or at the
#       systemd-unit gate) can simply be retried, because nothing irreplaceable
#       has been generated yet — the shell writes AGENT_SHARED_SECRET *before*
#       calling cfvpnctl, so keying the guard on that would lock the operator
#       out of their own retry.
#
# Environment:
#   FORCE_REINSTALL=1   allow re-installing over a provisioned node
#   CFVPN_ENV_FILE      override the target path (tests)
set -euo pipefail

ENV_FILE="${CFVPN_ENV_FILE:-/etc/cfvpn/cfvpn.env}"
ENV_DIR="$(dirname "$ENV_FILE")"
INSTALLED_MARKER="$ENV_DIR/.installed"
FORCE="${FORCE_REINSTALL:-0}"

# Keys written by the GO installer (never by the shell pre-write). Their
# presence means `cfvpnctl install` completed at least once, so they act as the
# marker for nodes provisioned before .installed existed.
GO_KEYS_RE='^(REALITY_PRIVATE_KEY|ADMIN_TUNNEL_UUID)='

# Keys that must survive even a forced re-provision: ADMIN_TUNNEL_UUID because
# dropping it makes cfvpnctl create a second admin tunnel and orphan the first,
# and the CF credentials because they are the operator's, not the node's.
CARRY_ON_FORCE_RE='^(ADMIN_TUNNEL_UUID|CF_API_TOKEN|CF_ACCOUNT_ID)='

# Everything a re-install would regenerate, i.e. what the operator loses.
LOSS_KEYS='REALITY_PRIVATE_KEY REALITY_PUBLIC_KEY REALITY_SHORT_ID UUID_USER1 HY2_PASS_USER1 HY2_OBFS_PW AGENT_SHARED_SECRET'

log()  { printf '[cfvpn-env] %s\n' "$*"; }
die()  { printf '[cfvpn-env] ERROR: %s\n' "$*" >&2; exit 1; }

node_is_provisioned() {
  [ -f "$INSTALLED_MARKER" ] && return 0
  [ -f "$ENV_FILE" ] && grep -qE "$GO_KEYS_RE" "$ENV_FILE"
}

refuse() {
  local k
  printf '[cfvpn-env] ERROR: this node is already provisioned (%s).\n' \
    "$( [ -f "$INSTALLED_MARKER" ] && echo "$INSTALLED_MARKER exists" || echo "$ENV_FILE holds installer-generated keys" )" >&2
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
  local body tmp bak preserve_re key carried
  body="$(cat)"
  [ -n "$body" ] || die "no env content on stdin"

  install -d -m 700 "$(dirname "$ENV_FILE")"

  if node_is_provisioned; then
    [ "$FORCE" = "1" ] || refuse
    if [ -f "$ENV_FILE" ]; then
      bak="${ENV_FILE}.bak-$(date +%s)"
      install -m 600 "$ENV_FILE" "$bak"
      log "FORCE_REINSTALL=1 — backed up $ENV_FILE -> $bak"
      # Forced re-provision: drop everything the installer will regenerate, but
      # keep ADMIN_TUNNEL_UUID (without it cfvpnctl creates a SECOND admin
      # tunnel and orphans the first) and the operator's CF credentials.
      carried="$(grep -E "$CARRY_ON_FORCE_RE" "$ENV_FILE" || true)"
      if [ -n "$carried" ]; then
        printf '%s\n' "$carried" >"$ENV_FILE"
      else
        : >"$ENV_FILE"
      fi
      chmod 600 "$ENV_FILE"
    fi
    # The node is being provisioned again: re-arm only once it succeeds.
    rm -f "$INSTALLED_MARKER"
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

# Arm the guard. Called only once `cfvpnctl install` has actually succeeded, so
# an install that fails before that point stays retryable.
cmd_mark_installed() {
  install -d -m 700 "$ENV_DIR"
  printf 'installed_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$INSTALLED_MARKER"
  chmod 600 "$INSTALLED_MARKER"
  log "marked provisioned: $INSTALLED_MARKER"
}

case "${1:-}" in
  check)          cmd_check ;;
  write)          cmd_write ;;
  mark-installed) cmd_mark_installed ;;
  *) die "usage: $0 check|write|mark-installed  (write reads KEY=VALUE lines from stdin)" ;;
esac
