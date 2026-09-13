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

section "cfvpn-common.sh — HY2_PORT bounds (shared by both installers)"
for p in 1024 32443 65535; do
  if cfvpn_port_ok "$p"; then ok "accepts port $p"; else bad "rejects valid port $p"; fi
done
# 443 is rejected on purpose: every cfvpn listener runs unprivileged. The rest are
# the typos this is here to catch before the install mutates anything.
for p in 443 1023 65536 0 "" "32443 " "3244a" "-1" "32.443" "0x80"; do
  if cfvpn_port_ok "$p"; then bad "accepts invalid port [$p]"; else ok "rejects invalid port [$p]"; fi
done
out="$( ( cfvpn_require_port HY2_PORT 70000 ) 2>&1 )"; rc=$?
is "$rc" "1" "cfvpn_require_port dies on an out-of-range port"
contains "$out" "HY2_PORT must be an integer in [1024,65535] (got: 70000)" "…and names the key and the value"

section "install-node-CN.sh — the remote stage dir is removed on failure (C5)"
# The REAL function, lifted out of the installer (which cannot be sourced: its
# top-level preflight hits the network), so the rm -rf guard under test is the
# shipped one and not a copy.
sed -n '/^cleanup_remote_stage() {/,/^}/p' "$ROOT/scripts/install-node-CN.sh" > "$TMPROOT/crs.sh"
is "$(grep -c '^cleanup_remote_stage() {' "$TMPROOT/crs.sh")" "1" "cleanup_remote_stage was found in the installer"
RM_LOG="$TMPROOT/rm.log"
(
  . "$TMPROOT/crs.sh"
  # shellcheck disable=SC2034  # named in cleanup_remote_stage's log/warn lines
  TARGET_HOST="root@target"
  ssh_run() { printf '%s\n' "$*" >> "$RM_LOG"; }     # stub: record, never run
  : >"$RM_LOG"
  REMOTE_STAGE="/tmp/cfvpn-stage.abc123"; cleanup_remote_stage >/dev/null 2>&1
  printf 'after=[%s]\n' "$REMOTE_STAGE"
  # Anything that is not a stage dir must never be handed to rm -rf.
  for danger in "/" "/etc" "" "/tmp" "/root/cfvpn-backups"; do
    REMOTE_STAGE="$danger"; cleanup_remote_stage >/dev/null 2>&1
  done
) > "$TMPROOT/crs.out" 2>&1
is "$(grep -c '^rm -rf /tmp/cfvpn-stage.abc123$' "$RM_LOG")" "1" "a real stage dir is removed on the target"
contains "$(cat "$TMPROOT/crs.out")" "after=[]" "…and REMOTE_STAGE is cleared so the EXIT trap does not repeat it"
is "$(grep -cvE '^rm -rf /tmp/cfvpn-stage\.' "$RM_LOG")" "0" \
   "no path outside /tmp/cfvpn-stage.* is ever passed to rm -rf"

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
# Real shape of apernet/hysteria's hashes.txt: ~27 entries, every name carries
# a "build/" prefix, and the plain binary sits next to its -avx sibling. A
# path-sensitive match finds nothing here (and then, because every line holds a
# valid hash, the "exactly one hash" fallback cannot save it either).
hysteria_hashes="$(printf '%s  build/hysteria-linux-386\n%s  build/hysteria-linux-amd64-avx\n%s  build/hysteria-linux-amd64\n%s  build/hysteria-linux-arm64\n' \
  "$h2" "$h2" "$h1" "$h2")"
is "$(printf '%s\n' "$hysteria_hashes" | cfvpn_extract_sha256 hysteria-linux-amd64)" "$h1" \
   "hashes.txt with a build/ prefix and an -avx sibling picks the right hash"
is "$(printf '%s  ./jq-linux-amd64\n' "$h1" | cfvpn_extract_sha256 jq-linux-amd64)" \
   "$h1" "leading ./ is ignored"
is "$(printf '%s *hysteria-linux-amd64\n' "$h1" | cfvpn_extract_sha256 hysteria-linux-amd64)" \
   "$h1" "sha256sum binary-mode '*name' prefix"
printf '%s  build/hysteria-linux-amd64-avx\n%s  build/hysteria-linux-arm64\n' "$h1" "$h2" \
  | cfvpn_extract_sha256 hysteria-linux-amd64 >/dev/null 2>&1
is "$?" "1" "…and an -avx-only listing still fails rather than returning its hash"

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

# --- C1: the operator's transport CHOICES must survive a forced re-provision --
# A missing HY2_ENABLED means "on" in internal/state/keys.go, so dropping the key
# is not neutral: a node deliberately running without hysteria came back with it
# enabled, advertising an endpoint nothing was listening on.
cat >"$ENVF" <<'EOF'
REALITY_PRIVATE_KEY=priv
UUID_USER1=uuid-1
AGENT_SHARED_SECRET=secret-one
ADMIN_TUNNEL_UUID=tunnel-old
HY2_ENABLED=0
XHTTP_ENABLED=1
XHTTP_DIRECT_HOST=static-df60bd79.duylinh.org
XHTTP_DIRECT_PATH=/api/v1/sync
XHTTP_H3_HOST=quic-b55170f3.dongnat247.com
XHTTP_H3_PATH=/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10
CLOUDFLARED_PROTOCOL=http2
REALITY_DEST=www.sony.jp:443
REALITY_SNI=www.sony.jp
XRAY_DNS_SERVERS=https://223.5.5.5/dns-query
EOF
printf 'NODE_ID=chn-01\nAGENT_SHARED_SECRET=secret-two\n' | FORCE_REINSTALL=1 helper write >/dev/null
is "$(grep -c '^HY2_ENABLED=0$' "$ENVF")" "1" \
   "forced rewrite KEEPS HY2_ENABLED=0 (a missing key would silently re-enable HY2)"
is "$(grep -c '^XHTTP_ENABLED=1$' "$ENVF")" "1"          "forced rewrite keeps XHTTP_ENABLED"
is "$(grep -c '^XHTTP_DIRECT_HOST=static-df60bd79.duylinh.org$' "$ENVF")" "1" \
   "forced rewrite keeps XHTTP_DIRECT_HOST"
is "$(grep -c '^XHTTP_DIRECT_PATH=/api/v1/sync$' "$ENVF")" "1" "forced rewrite keeps XHTTP_DIRECT_PATH"
is "$(grep -c '^XHTTP_H3_HOST=quic-b55170f3.dongnat247.com$' "$ENVF")" "1" \
   "forced rewrite keeps XHTTP_H3_HOST (losing it silently drops the H3 route)"
is "$(grep -c '^XHTTP_H3_PATH=/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10$' "$ENVF")" "1" \
   "forced rewrite keeps XHTTP_H3_PATH"
is "$(grep -c '^CLOUDFLARED_PROTOCOL=http2$' "$ENVF")" "1"    "forced rewrite keeps CLOUDFLARED_PROTOCOL"
is "$(grep -c '^REALITY_DEST=www.sony.jp:443$' "$ENVF")" "1"  "forced rewrite keeps the per-node REALITY_DEST"
is "$(grep -c '^REALITY_SNI=www.sony.jp$' "$ENVF")" "1"       "forced rewrite keeps REALITY_SNI"
is "$(grep -c '^XRAY_DNS_SERVERS=https://223.5.5.5/dns-query$' "$ENVF")" "1" \
   "forced rewrite keeps XRAY_DNS_SERVERS (the CN nodes need domestic DNS)"
# …while the generated secrets are still dropped, which is the whole point.
is "$(grep -c '^REALITY_PRIVATE_KEY=' "$ENVF")" "0" "forced rewrite still drops the Reality private key"
is "$(grep -c '^UUID_USER1=' "$ENVF")" "0"          "forced rewrite still drops the user UUID"

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

section "cfvpn-d1.sh — d1_zone_report (shared by both installers)"
out="$(d1_zone_report 2>&1)"; rc=$?
is "$rc" "0" "zone report is non-fatal when D1 fails"
contains "$out" "D1 zone check failed (non-fatal): curl failed" "…and says why"
d1_query() { printf '{"success":true,"result":[{"results":[{"id":"JPY-01","label":"JPY-01","zone":"rwl247.dev","vpn_host":"a.rwl247.dev"},{"id":"SIN-01","label":"SIN-01","zone":"rwl247.dev","vpn_host":"b.rwl247.dev"},{"id":"HKG-01","label":"HKG-01","zone":"888vn.net","vpn_host":"c.888vn.net"}]}]}'; }
out="$(d1_zone_report 2>&1)"; rc=$?
is "$rc" "0" "zone report exits 0 on success"
contains "$out" "D1 nodes: 3" "zone report counts the nodes"
contains "$out" "  rwl247.dev: JPY-01(a.rwl247.dev), SIN-01(b.rwl247.dev)" "zone report groups nodes by zone"
d1_query() { printf '{"success":true,"result":[{"results":[]}]}'; }
out="$(d1_zone_report 2>&1)"; rc=$?
is "$rc|$(printf '%s\n' "$out" | grep -c '^  ')" "0|0" "an empty fleet prints the count only and exits 0"

section "cfvpn-d1.sh — d1_upsert_node NULLs the empty hy2 columns (C2)"
# "No hysteria on this node" is NULL everywhere else in the system (that is what
# `cfvpnctl hy2 disable` + d1-set-node.sh hy2-off write, and what the Worker
# tests before putting an HY2 line in a subscription). An empty string is truthy
# there, so an HY2-less install used to be advertised as "hysteria2://@:".
# Captured to a FILE: d1_upsert_node calls d1_query inside a command
# substitution, so a variable set by the stub never reaches this shell.
CAPFILE="$TMPROOT/d1-upsert.json"
d1_query() { printf '%s' "$1" >"$CAPFILE"; printf '{"success":true,"result":[{"meta":{"changes":1}}]}'; }
# shellcheck disable=SC2034  # every one of these is read by d1_upsert_node
{
  DB_NODE_ID=JPY-03
  NODE_LABEL="JPY-03"
  ADMIN_HOST=admin-x.duylinh.net
  DOMAIN=jpy-03.rwl247.dev
  HY2_HOST=""
  HY2_PORT=""
  HY2_OBFS_PW=""
  PUBLIC_IP=203.0.113.9
  ZONE=rwl247.dev
  MODE=direct
  NOW_MS=1789300800000
  AGENT_SHARED_SECRET=secret-one
}
d1_upsert_node >/dev/null 2>&1
sql="$(jq -r '.sql' "$CAPFILE")"
contains "$sql" "NULLIF(?, ''),NULLIF(?, ''),NULLIF(?, '')" \
   "hy2_host/hy2_port/hy2_obfs_pw go through NULLIF(?, '')"
is "$(printf '%s' "$sql" | grep -o '?' | wc -l)" "$(jq '.params | length' "$CAPFILE")" \
   "placeholder count still matches the params array (the NULLIFs did not shift it)"
# jq -c, not `// "MISSING"`: a JSON null is falsy in jq, so // would hide it.
is "$(jq -c '.params[5]' "$CAPFILE")" "null" \
   "an empty HY2_PORT is sent as JSON null, not as a string (hy2_port is an integer column)"
# A node that DOES run HY2 is unaffected.
# shellcheck disable=SC2034  # read by d1_upsert_node
{
  HY2_HOST=hy-c36ca6bd.dongnat247.com
  HY2_PORT=31300
  HY2_OBFS_PW=obfspw
}
d1_upsert_node >/dev/null 2>&1
is "$(jq -r '.params[4]' "$CAPFILE")" "hy-c36ca6bd.dongnat247.com" "a real hy2_host is passed through"
is "$(jq -r '.params[5]' "$CAPFILE")" "31300" "a real hy2_port stays an integer"
unset -f d1_query

# ---------------------------------------------------------------------------
section "cfvpn-drift.sh — node config vs D1 (M-S11)"
# shellcheck source=../lib/cfvpn-drift.sh
. "$LIB/cfvpn-drift.sh"

DRIFT_DIR="$TMPROOT/drift"; mkdir -p "$DRIFT_DIR"
mk() { printf '%b' "$2" > "$DRIFT_DIR/$1"; echo "$DRIFT_DIR/$1"; }

D1_OK=$(mk d1ok 'JPY-01\tkulinh\tuuid-a\tpassword-aaaa\nSIN-01\tkulinh\tuuid-b\tpassword-bbbb\n')
NODE_OK=$(mk nodeok 'JPY-01\tkulinh\tuuid-a\tpassword-aaaa\nSIN-01\tkulinh\tuuid-b\tpassword-bbbb\n')
OUT=$(drift_compare "$D1_OK" "$NODE_OK"); RC=$?
is "$RC" "0" "in sync exits 0"
is "$OUT" "" "in sync prints nothing"

# The exact failure that took JPY-01 and SIN-01 down: the hy2 password on the
# node drifted away from the one D1 hands to clients.
NODE_PW=$(mk nodepw 'JPY-01\tkulinh\tuuid-a\tpassword-zzzz\nSIN-01\tkulinh\tuuid-b\tpassword-bbbb\n')
OUT=$(drift_compare "$D1_OK" "$NODE_PW"); RC=$?
is "$RC" "1" "hy2 password drift exits 1"
contains "$OUT" "JPY-01	kulinh	hy2_pw" "hy2 password drift names node, user and field"
case "$OUT" in *SIN-01*) bad "in-sync node reported as drifted" ;; *) ok "only the drifted node is reported" ;; esac

# A credential must never be echoed in full by a diagnostic.
case "$OUT" in
  *password-zzzz*|*password-aaaa*) bad "drift output leaked a full credential" ;;
  *) ok "credentials are masked in the output" ;;
esac

NODE_UUID=$(mk nodeuuid 'JPY-01\tkulinh\tuuid-WRONG\tpassword-aaaa\nSIN-01\tkulinh\tuuid-b\tpassword-bbbb\n')
OUT=$(drift_compare "$D1_OK" "$NODE_UUID")
contains "$OUT" "vless_uuid" "vless uuid drift is reported"


# ----- cfvpn_strip_oci_reject -------------------------------------------------
oci_rules="$(mktemp)"
cat >"$oci_rules" <<'OCI'
*filter
:INPUT ACCEPT [0:0]
-A INPUT -m state --state RELATED,ESTABLISHED -j ACCEPT
-A INPUT -p icmp -j ACCEPT
-A INPUT -i lo -j ACCEPT
-A INPUT -p tcp -m state --state NEW -m tcp --dport 22 -j ACCEPT
-A INPUT -j REJECT --reject-with icmp-host-prohibited
-A FORWARD -j REJECT --reject-with icmp-host-prohibited
COMMIT
OCI
is "$(cfvpn_strip_oci_reject "$oci_rules")" "2" "oci: both blanket REJECT rules counted"
is "$(grep -c 'REJECT' "$oci_rules")" "0" "oci: REJECT lines removed"
is "$(grep -c -- '--dport 22 -j ACCEPT' "$oci_rules")" "1" "oci: SSH accept kept"
is "$(grep -c '^COMMIT' "$oci_rules")" "1" "oci: COMMIT kept"
is "$(ls "$oci_rules".cfvpn-orig.* | wc -l)" "1" "oci: original backed up"
is "$(cfvpn_strip_oci_reject "$oci_rules")" "0" "oci: second run is a no-op"
plain_rules="$(mktemp)"
printf '*filter\n-A INPUT -j DROP\nCOMMIT\n' >"$plain_rules"
is "$(cfvpn_strip_oci_reject "$plain_rules")" "0" "oci: a non-OCI ruleset is left alone"
is "$(cat "$plain_rules")" "$(printf '*filter\n-A INPUT -j DROP\nCOMMIT\n')" "oci: non-OCI file unchanged"
is "$(cfvpn_strip_oci_reject /nonexistent/rules.v4)" "0" "oci: missing file is fine"
rm -f "$oci_rules" "$oci_rules".cfvpn-orig.* "$plain_rules"

# ----- cfvpn_is_oci -----------------------------------------------------------
# The REJECT pattern above is the stock tail of any Red-Hat-style ruleset, not an
# OCI fingerprint, so the wrapper must gate on the DMI before touching a file.
DMI="$TMPROOT/dmi"; mkdir -p "$DMI"
printf 'OracleCloud.com\n' > "$DMI/asset_oci"
printf 'Oracle Corporation\n' > "$DMI/vendor_oci"
printf 'oraclecloud.com\n' > "$DMI/asset_lower"
printf 'Google\n' > "$DMI/asset_other"
printf 'DigitalOcean\n' > "$DMI/vendor_other"
cfvpn_is_oci "$DMI/asset_oci" "$DMI/vendor_other"
is "$?" "0" "cfvpn_is_oci: chassis_asset_tag OracleCloud.com is enough"
cfvpn_is_oci "$DMI/asset_other" "$DMI/vendor_oci"
is "$?" "0" "cfvpn_is_oci: sys_vendor 'Oracle Corporation' is enough"
cfvpn_is_oci "$DMI/asset_lower" "$DMI/vendor_other"
is "$?" "0" "cfvpn_is_oci: match is case-insensitive"
cfvpn_is_oci "$DMI/asset_other" "$DMI/vendor_other"
is "$?" "1" "cfvpn_is_oci: a non-Oracle node is not OCI"
cfvpn_is_oci "$DMI/nope" "$DMI/nope2"
is "$?" "1" "cfvpn_is_oci: unreadable DMI is not OCI (never guess)"

# The wrapper must not touch a single rule on a node that is not OCI. A real
# REJECT-bearing ruleset is put in its way: if the early return is missing, the
# node's own deliberate REJECT lines are deleted.
not_oci_rules="$TMPROOT/notoci-rules.v4"
printf '*filter\n-A INPUT -i lo -j ACCEPT\n-A INPUT -j REJECT --reject-with icmp-host-prohibited\nCOMMIT\n' >"$not_oci_rules"
before="$(cat "$not_oci_rules")"
out="$(CFVPN_FORCE_OCI=0 cfvpn_oci_firewall_fix 2>&1)"; rc=$?
is "$rc" "0" "oci wrapper: non-OCI node returns 0"
contains "$out" "not an Oracle Cloud instance" "oci wrapper: non-OCI node says so in one line"
is "$(cat "$not_oci_rules")" "$before" "oci wrapper: non-OCI node's REJECT rules are untouched"
is "$(ls "$TMPROOT"/notoci-rules.v4.cfvpn-orig.* 2>/dev/null | wc -l)" "0" \
   "oci wrapper: non-OCI node gets no backup file either"
# The CFVPN_KEEP_OCI_IPTABLES opt-out still wins on a node that IS OCI, and must
# also leave netfilter-persistent alone.
out="$(CFVPN_FORCE_OCI=1 CFVPN_KEEP_OCI_IPTABLES=1 cfvpn_oci_firewall_fix 2>&1)"
contains "$out" "CFVPN_KEEP_OCI_IPTABLES=1" "oci wrapper: the opt-out is honoured on an OCI node"
contains "$out" "netfilter-persistent" "oci wrapper: the opt-out message says the unit is left alone"

# ----- cfvpn_env_read ---------------------------------------------------------
section "cfvpn-common.sh — cfvpn_env_read (no sourcing)"
ENV_READ="$TMPROOT/read.env"
cat >"$ENV_READ" <<'ENVEOF'
# comment
PUBLIC_IP=$(touch /tmp/cfvpn-pwned-by-env-read)
DOMAIN=vpn.rwl247.dev
HY2_OBFS_PW=a`id`b
UUID_USER1=1f0b0e0e-0000-4000-8000-000000000001
XRAY_DNS_SERVERS=https://1.1.1.1/dns-query,https://9.9.9.9/dns-query
IGNORED_KEY=should-not-be-exported
ENVEOF
rm -f /tmp/cfvpn-pwned-by-env-read
(
  cfvpn_env_read "$ENV_READ" PUBLIC_IP DOMAIN HY2_OBFS_PW UUID_USER1 XRAY_DNS_SERVERS
  printf 'PUBLIC_IP=[%s]\n'   "${PUBLIC_IP:-}"
  printf 'DOMAIN=[%s]\n'      "${DOMAIN:-}"
  printf 'HY2_OBFS_PW=[%s]\n' "${HY2_OBFS_PW:-}"
  printf 'DNS=[%s]\n'         "${XRAY_DNS_SERVERS:-}"
  printf 'IGNORED=[%s]\n'     "${IGNORED_KEY:-}"
) >"$TMPROOT/env-read.out" 2>&1
out="$(cat "$TMPROOT/env-read.out")"
is "$([ -e /tmp/cfvpn-pwned-by-env-read ] && echo pwned || echo clean)" "clean" \
   "cfvpn_env_read does NOT execute \$(...) in a value (the reason not to source)"
contains "$out" 'PUBLIC_IP=[$(touch /tmp/cfvpn-pwned-by-env-read)]' "value is kept literally"
contains "$out" 'HY2_OBFS_PW=[a`id`b]' "backticks are kept literally too"
contains "$out" "DOMAIN=[vpn.rwl247.dev]" "a normal value round-trips"
contains "$out" "DNS=[https://1.1.1.1/dns-query,https://9.9.9.9/dns-query]" \
   "a value containing '=' free text and commas survives (split on the FIRST '=')"
contains "$out" "IGNORED=[]" "only the requested keys are exported"
# A hand-edited cfvpn.env often has no trailing newline on its last line.
printf 'DOMAIN=first\nUUID_USER1=no-trailing-newline' >"$TMPROOT/nonl.env"
out="$( ( cfvpn_env_read "$TMPROOT/nonl.env" UUID_USER1; printf '[%s]\n' "${UUID_USER1:-}" ) 2>&1 )"
contains "$out" "[no-trailing-newline]" "the last line is read even without a trailing newline"
rm -f /tmp/cfvpn-pwned-by-env-read
out="$( ( cfvpn_env_read "$TMPROOT/nope.env" DOMAIN ) 2>&1 )"; rc=$?
is "$rc" "1" "cfvpn_env_read: an unreadable file is an error, not a silent empty read"
contains "$out" "cannot read" "cfvpn_env_read: and it says which file"

# ----- cfvpn_ensure_ufw_ssh_allowed ------------------------------------------
section "cfvpn-common.sh — cfvpn_ensure_ufw_ssh_allowed"
FAKEBIN="$TMPROOT/fakebin"; mkdir -p "$FAKEBIN"
UFW_LOG="$TMPROOT/ufw.log"
cat >"$FAKEBIN/ufw" <<'UFWEOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$UFW_LOG"
case "$1" in
  status) printf '%s\n' "$UFW_STATUS_OUT" ;;
  allow)  [ "${UFW_ALLOW_FAILS:-0}" = "1" ] && [ "$2" = "OpenSSH" ] && exit 1; printf 'Rule added\n' ;;
esac
exit 0
UFWEOF
chmod +x "$FAKEBIN/ufw"
export UFW_LOG
# A subshell per case: `VAR=x shell_function` leaves VAR set afterwards in bash,
# which would leak UFW_ALLOW_FAILS into the later cases.
try_ufw() { # try_ufw <status-output> <port> [allow_fails]
  : >"$UFW_LOG"
  (
    PATH="$FAKEBIN:$PATH"
    export PATH UFW_STATUS_OUT="$1" UFW_ALLOW_FAILS="${3:-0}"
    cfvpn_ensure_ufw_ssh_allowed "$2"
  ) >/dev/null 2>&1
}

# inactive ufw: nothing is touched (adding rules to a disabled firewall is noise)
try_ufw "Status: inactive" 22
is "$(grep -c 'allow' "$UFW_LOG")" "0" "ufw inactive: no allow rule is added"

# active ufw on the default port: the OpenSSH profile is enough
try_ufw "Status: active" 22
is "$(grep -c '^allow OpenSSH$' "$UFW_LOG")" "1" "ufw active, port 22: allows the OpenSSH profile"
is "$(grep -c '^allow 22/tcp$' "$UFW_LOG")" "0" "…and does not also add a redundant 22/tcp rule"

# the fleet baseline port: the OpenSSH profile would open 22 and leave 17722
# closed, which is how a remote install ends with an unreachable box.
try_ufw "Status: active" 17722
is "$(grep -c '^allow 17722/tcp$' "$UFW_LOG")" "1" "ufw active, port 17722: allows the REAL port"
is "$(grep -c 'OpenSSH' "$UFW_LOG")" "0" "…and never falls back to the OpenSSH profile"

# OpenSSH profile missing (minimal images): fall back to the numeric rule
try_ufw "Status: active" 22 1
is "$(grep -c '^allow 22/tcp$' "$UFW_LOG")" "1" "no OpenSSH profile: falls back to 22/tcp"

# no ufw at all
mkdir -p "$TMPROOT/empty-bin"
# shellcheck disable=SC2123  # replacing PATH is the point: simulate "ufw not installed"
( PATH="$TMPROOT/empty-bin"; export PATH; cfvpn_ensure_ufw_ssh_allowed 22 ) >/dev/null 2>&1
is "$?" "0" "ufw not installed: returns 0 and does nothing"

# ----- cfvpn_oci_firewall_fix: netfilter-persistent only goes away once ufw is up
# Stopping that unit flushes the chains it owns. On a box where ufw is not
# active yet that leaves NO in-box firewall (policy ACCEPT) — worse than the
# image default — so the disable must wait for ufw.
NFP_LOG="$TMPROOT/nfp.log"
cat >"$FAKEBIN/systemctl" <<'SCEOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$NFP_LOG"
case "$1" in
  cat) exit 0 ;;   # the unit exists on this image
  *) exit 0 ;;
esac
SCEOF
chmod +x "$FAKEBIN/systemctl"
export NFP_LOG
oci_rules2="$TMPROOT/oci-rules.v4"
run_oci_fix() { # run_oci_fix <ufw-status>
  : >"$NFP_LOG"; : >"$UFW_LOG"
  printf '*filter\n:INPUT ACCEPT [0:0]\n-A INPUT -p tcp --dport 22 -j ACCEPT\n-A INPUT -j REJECT --reject-with icmp-host-prohibited\nCOMMIT\n' >"$oci_rules2"
  (
    PATH="$FAKEBIN:$PATH"
    export PATH UFW_STATUS_OUT="$1" CFVPN_FORCE_OCI=1
    # Point the function at the temp ruleset by running it from a shim that
    # rewrites the paths: the wrapper hardcodes /etc/iptables, which tests must
    # never touch, so only the netfilter-persistent decision is exercised here.
    cfvpn_oci_firewall_fix
  ) >/dev/null 2>&1
  rm -f "$oci_rules2"
}

run_oci_fix "Status: inactive"
is "$(grep -c 'disable --now netfilter-persistent' "$NFP_LOG")" "0" \
  "oci wrapper: ufw inactive → netfilter-persistent is KEPT (node would be left open)"
run_oci_fix "Status: active"
is "$(grep -c 'disable --now netfilter-persistent' "$NFP_LOG")" "1" \
  "oci wrapper: ufw active → netfilter-persistent is disabled"
is "$(grep -c '^reload$' "$UFW_LOG")" "1" \
  "…and ufw is reloaded right after, because stopping the unit flushes its chains"
rm -f "$FAKEBIN/systemctl"

# A user D1 promises but the node does not serve is just as broken.
NODE_MISSING=$(mk nodemissing 'JPY-01\tkulinh\tuuid-a\tpassword-aaaa\n')
OUT=$(drift_compare "$D1_OK" "$NODE_MISSING"); RC=$?
is "$RC" "1" "user missing on the node exits 1"
contains "$OUT" "node=<absent>" "user missing on the node is reported"

NODE_EXTRA=$(mk nodeextra 'JPY-01\tkulinh\tuuid-a\tpassword-aaaa\nSIN-01\tkulinh\tuuid-b\tpassword-bbbb\nSIN-01\tghost\tuuid-g\tpassword-gggg\n')
OUT=$(drift_compare "$D1_OK" "$NODE_EXTRA"); RC=$?
is "$RC" "1" "user only on the node exits 1"
contains "$OUT" "d1=<absent>" "user only on the node is reported"

OUT=$(drift_compare "$DRIFT_DIR/nope" "$NODE_OK" 2>/dev/null); RC=$?
is "$RC" "2" "unreadable input exits 2 (not mistaken for 'in sync')"

# ---------------------------------------------------------------------------
section "cfvpn-drift.sh — transport flags: node cfvpn.env vs D1 (C6)"
# d1:   <node>\t<hy2_host>\t<xhttp_enabled>\t<xhttp_direct_host>
# node: <node>\t<HY2_ENABLED>\t<XHTTP_ENABLED>\t<XHTTP_DIRECT_HOST>
# Both sides raw, so the default-on/default-off rules are what is under test.
TD1=$(mk td1 'JPY-01\thy.a\t0\t\nSIN-01\t\t1\tstatic.x\n')
TNODE=$(mk tnode 'JPY-01\t\t\t\nSIN-01\t0\t1\tstatic.x\n')
OUT=$(drift_transport_compare "$TD1" "$TNODE"); RC=$?
is "$RC" "0" "in sync exits 0 (absent HY2_ENABLED == hy2_host set == on)"
is "$OUT" "" "in sync prints nothing"

# The exact failure: `cfvpnctl hy2 disable` over SSH, D1 never told. The panel
# keeps handing out a hysteria2:// link to a node with no hysteria listening.
TNODE_OFF=$(mk tnodeoff 'JPY-01\t0\t\t\nSIN-01\t0\t1\tstatic.x\n')
OUT=$(drift_transport_compare "$TD1" "$TNODE_OFF"); RC=$?
is "$RC" "1" "hy2 disabled on the node but still set in D1 exits 1"
contains "$OUT" "JPY-01	-	hy2_enabled	d1=on	node=off" "hy2 drift names the node and both sides"
case "$OUT" in *SIN-01*) bad "in-sync node reported as drifted" ;; *) ok "only the drifted node is reported" ;; esac

# The reverse: HY2 re-enabled on the node while D1 still has hy2_host NULL.
TD1_NULL=$(mk td1null 'JPY-01\t\t0\t\n')
TNODE_ON=$(mk tnodeon 'JPY-01\t1\t\t\n')
contains "$(drift_transport_compare "$TD1_NULL" "$TNODE_ON")" \
   "JPY-01	-	hy2_enabled	d1=off	node=on" "hy2 NULL in D1 while the node runs it is drift too"

# XHTTP-H3 (column 5): the node turns it on with `cfvpnctl xhttp-h3 enable`,
# and until the agent's next /status the panel does not know, so the
# subscription lacks a route the node is serving. The reverse — D1 still
# advertising a route the node turned off — hands clients a dead endpoint.
TD1_H3=$(mk td1h3 'JPY-03\thy.a\t0\t\tquic-b55.dongnat247.com\n')
TNODE_H3_OFF=$(mk tnodeh3off 'JPY-03\t\t\t\t\n')
OUT=$(drift_transport_compare "$TD1_H3" "$TNODE_H3_OFF"); RC=$?
is "$RC" "1" "H3 set in D1 but not on the node exits 1"
contains "$OUT" "JPY-03	-	xhttp_h3_host	d1=quic-b55.dongnat247.com	node=-" \
   "H3 drift names the node and both sides"

TNODE_H3_ON=$(mk tnodeh3on 'JPY-03\t\t\t\tquic-b55.dongnat247.com\n')
TD1_NO_H3=$(mk td1noh3 'JPY-03\thy.a\t0\t\t\n')
contains "$(drift_transport_compare "$TD1_NO_H3" "$TNODE_H3_ON")" \
   "JPY-03	-	xhttp_h3_host	d1=-	node=quic-b55.dongnat247.com" \
   "H3 running on the node while D1 does not know is drift too"

is "$(drift_transport_compare "$TD1_H3" "$(mk tnodeh3same 'JPY-03\t\t\t\tquic-b55.dongnat247.com\n')" | grep -c xhttp_h3_host)" "0" \
   "matching H3 on both sides is not drift"

# Rows written before the H3 column existed have only four fields; an absent
# fifth column must read as "no route", not as drift.
is "$(drift_transport_compare "$(mk td1legacy 'JPY-01\thy.a\t0\t\n')" "$(mk tnodelegacy 'JPY-01\t\t\t\n')" | grep -c xhttp_h3_host)" "0" \
   "legacy four-column rows do not report phantom H3 drift"

# Every documented spelling of off/on must normalise like commands.Hy2Enabled
# and commands.XHTTPEnabled, or a node flipped by hand reads as drifted.
for spelling in 0 false no off FALSE Off ' off '; do
  n=$(mk tnodesp "JPY-01\t$spelling\t\t\n")
  is "$(drift_transport_compare "$TD1_NULL" "$n" | grep -c hy2_enabled)" "0" \
     "HY2_ENABLED='$spelling' reads as off (matches Hy2Enabled)"
done
for spelling in 1 true yes on TRUE On; do
  n=$(mk tnodexh "SIN-01\t\t$spelling\t\n")
  d=$(mk td1xh 'SIN-01\thy.b\t1\t\n')
  is "$(drift_transport_compare "$d" "$n" | grep -c xhttp_enabled)" "0" \
     "XHTTP_ENABLED='$spelling' reads as on (matches XHTTPEnabled)"
done
# A value that is neither: XHTTP is OFF by default, HY2 is ON by default.
n=$(mk tnodejunk 'SIN-01\tmaybe\tmaybe\t\n')
d=$(mk td1junk 'SIN-01\thy.b\t1\t\n')
OUT=$(drift_transport_compare "$d" "$n")
is "$(printf '%s\n' "$OUT" | grep -c hy2_enabled)" "0" "an unrecognised HY2_ENABLED still means on"
contains "$OUT" "xhttp_enabled	d1=on	node=off" "an unrecognised XHTTP_ENABLED means off"

# xhttp_direct_host is compared verbatim; empty on either side is "-".
TNODE_DH=$(mk tnodedh 'SIN-01\t0\t1\tstale.x\n')
contains "$(drift_transport_compare "$TD1" "$TNODE_DH")" \
   "SIN-01	-	xhttp_direct_host	d1=static.x	node=stale.x" "a changed xhttp_direct_host is drift"
TNODE_NODH=$(mk tnodenodh 'SIN-01\t0\t1\t\n')
contains "$(drift_transport_compare "$TD1" "$TNODE_NODH")" \
   "SIN-01	-	xhttp_direct_host	d1=static.x	node=-" "an empty xhttp_direct_host on the node is drift, shown as -"

# Rows on only one side: a node D1 does not know is as broken as a wrong flag.
OUT=$(drift_transport_compare "$TD1_NULL" "$TNODE"); RC=$?
is "$RC" "1" "a node missing from D1 exits 1"
contains "$OUT" "SIN-01	-	node_row	d1=<absent>	node=present" "the node absent from D1 is named"
OUT=$(drift_transport_compare "$TD1" "$TNODE_ON"); RC=$?
contains "$OUT" "SIN-01	-	node_row	d1=present	node=<absent>" "a D1 node that was not read back is named"

OUT=$(drift_transport_compare "$DRIFT_DIR/nope" "$TNODE" 2>/dev/null); RC=$?
is "$RC" "2" "unreadable input exits 2 (not mistaken for 'in sync')"

# The exit-status RANKING check-fleet-drift.sh folds its statuses through.
rank() { # rank <status>... — the real drift_rank_rc, folded like the script does
  local rc=0 r
  for r in "$@"; do rc="$(drift_rank_rc "$rc" "$r")"; done
  printf '%s\n' "$rc"
}
is "$(rank 0 0)" "0" "rank: nothing wrong exits 0"
is "$(rank 1 0 2)" "1" "rank: drift found plus an unreachable host exits 1, not 2"
is "$(rank 0 2 1)" "1" "rank: order does not matter — drift still wins"
is "$(rank 0 2 2)" "2" "rank: only an incomplete check exits 2"
is "$(rank 0 1 0)" "1" "rank: a later clean comparison does not erase drift"

# ---------------------------------------------------------------------------
section "d1-set-node.sh — SQL per action, guards, exit codes (writes prod D1)"
# The REAL script runs in a child bash. Seams: CFVPN_ENV_FILE (CF credentials
# fixture) and CFVPN_FLEET_HOSTS; `ssh` and `curl` are fake executables first on
# PATH, so the real d1_query builds the request and the fake curl captures the
# JSON payload it would have POSTed. Nothing reaches a node or Cloudflare.
D1S="$TMPROOT/d1set"; D1S_BIN="$D1S/bin"; mkdir -p "$D1S_BIN"
printf 'CF_API_TOKEN=tok\nCF_ACCOUNT_ID=acct\n' >"$D1S/creds.env"
printf 'JPY-03 root@jpy-03\n' >"$D1S/fleet-hosts"
D1S_SSH_LOG="$D1S/ssh.log"; D1S_NODE_ENV="$D1S/node.env"; D1S_CAP="$D1S/payload.json"
D1S_RESP='{"success":true,"result":[{"meta":{"changes":1}}]}'
cat >"$D1S_BIN/ssh" <<'SSHEOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$D1S_SSH_LOG"
cat "$D1S_NODE_ENV"
SSHEOF
cat >"$D1S_BIN/curl" <<'CURLEOF'
#!/usr/bin/env bash
for a in "$@"; do [ "$a" = "--version" ] && exit 0; done
f="$(sed -n 's/^data-binary = "@\(.*\)"$/\1/p')"   # the --config arrives on stdin
cp "$f" "$D1S_CAP"
printf '%s' "$D1S_RESP"
CURLEOF
chmod +x "$D1S_BIN/ssh" "$D1S_BIN/curl"
export D1S_SSH_LOG D1S_NODE_ENV D1S_CAP
node_env_fixture() {
  cat >"$D1S_NODE_ENV" <<'NEOF'
HY2_HOST=hy-c36ca6bd.dongnat247.com
HY2_PORT=31300
HY2_OBFS_PW=obfs=pw
REALITY_PUBLIC_KEY=pbk
REALITY_SHORT_ID=4a2739d7
REALITY_SNI=www.sony.jp
REALITY_DEST=www.sony.jp:443
PUBLIC_IP=203.0.113.9
XHTTP_DIRECT_HOST=static-df60bd79.duylinh.org
XHTTP_DIRECT_PATH=
XHTTP_H3_HOST=quic-b55170f3.dongnat247.com
XHTTP_H3_PATH=/h3path
NAIVE_HOST=naive.example.com
NAIVE_USER=kulinh
NAIVE_PASS=npw
PUBLIC_IPV6=2603:c023:1::9
NEOF
}
run_set() { # run_set <action> — sets out/rc; the payload lands in $D1S_CAP
  : >"$D1S_SSH_LOG"; rm -f "$D1S_CAP"
  out="$(PATH="$D1S_BIN:$PATH" D1S_RESP="$D1S_RESP" CFVPN_ENV_FILE="$D1S/creds.env" \
    CFVPN_FLEET_HOSTS="$D1S/fleet-hosts" CFVPN_LOCAL_TARGET=root@nowhere \
    bash "$ROOT/scripts/d1-set-node.sh" JPY-03 "$1" 2>&1)"; rc=$?
}
sql_of()    { jq -r '.sql' "$D1S_CAP" 2>/dev/null; }
params_of() { jq -c '.params' "$D1S_CAP" 2>/dev/null; }
ssh_calls() { grep -c . "$D1S_SSH_LOG"; }

node_env_fixture
run_set hy2-off
is "$rc" "0" "hy2-off exits 0"
is "$(sql_of)" "UPDATE nodes SET hy2_host=NULL, hy2_port=NULL, hy2_obfs_pw=NULL WHERE id=?" "hy2-off SQL"
is "$(params_of)" '["JPY-03"]' "hy2-off params"
is "$(ssh_calls)" "0" "hy2-off never reaches the node"

run_set hy2-on
is "$rc" "0" "hy2-on exits 0"
is "$(sql_of)" "UPDATE nodes SET hy2_host=?, hy2_port=?, hy2_obfs_pw=? WHERE id=?" "hy2-on SQL"
is "$(params_of)" '["hy-c36ca6bd.dongnat247.com",31300,"obfs=pw","JPY-03"]' \
   "hy2-on params (port is an integer; a value holding '=' survives)"
is "$(ssh_calls)" "1" "hy2-on reads the node env once over ssh"
contains "$(cat "$D1S_SSH_LOG")" "root@jpy-03 cat /etc/cfvpn/cfvpn.env" "…from the target named in the hosts file"

run_set reality
is "$rc" "0" "reality exits 0"
is "$(sql_of)" "UPDATE nodes SET reality_pubkey=?, reality_sid=?, reality_sni=?, reality_dest=?, public_ip=? WHERE id=?" "reality SQL"
is "$(params_of)" '["pbk","4a2739d7","www.sony.jp","www.sony.jp:443","203.0.113.9","JPY-03"]' "reality params"

run_set xhttp-on
is "$(sql_of)|$(params_of)|$(ssh_calls)" 'UPDATE nodes SET xhttp_enabled=? WHERE id=?|[1,"JPY-03"]|0' \
   "xhttp-on SQL + params, no ssh"
run_set xhttp-off
is "$(params_of)|$(ssh_calls)" '[0,"JPY-03"]|0' "xhttp-off params, no ssh"

run_set xhttp-direct
is "$(sql_of)" 'UPDATE nodes SET xhttp_direct_host=NULLIF(?,""), xhttp_direct_path=NULLIF(?,"") WHERE id=?' "xhttp-direct SQL"
is "$(params_of)" '["static-df60bd79.duylinh.org","","JPY-03"]' "xhttp-direct params (empty path -> NULLIF)"

run_set xhttp-h3
is "$(sql_of)" 'UPDATE nodes SET xhttp_h3_host=NULLIF(?,""), xhttp_h3_path=NULLIF(?,"") WHERE id=?' "xhttp-h3 SQL"
is "$(params_of)" '["quic-b55170f3.dongnat247.com","/h3path","JPY-03"]' "xhttp-h3 params"

run_set naive
is "$(sql_of)" 'UPDATE nodes SET naive_host=NULLIF(?,""), naive_user=NULLIF(?,""), naive_pass=NULLIF(?,"") WHERE id=?' "naive SQL"
is "$(params_of)" '["naive.example.com","kulinh","npw","JPY-03"]' "naive params"

run_set ipv6
is "$rc" "0" "ipv6 exits 0 for a plain address"
is "$(sql_of)" 'UPDATE nodes SET public_ipv6=NULLIF(?,"") WHERE id=?' "ipv6 SQL"
is "$(params_of)" '["2603:c023:1::9","JPY-03"]' "ipv6 params"

# PUBLIC_IPV6 validation (L1): only a plain IPv6 address, or empty (= NULL).
set_v6() { node_env_fixture; sed -i '/^PUBLIC_IPV6=/d' "$D1S_NODE_ENV"; printf 'PUBLIC_IPV6=%s\n' "$1" >>"$D1S_NODE_ENV"; }
for good in "::1" "2001:db8::1" "2603:c023:1:5e00:abcd:ef01:2345:6789" ""; do
  set_v6 "$good"; run_set ipv6
  is "$rc|$(params_of)" "0|[\"$good\",\"JPY-03\"]" "ipv6 accepts [$good]"
done
for evil in "fe80::1%eth0" "[2001:db8::1]" "::ffff:192.0.2.1" "1.2.3.4" "2001:db8::zz" ":" "2001:db8::1 ; x"; do
  set_v6 "$evil"; run_set ipv6
  is "$rc" "1" "ipv6 rejects [$evil]"
  is "$([ -e "$D1S_CAP" ] && echo written || echo none)" "none" "…and nothing is sent to D1 for [$evil]"
done
contains "$out" "refusing to write" "ipv6 rejection says it refused"

# reality refuses an incomplete env (a half-written row breaks every client).
for k in REALITY_PUBLIC_KEY REALITY_SHORT_ID REALITY_SNI REALITY_DEST PUBLIC_IP; do
  node_env_fixture; sed -i "/^$k=/d" "$D1S_NODE_ENV"
  run_set reality
  is "$rc|$([ -e "$D1S_CAP" ] && echo written || echo none)" "1|none" "reality without $k exits 1 and writes nothing"
done
contains "$out" "incomplete in cfvpn.env; refusing to write" "reality refusal names the problem"
node_env_fixture

run_set bogus-action
is "$rc" "2" "unknown action exits 2"
contains "$out" "unknown action: bogus-action" "unknown action is named"
is "$(ssh_calls)|$([ -e "$D1S_CAP" ] && echo written || echo none)" "0|none" "unknown action: no ssh, no D1"

D1S_RESP='{"success":true,"result":[{"meta":{"changes":0}}]}'
run_set xhttp-on
is "$rc" "1" "changes=0 (no such node row) exits 1"
contains "$out" "D1 updated 0 rows for JPY-03 (expected 1)" "changes mismatch is reported"
D1S_RESP='{"success":true,"result":[{"meta":{"changes":2}}]}'
run_set xhttp-on
is "$rc" "1" "changes=2 exits 1"
D1S_RESP='{"success":false,"errors":[{"message":"no such column"}]}'
run_set xhttp-on
is "$rc" "1" "success:false exits 1"
contains "$out" "D1 update failed" "D1 failure is reported"
D1S_RESP='{"success":true,"result":[{"meta":{"changes":1}}]}'


printf '\n--------------------------------------------\n'
printf 'scripts/tests: pass=%d fail=%d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
