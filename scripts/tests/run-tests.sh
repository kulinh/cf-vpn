#!/usr/bin/env bash
# run-tests.sh — unit tests + failure-mode simulations for scripts/.
#
#   bash scripts/tests/run-tests.sh
#
# Nothing here touches the network, SSH, D1 or /etc: `ssh` and `curl` are
# replaced by shell functions, and every path is under a temp dir.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LIB="$ROOT/scripts/lib"
TMPROOT="$(mktemp -d)"
trap 'rm -rf "$TMPROOT"' EXIT INT TERM

PASS=0; FAIL=0
ok()   { printf '  [ok]   %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '  [FAIL] %s\n' "$*" >&2; FAIL=$((FAIL+1)); }
is()   { # is <actual> <expected> <label>
  if [ "$1" = "$2" ]; then ok "$3"; else bad "$3: expected [$2] got [$1]"; fi
}
contains() { # contains <haystack> <needle> <label>
  case "$1" in *"$2"*) ok "$3" ;; *) bad "$3: [$2] not found in [$1]" ;; esac
}

section() { printf '\n== %s\n' "$*"; }

# The library under test — this is the REAL ssh_run, not a copy.
# shellcheck source=../lib/cfvpn-common.sh
. "$LIB/cfvpn-common.sh"

# `ssh` is shadowed by a function that emulates OpenSSH exactly: argv[1..]
# after the host are joined with SPACES into one string that the remote LOGIN
# SHELL re-parses. Local quoting is gone unless the caller re-quoted.
fake_ssh() {
  local args=() seen_host=0
  while [ $# -gt 0 ]; do
    case "$1" in
      -o|-i) shift 2 ;;
      -*)    shift ;;
      *)     if [ "$seen_host" -eq 0 ]; then seen_host=1; else args+=("$1"); fi; shift ;;
    esac
  done
  sh -c "${args[*]}"           # stdin (heredocs) passes through, as with ssh
}
ssh() { fake_ssh "$@"; }
# shellcheck disable=SC2034  # read by ssh_run() in the sourced library
TARGET_HOST="root@target"
_ssh_opts=(-o BatchMode=yes)

# ---------------------------------------------------------------------------
section "ssh_run — argv contract (scripts/lib/cfvpn-common.sh)"
# Arguments with spaces/quotes must arrive as ONE argument each, unmangled.
is "$(ssh_run printf '[%s]' 'a b' 'c"d' "e'f" 'g$h')" '[a b][c"d][e'"'"'f][g$h]' \
   "argv survives spaces, quotes and \$ intact"
# A whole shell snippet passed as ONE argument is a COMMAND NAME, not a script:
# this is the resolve_mode bug — it must fail loudly, not silently mean "no".
rc=0; ssh_run 'awk "NR>1 && \$4==\"0A\"{f=1} END{exit !f}" /proc/net/tcp' >/dev/null 2>&1 || rc=$?
is "$rc" "127" "a shell snippet as a single argv element fails with 127 (not a silent false)"
# The supported way to run a remote script: stdin.
is "$(ssh_run bash -s <<'REMOTE'
printf 'probe=%s\n' ok
REMOTE
)" "probe=ok" "remote scripts go on stdin via bash -s"

# ---------------------------------------------------------------------------
section "C3 — reading the target's env file back over ssh"
REMOTE_ENV_FILE="$TMPROOT/cfvpn.env"
cat >"$REMOTE_ENV_FILE" <<EOF
DOMAIN=vpn.rwl247.dev
HY2_PORT=34567
UUID_USER1=1f0b0e0e-0000-4000-8000-000000000001
EOF

# --- old form: ssh_run bash -c '<multiline>'  (the shipped bug) --------------
old_ssh_run() { fake_ssh -o BatchMode=yes root@target "$@"; }
old_out="$(old_ssh_run bash -c ". $REMOTE_ENV_FILE
printf \"DOMAIN=%s\n\" \"\$DOMAIN\"
printf \"HY2_PORT=%s\n\" \"\$HY2_PORT\"" 2>/dev/null)"
old_rc=$?
is "$old_rc" "0" "old form exits 0 (nothing detects the failure)"
is "$(printf '%s' "$old_out" | grep -c '^DOMAIN=vpn.rwl247.dev$')" "0" \
   "old form loses DOMAIN (comes back empty)"
is "$(printf '%s' "$old_out" | grep -c '^HY2_PORT=34567$')" "0" \
   "old form loses HY2_PORT (comes back empty)"

# --- new form: the real ssh_run + a quoted heredoc on stdin ------------------
new_out="$(ssh_run env "CFVPN_ENV_FILE=$REMOTE_ENV_FILE" bash -s <<'REMOTE'
set -euo pipefail
. "${CFVPN_ENV_FILE:-/etc/cfvpn/cfvpn.env}"
printf "DOMAIN=%s\n"     "${DOMAIN:-}"
printf "HY2_PORT=%s\n"   "${HY2_PORT:-}"
printf "UUID_USER1=%s\n" "${UUID_USER1:-}"
REMOTE
)"
contains "$new_out" "DOMAIN=vpn.rwl247.dev" "new form returns DOMAIN"
contains "$new_out" "HY2_PORT=34567"        "new form returns HY2_PORT"

# --- and an empty read must abort instead of writing a half-empty D1 row ----
empty_check() {
  # shellcheck disable=SC2034  # read indirectly via ${!_k}
  local DOMAIN="" HY2_PORT="" _k
  for _k in DOMAIN HY2_PORT; do
    [ -n "${!_k:-}" ] || { echo "die: $_k not populated"; return 1; }
  done
  echo "would write to D1"
}
out="$(empty_check)"; rc=$?
is "$rc" "1" "empty values abort the D1 step"
contains "$out" "die: DOMAIN not populated" "abort names the missing key"

# ---------------------------------------------------------------------------
section "M-S3 — pipefail + grep -q"
big_apt_output() {
  echo "Inst curl [7.88.1] (7.88.2 Debian:12/stable [amd64])"
  seq 1 200000 | sed 's/^/Conf pkg-/'
}
( set -euo pipefail
  if big_apt_output 2>/dev/null | grep -q "^Inst curl "; then
    echo "IF-TRUE"
  else
    echo "IF-FALSE (BUG: upgrade skipped)"
  fi ) >"$TMPROOT/old.txt" 2>/dev/null
is "$(cat "$TMPROOT/old.txt")" "IF-FALSE (BUG: upgrade skipped)" \
   "piping into grep -q under pipefail reports NO match (the bug)"

( set -euo pipefail
  plan="$(big_apt_output 2>/dev/null || true)"
  if grep -q "^Inst curl " <<<"$plan"; then
    echo "IF-TRUE"
  else
    echo "IF-FALSE"
  fi ) >"$TMPROOT/new.txt" 2>/dev/null
is "$(cat "$TMPROOT/new.txt")" "IF-TRUE" "capture-then-grep finds the match"

# ---------------------------------------------------------------------------
section "cfvpn-common.sh — env value safety"
for good in "abc123" "1.1.1.1:53,8.8.8.8:53" "vpn.rwl247.dev" "a-b_c" "deadBEEF00"; do
  if cfvpn_env_value_ok "$good"; then ok "accepts [$good]"; else bad "rejects [$good]"; fi
done
for evil in 'a b' 'a$(id)b' 'a`id`b' 'a"b' "a'b" 'a\b' 'a;b' 'a
b'; do
  if cfvpn_env_value_ok "$evil"; then bad "accepts unsafe [$evil]"; else ok "rejects unsafe value"; fi
done

section "cfvpn-common.sh — sha256 parsing"
h1=0000000000000000000000000000000000000000000000000000000000000001
h2=0000000000000000000000000000000000000000000000000000000000000002
is "$(printf '%s  jq-linux-amd64\n%s  jq-macos\n' "$h1" "$h2" | cfvpn_extract_sha256 jq-linux-amd64)" \
   "$h1" "sha256sum.txt: picks the line for the wanted file"
is "$(printf '%s\n' "$h1" | cfvpn_extract_sha256 anything)" "$h1" \
   "bare single-hash .sha256 file"
is "$(printf 'MD5= 0123456789abcdef0123456789abcdef\nSHA1= 0123456789abcdef0123456789abcdef01234567\nSHA2-256= %s\nSHA2-512= %s%s\n' "$h1" "$h1" "$h1" | cfvpn_extract_sha256 Xray-linux-64.zip)" \
   "$h1" "openssl .dgst style (only the 64-hex line qualifies)"
printf '%s  other-a\n%s  other-b\n' "$h1" "$h2" | cfvpn_extract_sha256 not-listed >/dev/null 2>&1
is "$?" "1" "multi-entry file with no match fails instead of guessing"
# A substring match would return the -avx hash for the plain binary.
is "$(printf '%s  hysteria-linux-amd64-avx\n%s  hysteria-linux-amd64\n' "$h2" "$h1" \
      | cfvpn_extract_sha256 hysteria-linux-amd64)" "$h1" \
   "matches the filename FIELD, not a substring (…-avx must not win)"
is "$(printf '%s *hysteria-linux-amd64\n' "$h1" | cfvpn_extract_sha256 hysteria-linux-amd64)" \
   "$h1" "sha256sum binary-mode '*name' prefix"

section "cfvpn-common.sh — sha256 verification"
echo hello >"$TMPROOT/payload"
good_hash="$(cfvpn_sha256_file "$TMPROOT/payload")"
( cfvpn_verify_sha256 "$TMPROOT/payload" "$good_hash" test-payload >/dev/null ) 2>&1
is "$?" "0" "matching sha256 passes"
out="$( ( cfvpn_verify_sha256 "$TMPROOT/payload" "$h1" test-payload ) 2>&1 )"; rc=$?
is "$rc" "1" "mismatching sha256 dies"
contains "$out" "sha256 MISMATCH" "mismatch message names the problem"

# ---------------------------------------------------------------------------
section "cfvpn-env-file.sh — C4 re-install guard"
ENVF="$TMPROOT/etc/cfvpn/cfvpn.env"
mkdir -p "$(dirname "$ENVF")"
helper() { CFVPN_ENV_FILE="$ENVF" bash "$LIB/cfvpn-env-file.sh" "$@"; }

# fresh host: check passes, write creates the file
rm -f "$ENVF"
helper check >/dev/null 2>&1
is "$?" "0" "check passes when no env file exists"
printf 'NODE_ID=chn-01\nMODE=direct\nAGENT_SHARED_SECRET=secret-one\n' | helper write >/dev/null
is "$(grep -c . "$ENVF")" "3" "write creates the env file"
is "$(stat -c '%a' "$ENVF")" "600" "env file is 0600"

# provisioned host: guard fires and names what would be lost
printf 'REALITY_PRIVATE_KEY=priv\nREALITY_PUBLIC_KEY=pub\nUUID_USER1=uuid-1\nAGENT_SHARED_SECRET=secret-one\n' >"$ENVF"
out="$(helper check 2>&1)"; rc=$?
is "$rc" "3" "check refuses to re-provision a live node"
contains "$out" "REALITY_PRIVATE_KEY" "refusal lists the Reality key"
contains "$out" "UUID_USER1"          "refusal lists the user UUID"
contains "$out" "cfvpnctl upgrade"    "refusal points at cfvpnctl upgrade"
out="$(printf 'NODE_ID=chn-01\n' | helper write 2>&1)"; rc=$?
is "$rc" "3" "write refuses too (not just check)"
is "$(grep -c '^REALITY_PRIVATE_KEY=priv$' "$ENVF")" "1" "refused write left the env file untouched"

# FORCE_REINSTALL=1: backup first, keep only what must not be regenerated
printf 'REALITY_PRIVATE_KEY=priv\nUUID_USER1=uuid-1\nAGENT_SHARED_SECRET=secret-one\nADMIN_TUNNEL_UUID=tunnel-old\nCF_ACCOUNT_ID=acct-1\n' >"$ENVF"
out="$(printf 'NODE_ID=chn-01\nAGENT_SHARED_SECRET=secret-two\n' | FORCE_REINSTALL=1 helper write 2>&1)"
is "$?" "0" "FORCE_REINSTALL=1 allows the rewrite"
contains "$out" "backed up" "forced rewrite reports the backup"
bak="$(ls "$(dirname "$ENVF")"/cfvpn.env.bak-* 2>/dev/null | head -1)"
[ -n "$bak" ] && ok "backup file created: $(basename "$bak")" || bad "no backup file created"
is "$(stat -c '%a' "$bak")" "600" "backup is 0600"
contains "$(cat "$bak")" "REALITY_PRIVATE_KEY=priv" "backup holds the old secrets"
is "$(grep -c '^REALITY_PRIVATE_KEY=' "$ENVF")" "0" "forced rewrite drops the stale Reality key"
is "$(grep -c '^UUID_USER1=' "$ENVF")" "0" "forced rewrite drops the stale user UUID"
is "$(grep -c '^AGENT_SHARED_SECRET=secret-two$' "$ENVF")" "1" "forced rewrite stores the new secret"
is "$(grep -c '^ADMIN_TUNNEL_UUID=tunnel-old$' "$ENVF")" "1" \
   "forced rewrite CARRIES OVER ADMIN_TUNNEL_UUID (else the old tunnel is orphaned)"
is "$(grep -c '^CF_ACCOUNT_ID=acct-1$' "$ENVF")" "1" "forced rewrite carries over the CF credentials"

# unforced re-run over a partial env file preserves keys we do not write
printf 'NODE_ID=old\nUUID_USER1=uuid-keep\nADMIN_TUNNEL_UUID=tunnel-keep\n' >"$ENVF"
printf 'NODE_ID=chn-01\nMODE=direct\n' | FORCE_REINSTALL=1 helper write >/dev/null
is "$(grep -c '^UUID_USER1=uuid-keep$' "$ENVF")" "0" "forced: user UUID is not preserved"
printf 'NODE_ID=old\nUUID_USER1=uuid-keep\nSOME_OPERATOR_KEY=keep-me\n' >"$ENVF"
printf 'NODE_ID=chn-01\nMODE=direct\n' | helper write >/dev/null
is "$(grep -c '^UUID_USER1=uuid-keep$' "$ENVF")" "1" "preserves keys the installer does not write"
is "$(grep -c '^SOME_OPERATOR_KEY=keep-me$' "$ENVF")" "1" "preserves operator-supplied keys"
is "$(grep -c '^NODE_ID=' "$ENVF")" "1" "overridden key is written exactly once"
is "$(grep -c '^NODE_ID=chn-01$' "$ENVF")" "1" "overridden key has the new value"

# --- the guard must NOT fire on a run that died before cfvpnctl install -----
# install-node.sh writes AGENT_SHARED_SECRET *before* calling cfvpnctl, so
# keying the guard on it would make every retry after a failed apt / CF error /
# unit-gate abort impossible.
rm -f "$(dirname "$ENVF")/.installed"
printf 'CF_API_TOKEN=tok\nNODE_ID=chn-01\nAGENT_SHARED_SECRET=secret-one\n' >"$ENVF"
helper check >/dev/null 2>&1
is "$?" "0" "a partial install (env written, cfvpnctl never ran) is retryable"
printf 'NODE_ID=chn-01\nAGENT_SHARED_SECRET=secret-three\n' | helper write >/dev/null
is "$?" "0" "…and the retry may rewrite the env file"

# mark-installed is what arms the guard
helper mark-installed >/dev/null
is "$(stat -c '%a' "$(dirname "$ENVF")/.installed")" "600" ".installed marker is 0600"
out="$(helper check 2>&1)"; rc=$?
is "$rc" "3" "guard fires once the install is marked complete"
contains "$out" "already provisioned" "refusal says the node is provisioned"
# a forced re-provision disarms it again until the new install succeeds
printf 'NODE_ID=chn-01\n' | FORCE_REINSTALL=1 helper write >/dev/null
is "$([ -e "$(dirname "$ENVF")/.installed" ] && echo present || echo gone)" "gone" \
   "forced re-provision clears the marker"

# ---------------------------------------------------------------------------
section "H14 — writing the target env file without a remote shell expansion"
# The old CN step was `ssh_run bash -s <<REMOTE` (UNQUOTED delimiter), so the
# token was expanded LOCALLY into a script the remote root shell then parsed.
EVIL='x"; touch '"$TMPROOT/pwned"'; echo "'
(
  CF_API_TOKEN="$EVIL"
  fake_ssh -o BatchMode=yes root@target bash -s <<REMOTE
printf 'CF_API_TOKEN=%s\n' "${CF_API_TOKEN}"
REMOTE
) >/dev/null 2>&1
if [ -f "$TMPROOT/pwned" ]; then
  ok "old unquoted heredoc executes injected commands as root (reproduced)"
else
  bad "could not reproduce the H14 injection"
fi

# New path: such a value never reaches the wire at all.
out="$( ( cfvpn_require_env_value CF_API_TOKEN "$EVIL" ) 2>&1 )"; rc=$?
is "$rc" "1" "an injectable CF_API_TOKEN is rejected before any ssh"
contains "$out" "cannot be written" "rejection explains why"

# And a legitimate value is transported verbatim over stdin, with the guard,
# backup and merge running on the far side.
ENVF2="$TMPROOT/remote-etc/cfvpn.env"
mkdir -p "$(dirname "$ENVF2")"
{
  printf 'CF_API_TOKEN=%s\n' "tok-en_123"
  printf 'NODE_ID=%s\n'      "chn-02"
} | ssh_run env "CFVPN_ENV_FILE=$ENVF2" FORCE_REINSTALL=0 bash "$LIB/cfvpn-env-file.sh" write >/dev/null
is "$(grep -c '^CF_API_TOKEN=tok-en_123$' "$ENVF2")" "1" "env payload arrives verbatim over ssh stdin"
is "$(stat -c '%a' "$ENVF2")" "600" "remote env file is 0600"

# ---------------------------------------------------------------------------
section "cfvpn-d1.sh — d1_query always returns JSON (M-S4)"
# shellcheck disable=SC2034  # read by d1_query() in the sourced library
CF_API_TOKEN=t CF_ACCOUNT_ID=a D1_DB_ID=d
# shellcheck source=../lib/cfvpn-d1.sh
. "$LIB/cfvpn-d1.sh"

curl() { cat >/dev/null; printf '<html>502 Bad Gateway</html>'; return 22; }   # CF error page
out="$(d1_query '{"sql":"SELECT 1"}' 2>/dev/null)"; rc=$?
is "$rc" "0" "d1_query returns 0 so warn-and-continue paths are reachable"
is "$(printf '%s' "$out" | jq -r '.success')" "false" "non-JSON body becomes success:false"
is "$(printf '%s' "$out" | jq -r '.errors[0].message')" "curl failed" "error message is usable by callers"

curl() { cat >/dev/null; return 7; }                                          # no response at all
out="$(d1_query '{"sql":"SELECT 1"}' 2>/dev/null)"
is "$(printf '%s' "$out" | jq -r '.errors[0].message')" "curl failed" "empty body becomes success:false"

curl() { cat >/dev/null; printf '{"success":false,"errors":[{"code":7500,"message":"no such table: nodes"}]}'; return 22; }
out="$(d1_query '{"sql":"SELECT 1"}' 2>/dev/null)"
is "$(printf '%s' "$out" | jq -r '.errors[0].message')" "no such table: nodes" \
   "a real D1 error body survives (not masked as 'curl failed')"
unset -f curl

section "cfvpn-d1.sh — zone derivation (M-S10)"
zones_json() {
  printf '{"success":true,"result":[{"results":['
  printf '{"name":"rwl247.dev"},{"name":"example.co.uk"},{"name":"co.uk"},{"name":"888vn.net"}'
  printf ']}]}'
}
d1_query() { zones_json; }
is "$(d1_zone_for_domain vpn.example.co.uk)" "example.co.uk" "longest matching zone wins over co.uk"
is "$(d1_zone_for_domain chn-01.rwl247.dev)" "rwl247.dev"    "plain two-label zone still works"
is "$(d1_zone_for_domain rwl247.dev)"        "rwl247.dev"    "domain equal to the zone"
d1_query() { printf '{"success":false,"errors":[{"message":"curl failed"}]}'; }
is "$(d1_zone_for_domain vpn.example.co.uk 2>/dev/null)" "co.uk" \
   "falls back to the old heuristic when D1 is unreachable"
contains "$(d1_zone_for_domain vpn.example.co.uk 2>&1 >/dev/null)" "falling back" \
   "fallback warns on stderr"

# ---------------------------------------------------------------------------
printf '\n--------------------------------------------\n'
printf 'scripts/tests: pass=%d fail=%d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
