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
# sha256 verification for downloaded binaries
# ---------------------------------------------------------------------------
cfvpn_sha256_file() {
  sha256sum "$1" | awk '{print tolower($1)}'
}

# cfvpn_extract_sha256 NAME  < checksum-file
# Understands the formats upstream projects actually publish:
#   "<hash>  filename"        (sha256sum / *.sha256sum / sha256sum.txt)
#   "<hash> *filename"        (sha256sum binary mode)
#   "<hash>"                  (bare single-hash *.sha256 file)
#   "SHA2-256= <hash>"        (openssl dgst style, e.g. Xray *.dgst)
#   "SHA256(filename)= <hash>"
# The filename is matched as a whole FIELD, not as a substring: a substring
# match would happily hand back the hash of hysteria-linux-amd64-avx for
# hysteria-linux-amd64. Otherwise a file containing exactly one hash is
# accepted; a multi-entry file with no match is a failure (never guess).
cfvpn_extract_sha256() {
  local want="$1" line tok hash matched lone="" lone_count=0
  while IFS= read -r line || [ -n "$line" ]; do
    hash=""; matched=0
    for tok in $line; do
      tok="${tok##*=}"
      tok="${tok##*\)}"
      if [ -z "$hash" ] && [[ "$tok" =~ ^[0-9a-fA-F]{64}$ ]]; then
        hash="${tok,,}"
        continue
      fi
      # sha256sum writes "<hash>  name" (text) or "<hash> *name" (binary);
      # some files carry a leading "./".
      tok="${tok#\*}"; tok="${tok#./}"
      # SHA256(name)= <hash> puts the name before the '=' we stripped above,
      # so compare against the basename in every position.
      [ -n "$want" ] && [ "$tok" = "$want" ] && matched=1
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
