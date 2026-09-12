#!/usr/bin/env bash
# cfvpn-common.sh — helpers shared by scripts/install-node.sh and
# scripts/install-node-CN.sh. Source it, do not execute it.
#
# The caller is expected to define log()/warn()/die(); minimal fallbacks are
# installed below so the library is usable (and testable) on its own.
#
# Provides:
#   ssh_run             ARGV...                   — run one command on TARGET_HOST
#   cfvpn_env_value_ok  VALUE                     — env-file value safety check
#   cfvpn_require_env_value NAME VALUE            — same, but die() on reject
#   cfvpn_sha256_file   FILE                      — lowercase sha256 of FILE
#   cfvpn_extract_sha256 NAME  (checksum on stdin)— parse a checksum file
#   cfvpn_verify_sha256 FILE EXPECTED LABEL       — compare, die on mismatch
#   cfvpn_curl_dl       URL OUT                   — hardened download
#   cfvpn_download_verified URL OUT CKSUM_URL [NAME]
#   cfvpn_env_read      [FILE] KEY...             — export KEYs from an env file
#   cfvpn_ensure_ufw_ssh_allowed [PORT]           — keep SSH open when ufw is on
#   cfvpn_is_oci        [TAG_FILE] [VENDOR_FILE]  — is this an Oracle Cloud VM?

[ -n "${_CFVPN_COMMON_SH:-}" ] && return 0
_CFVPN_COMMON_SH=1

declare -F log  >/dev/null || log()  { printf '[cfvpn] %s\n' "$*"; }
declare -F warn >/dev/null || warn() { printf '[cfvpn] WARN: %s\n' "$*" >&2; }
declare -F die  >/dev/null || die()  { printf '[cfvpn] ERROR: %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# ssh_run — ARGV ONLY.
#
# Contract: `ssh_run cmd arg1 arg2` runs exactly that command with exactly
# those arguments on $TARGET_HOST. Each argument is re-quoted with printf %q,
# because ssh joins argv with spaces into one string that the remote login
# shell re-parses — without the re-quoting anything relying on local quoting
# is lost in transit.
#
# The flip side: a whole shell snippet passed as ONE argument is quoted too, so
# the remote tries to execute a command *named* `awk "NR>1 …"` and fails with
# 127. To run a script remotely, feed it on stdin:
#
#     ssh_run bash -s <<'EOF'
#     …script…
#     EOF
#
# Requires the caller to set TARGET_HOST and the _ssh_opts array.
# ---------------------------------------------------------------------------
# shellcheck disable=SC2154  # _ssh_opts/TARGET_HOST are set by the caller
ssh_run() { ssh "${_ssh_opts[@]}" "$TARGET_HOST" "$(printf '%q ' "$@")"; }

# ---------------------------------------------------------------------------
# env-file value safety
#
# internal/state/store.go splits each line on the FIRST '=' and keeps the rest
# of the line verbatim — it does NOT strip quotes. So values must be written
# unquoted (a quoted value would reach Go with its quotes attached). The same
# file is also `.`-sourced by install-node.sh and read by systemd's
# EnvironmentFile=, so an unquoted value containing whitespace, '$', a
# backtick, a quote, a backslash or ';' would either execute code or silently
# truncate. Values that cannot be written safely are rejected instead.
# ---------------------------------------------------------------------------
cfvpn_env_value_ok() {
  local v="$1"
  case "$v" in
    *[[:space:]]*|*'$'*|*'`'*|*'"'*|*"'"*|*'\'*|*';'*|*'#'*) return 1 ;;
  esac
  # reject control characters (newline is already covered by [[:space:]])
  case "$v" in
    *[[:cntrl:]]*) return 1 ;;
  esac
  return 0
}

cfvpn_require_env_value() {
  local name="$1" value="$2"
  cfvpn_env_value_ok "$value" || die "$name contains characters that cannot be written to /etc/cfvpn/cfvpn.env unquoted (whitespace, \$, backtick, quote, backslash, ';' or '#'). Fix the value and re-run."
}

# ---------------------------------------------------------------------------
# cfvpn_env_read [FILE] KEY...
#
# Exports only the named KEYs from an env file. FILE is recognised by the '/'
# in it; without one the fleet default /etc/cfvpn/cfvpn.env is used.
#
# The file is deliberately NOT sourced. `. /etc/cfvpn/cfvpn.env` hands every
# value to the shell, so a `$(...)` that ever reached a value — from a provider
# API, a hand edit, or an operator paste — executes as root the moment the
# installer reads its own state back. Splitting on the FIRST '=' and keeping the
# remainder verbatim is also exactly how internal/state/store.go reads the same
# file, so the shell and Go sides never disagree about a value.
# ---------------------------------------------------------------------------
cfvpn_env_read() {
  local file="/etc/cfvpn/cfvpn.env"
  case "${1:-}" in */*) file="$1"; shift ;; esac
  [ "$#" -gt 0 ] || { warn "cfvpn_env_read: no keys requested"; return 0; }
  [ -r "$file" ] || { warn "cfvpn_env_read: cannot read $file"; return 1; }
  local k v want
  # `|| [ -n "$k" ]`: a file whose last line has no trailing newline would
  # otherwise lose that line, and a hand-edited cfvpn.env often does.
  while IFS='=' read -r k v || [ -n "$k" ]; do
    [ -n "$k" ] || continue
    for want in "$@"; do
      # Only an exact match is exported, so the name being assigned here is one
      # of the caller's literals and never something the file chose.
      [ "$k" = "$want" ] && { export "$k=$v"; break; }
    done
  done < "$file"
  return 0
}

# cfvpn_port_ok VALUE — a listenable, non-privileged TCP/UDP port.
# The lower bound is 1024 because every cfvpn listener runs unprivileged, and the
# same rule is applied by the Go installer; both installers check HY2_PORT with
# this so a typo fails before anything is mutated (the port is advertised to
# clients verbatim, so a wrong one is not silently recoverable).
cfvpn_port_ok() {
  local v="$1"
  case "$v" in ''|*[!0-9]*) return 1 ;; esac
  [ "$v" -ge 1024 ] && [ "$v" -le 65535 ]
}

cfvpn_require_port() {
  local name="$1" value="$2"
  cfvpn_port_ok "$value" || die "$name must be an integer in [1024,65535] (got: $value)"
}

# ---------------------------------------------------------------------------
# cfvpn_ensure_ufw_ssh_allowed [PORT]
#
# Keeps SSH reachable before anything else touches the firewall. PORT defaults
# to 22; the fleet baseline moves sshd to 17722, so the installers pass
# "${SSH_PORT:-22}" — whitelisting the wrong port is how a remote install ends
# with a box nobody can log into. No-op when ufw is absent or inactive.
# ---------------------------------------------------------------------------
cfvpn_ensure_ufw_ssh_allowed() {
  local port="${1:-22}" status
  command -v ufw >/dev/null 2>&1 || return 0
  # Capture first: `ufw status | grep -q` closes the pipe on the first match, and
  # under `set -o pipefail` the SIGPIPE from ufw becomes the status of the test —
  # skipping the allow rule on exactly the hosts that have ufw enabled.
  status="$(ufw status 2>/dev/null || true)"
  case "$status" in *'Status: active'*) ;; *) return 0 ;; esac
  log "ufw is active — ensuring SSH ($port/tcp) stays allowed"
  # The OpenSSH profile is only correct while sshd is on 22; on any other port
  # it would open 22 and leave the real one closed.
  if [ "$port" = "22" ] && ufw allow OpenSSH; then
    return 0
  fi
  ufw allow "$port/tcp" || warn "could not whitelist SSH on $port/tcp; verify manually before disconnecting"
  return 0
}

# ---------------------------------------------------------------------------
# sha256 verification for downloaded binaries
# ---------------------------------------------------------------------------
cfvpn_sha256_file() {
  sha256sum "$1" | awk '{print tolower($1)}'
}

# cfvpn_extract_sha256 NAME  < checksum-file
# Understands the formats upstream projects actually publish:
#   "<hash>  filename"          (sha256sum / *.sha256sum / sha256sum.txt)
#   "<hash> *filename"          (sha256sum binary mode)
#   "<hash>  build/filename"    (hashes.txt written from a build dir)
#   "<hash>"                    (bare single-hash *.sha256 file)
#   "SHA2-256= <hash>"          (openssl dgst style, e.g. Xray *.dgst)
#   "SHA256(filename)= <hash>"
# Matching is on the BASENAME as a whole field: a substring match would hand
# back the hash of hysteria-linux-amd64-avx for hysteria-linux-amd64, while a
# path-sensitive match would miss "build/hysteria-linux-amd64" (which is the
# shape apernet/hysteria actually publishes). Otherwise a file containing
# exactly one hash is accepted; a multi-entry file with no match is a failure
# (never guess).
cfvpn_extract_sha256() {
  local want="$1" want_base line tok hash matched lone="" lone_count=0
  want_base="${want##*/}"
  while IFS= read -r line || [ -n "$line" ]; do
    hash=""; matched=0
    for tok in $line; do
      tok="${tok##*=}"
      tok="${tok##*\)}"
      if [ -z "$hash" ] && [[ "$tok" =~ ^[0-9a-fA-F]{64}$ ]]; then
        hash="${tok,,}"
        continue
      fi
      # sha256sum writes "<hash>  name" (text) or "<hash> *name" (binary), and
      # the name may carry any directory prefix ("./", "build/", …).
      tok="${tok#\*}"
      tok="${tok##*/}"
      # SHA256(name)= <hash> puts the name before the '=' we stripped above,
      # so compare against the basename in every position.
      [ -n "$want_base" ] && [ "$tok" = "$want_base" ] && matched=1
    done
    [ -n "$hash" ] || continue
    if [ "$matched" -eq 1 ]; then
      printf '%s\n' "$hash"
      return 0
    fi
    lone="$hash"
    lone_count=$((lone_count + 1))
  done
  if [ "$lone_count" -eq 1 ]; then
    printf '%s\n' "$lone"
    return 0
  fi
  return 1
}

# cfvpn_verify_sha256 FILE EXPECTED LABEL
cfvpn_verify_sha256() {
  local file="$1" expected="${2,,}" label="$3" actual
  [ -f "$file" ] || die "$label: $file missing, cannot verify checksum"
  if ! [[ "$expected" =~ ^[0-9a-f]{64}$ ]]; then
    die "$label: no usable sha256 to verify against (got: '${expected:-<empty>}')"
  fi
  actual="$(cfvpn_sha256_file "$file")"
  if [ "$actual" != "$expected" ]; then
    die "$label: sha256 MISMATCH — refusing to install unverified binary
  expected: $expected
  actual:   $actual
  file:     $file"
  fi
  log "    sha256 OK ($label)"
}

# cfvpn_curl_dl URL OUT [extra curl args...]
# Hardened: https only, no plaintext downgrade on redirect, TLS >= 1.2, retries.
cfvpn_curl_dl() {
  local url="$1" out="$2"; shift 2
  curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --retry 3 --retry-connrefused --max-time 300 "$@" "$url" -o "$out"
}

# cfvpn_download_verified URL OUT NAME CHECKSUM_URL [CHECKSUM_URL...]
# Downloads URL to OUT, then verifies it against the first checksum URL that
# yields a usable sha256 for NAME (projects publish these under several
# conventions: <asset>.sha256, sha256sum.txt, <asset>.dgst, …).
# A mismatch is NEVER tolerated. Set CFVPN_ALLOW_UNVERIFIED_DOWNLOADS=1 to
# continue (loudly) when no checksum file can be fetched at all.
cfvpn_download_verified() {
  local url="$1" out="$2" name="$3"; shift 3
  local cksum_url cksum_raw expected=""
  cfvpn_curl_dl "$url" "$out"
  for cksum_url in "$@"; do
    if cksum_raw="$(curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
        --retry 2 --retry-connrefused --max-time 60 "$cksum_url" 2>/dev/null)"; then
      expected="$(printf '%s\n' "$cksum_raw" | cfvpn_extract_sha256 "$name" || true)"
      [ -n "$expected" ] && break
    fi
  done
  if [ -z "$expected" ]; then
    if [ "${CFVPN_ALLOW_UNVERIFIED_DOWNLOADS:-0}" = "1" ]; then
      warn "$name: no sha256 available from $* — continuing because CFVPN_ALLOW_UNVERIFIED_DOWNLOADS=1"
      return 0
    fi
    die "$name: could not obtain a sha256 from any of: $*
  Refusing to install an unverified binary as root. Re-run with
  CFVPN_ALLOW_UNVERIFIED_DOWNLOADS=1 only after verifying the release by hand."
  fi
  cfvpn_verify_sha256 "$out" "$expected" "$name"
}

# cfvpn_strip_oci_reject <rules-file>
# Oracle Cloud's stock Ubuntu/Oracle Linux images ship an in-instance iptables
# ruleset (/etc/iptables/rules.v4, restored by netfilter-persistent at boot)
# that ends every chain with "-j REJECT --reject-with icmp-host-prohibited", so
# only :22 is reachable even after the VCN security list has been opened — the
# classic "opened the security list, 443 still dead" OCI trap. The node's
# ports (443, a random HY2 UDP port chosen later by cfvpnctl install) are
# protected by the VCN security list, which is the layer the operator already
# manages, so the redundant in-image REJECT lines are removed. Everything else
# in the file (SSH accept, established, loopback) is kept. Prints the number of
# lines removed; a file without the OCI signature is left untouched (prints 0).
cfvpn_strip_oci_reject() {
  local f="$1" n
  [ -f "$f" ] || { printf '0\n'; return 0; }
  n="$(grep -cE -- '-j REJECT --reject-with icmp6?-(host|adm)-prohibited' "$f" || true)"
  if [ "${n:-0}" -eq 0 ]; then
    printf '0\n'
    return 0
  fi
  cp -p -- "$f" "$f.cfvpn-orig.$(date -u +%Y%m%dT%H%M%SZ)"
  sed -i -E '/-j REJECT --reject-with icmp6?-(host|adm)-prohibited/d' "$f"
  printf '%s\n' "$n"
}

# cfvpn_is_oci [TAG_FILE] [VENDOR_FILE]
# True on an Oracle Cloud instance. OCI stamps chassis_asset_tag with
# "OracleCloud.com" and sys_vendor with "Oracle Corporation"; either signal is
# enough, and the match is case-insensitive because the exact spelling has
# changed between image generations. The paths are arguments so the tests can
# hand over temp files instead of the real /sys.
#
# This gate matters because the REJECT pattern cfvpn_strip_oci_reject deletes
# is NOT OCI-specific — "-j REJECT --reject-with icmp-host-prohibited" is the
# stock tail of any Red-Hat-style ruleset. Running the strip unconditionally
# would quietly delete a non-OCI node's own deliberate REJECT rules.
# shellcheck disable=SC2120  # production calls pass nothing; the tests pass temp files
cfvpn_is_oci() {
  local tag_file="${1:-/sys/class/dmi/id/chassis_asset_tag}"
  local vendor_file="${2:-/sys/class/dmi/id/sys_vendor}"
  local f v
  for f in "$tag_file" "$vendor_file"; do
    [ -r "$f" ] || continue
    v="$(tr '[:upper:]' '[:lower:]' < "$f" 2>/dev/null || true)"
    case "$v" in *oracle*) return 0 ;; esac
  done
  return 1
}

# cfvpn_oci_firewall_fix — on Oracle Cloud only: strip the image's blanket
# REJECT rules, apply the result, and hand the box over to ufw.
#
# CFVPN_FORCE_OCI=1 forces the OCI path and 0 forces the skip (tests, and the
# rare image whose DMI is unreadable); unset means "ask the DMI".
# CFVPN_KEEP_OCI_IPTABLES=1 keeps the image rules and the netfilter-persistent
# unit exactly as they are.
cfvpn_oci_firewall_fix() {
  local is_oci=0
  # shellcheck disable=SC2119  # cfvpn_is_oci takes no args here on purpose: real /sys paths
  case "${CFVPN_FORCE_OCI:-}" in
    1) is_oci=1 ;;
    0) is_oci=0 ;;
    *) cfvpn_is_oci && is_oci=1 ;;
  esac
  if [ "$is_oci" -ne 1 ]; then
    log "not an Oracle Cloud instance — leaving this node's iptables rules untouched"
    return 0
  fi
  if [ "${CFVPN_KEEP_OCI_IPTABLES:-0}" = "1" ]; then
    log "CFVPN_KEEP_OCI_IPTABLES=1 — leaving the image's iptables rules and netfilter-persistent alone"
    return 0
  fi
  local f removed total=0
  local changed=()
  for f in /etc/iptables/rules.v4 /etc/iptables/rules.v6; do
    removed="$(cfvpn_strip_oci_reject "$f")"
    total=$((total + removed))
    if [ "$removed" -gt 0 ]; then
      changed+=("$f")
      log "removed $removed blanket REJECT rule(s) from $f (Oracle Cloud image default; ports stay guarded by the VCN security list)"
    fi
  done
  if [ "$total" -gt 0 ]; then
    if command -v netfilter-persistent >/dev/null 2>&1; then
      netfilter-persistent reload >/dev/null 2>&1 || warn "netfilter-persistent reload failed; rules apply at next boot"
    else
      # Restore each ruleset with its own tool: piping rules.v6 through
      # iptables-restore is a parse error, and before this only rules.v4 was
      # ever re-applied, so an IPv6 client kept hitting the deleted REJECT
      # until the next reboot.
      for f in "${changed[@]}"; do
        case "$f" in
          *rules.v6)
            if command -v ip6tables-restore >/dev/null 2>&1; then
              ip6tables-restore < "$f" 2>/dev/null || warn "ip6tables-restore failed; rules apply at next boot"
            fi
            ;;
          *)
            iptables-restore < "$f" 2>/dev/null || warn "iptables-restore failed; rules apply at next boot"
            ;;
        esac
      done
    fi
    log "in-instance firewall now defers to the VCN security list — open 443/tcp and the HY2 UDP port there"
  fi
  # ufw is the only in-box firewall on this fleet. netfilter-persistent restores
  # the image's remaining rules (notably "--dport 22 ACCEPT") ahead of ufw's
  # chains at boot, so public :22 comes back open no matter what ufw says.
  # `systemctl cat`, not `list-unit-files`: the latter exits 0 even when nothing
  # matches the pattern, so it can never tell us the unit is absent.
  #
  # Only when ufw is already enforcing: stopping netfilter-persistent flushes
  # the chains it owns, so doing it on a box where ufw is not active would
  # leave the node with NO in-box firewall at all (policy ACCEPT) — worse than
  # the image default we just edited. With the blanket REJECT gone, the image's
  # remaining rules are harmless (policy ACCEPT, SSH accept), so keeping the
  # unit until ufw is up costs nothing.
  local ufw_status=""
  command -v ufw >/dev/null 2>&1 && ufw_status="$(ufw status 2>/dev/null || true)"
  case "$ufw_status" in
    *'Status: active'*) ;;
    *)
      if systemctl cat netfilter-persistent.service >/dev/null 2>&1; then
        warn "ufw is not active — keeping netfilter-persistent; enable ufw (fleet baseline: 17722/tcp, 443, the HY2 port) then re-run 'cfvpnctl reconcile-units' or disable netfilter-persistent by hand, or its rules will reload ahead of ufw at boot"
      fi
      return 0 ;;
  esac
  if systemctl cat netfilter-persistent.service >/dev/null 2>&1; then
    if systemctl disable --now netfilter-persistent >/dev/null 2>&1; then
      log "disabled netfilter-persistent — ufw is now the only in-box firewall"
      # Stopping the unit flushes the chains it owns, which can take ufw's
      # chains with it; re-assert them while we still have this session.
      ufw reload >/dev/null 2>&1 || warn "ufw reload failed after disabling netfilter-persistent — check 'ufw status' before disconnecting"
    else
      warn "could not disable netfilter-persistent; its rules will reload ahead of ufw at boot"
    fi
  fi
}
