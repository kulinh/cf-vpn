# Cloudflare Tunnel VPN Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a personal VPN (1-5 users) with VLESS + Trojan over WebSocket, routed through Cloudflare Tunnel, deployable on a single VPS via Docker Compose, with idempotent install/rotate scripts and manual verification checklist.

**Architecture:** Two Docker services (Xray + cloudflared) on a private Docker network. Cloudflared outbound-only tunnel to CF edge; CF edge routes `wss://<domain>/vless` and `wss://<domain>/trojan` to the tunnel. All orchestration in bash scripts with a shared lib layer. TDD via bats-core for pure functions; shellcheck for lint; manual E2E for CF API integration.

**Tech Stack:** Docker Compose v2, Xray-core (teddysun/xray image), cloudflared (cloudflare/cloudflared image), bash 5+, jq, curl, openssl, uuidgen, envsubst, qrencode, bats-core, shellcheck.

**Working directory:** `/opt/cf-vpn`

---

## File Structure

```
/opt/cf-vpn/
├── .env.example                        # Template (committed)
├── .gitignore                          # Ignore .env, secrets, subscriptions/
├── Makefile                            # lint, test, install, smoke
├── README.md                           # Vận hành guide
├── docker-compose.yml                  # xray + cloudflared
├── cloudflared/
│   └── config.template.yml             # envsubst → config.yml
├── xray/
│   └── config.template.json            # envsubst → config.json (initial single user)
├── scripts/
│   ├── install.sh                      # Bootstrap orchestrator
│   ├── gen-subscription.sh             # Xuất URI/base64/QR per user
│   ├── add-user.sh                     # Thêm UUID + Trojan password
│   ├── remove-user.sh                  # Xóa user
│   ├── rotate-domain.sh                # Swap sang domain khác
│   ├── healthcheck.sh                  # Cron probe
│   └── lib/
│       ├── common.sh                   # log, die, require_cmd, env R/W
│       ├── cf_api.sh                   # Cloudflare API wrappers
│       ├── xray_config.sh              # jq manipulation helpers
│       └── uri.sh                      # URI + QR builders
├── tests/
│   ├── test_common.bats
│   ├── test_cf_api.bats
│   ├── test_xray_config.bats
│   ├── test_uri.bats
│   └── fixtures/
│       ├── xray_config.empty.json
│       └── xray_config.2users.json
└── docs/
    ├── TESTING.md                      # Manual checklist
    └── superpowers/
        ├── specs/2026-04-19-cf-vpn-design.md
        └── plans/2026-04-19-cf-vpn-implementation.md
```

Each file has one clear responsibility:
- `lib/*.sh` — pure functions, unit-tested via bats
- `scripts/*.sh` — orchestration, composes lib functions, manual-tested
- `tests/` — bats tests, mirror lib files
- Templates separate from rendered output (gitignored output)

---

## Test Infrastructure

We use:
- **bats-core v1.10+** for unit tests on pure bash functions
- **shellcheck v0.9+** for static analysis
- **Makefile** orchestrates: `make lint` (shellcheck all), `make test` (bats all), `make all`

No CI/CD automation (per spec §10.6). Tests run locally before commits.

---

## Task 1: Project Scaffolding

**Files:**
- Create: `/opt/cf-vpn/.gitignore`
- Create: `/opt/cf-vpn/README.md` (stub)
- Create: `/opt/cf-vpn/Makefile`

- [ ] **Step 1: Initialize git repo**

```bash
cd /opt/cf-vpn
git init
git config user.email "daica@cf-vpn.local"
git config user.name "Đại ca"
```

Expected: `Initialized empty Git repository in /opt/cf-vpn/.git/`

- [ ] **Step 2: Write `.gitignore`**

Content of `/opt/cf-vpn/.gitignore`:
```gitignore
# Secrets & runtime state
.env
.env.local
cloudflared/*.json
cloudflared/config.yml
xray/config.json
subscriptions/

# Editor
.vscode/
.idea/
*.swp
*~

# OS
.DS_Store
Thumbs.db

# Logs
*.log
```

- [ ] **Step 3: Write stub `README.md`**

Content of `/opt/cf-vpn/README.md`:
```markdown
# cf-vpn

Personal VPN (1-5 users) over Cloudflare Tunnel with VLESS + Trojan protocols.

**Status:** Under construction. See `docs/superpowers/specs/` for design and `docs/superpowers/plans/` for implementation plan.

## Quick start (will be filled in Task 19)

TBD
```

- [ ] **Step 4: Write `Makefile`**

Content of `/opt/cf-vpn/Makefile`:
```makefile
.PHONY: lint test all install smoke clean

SHELL := /bin/bash
SCRIPTS := $(shell find scripts -name '*.sh' 2>/dev/null)

lint:
	@command -v shellcheck >/dev/null || { echo "shellcheck not installed"; exit 1; }
	@echo "==> shellcheck"
	@shellcheck -x $(SCRIPTS)

test:
	@command -v bats >/dev/null || { echo "bats not installed"; exit 1; }
	@echo "==> bats"
	@bats tests/

all: lint test

install:
	@bash scripts/install.sh

smoke:
	@bash scripts/healthcheck.sh || true
	@docker compose ps

clean:
	@echo "Use 'docker compose down -v' explicitly to remove state"
```

- [ ] **Step 5: Verify Makefile syntax**

Run: `make -n lint`
Expected: prints the commands without executing (no "missing separator" errors).

- [ ] **Step 6: Commit**

```bash
git add .gitignore README.md Makefile
git commit -m "chore: initial project scaffolding"
```

---

## Task 2: Install Test Dependencies

**Files:** none (system-level install)

- [ ] **Step 1: Install shellcheck + bats-core**

On Debian/Ubuntu VPS:
```bash
apt-get update
apt-get install -y shellcheck bats
```

On systems without `bats` in apt:
```bash
git clone --depth 1 https://github.com/bats-core/bats-core.git /tmp/bats-core
cd /tmp/bats-core && ./install.sh /usr/local
cd - && rm -rf /tmp/bats-core
```

- [ ] **Step 2: Verify versions**

Run: `shellcheck --version && bats --version`
Expected: shellcheck ≥0.7, bats ≥1.5.

- [ ] **Step 3: Verify other runtime deps**

Run:
```bash
for cmd in docker jq curl openssl uuidgen envsubst qrencode; do
  command -v "$cmd" >/dev/null && echo "OK: $cmd" || echo "MISSING: $cmd"
done
docker compose version
```

Expected: all "OK" and `Docker Compose version v2.x`.
If any missing, install via apt: `apt-get install -y jq curl openssl uuid-runtime gettext qrencode`.

No commit (system install).

---

## Task 3: Shared Library — `lib/common.sh`

**Files:**
- Create: `scripts/lib/common.sh`
- Create: `tests/test_common.bats`

- [ ] **Step 1: Write the failing test**

Content of `/opt/cf-vpn/tests/test_common.bats`:
```bash
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /opt/cf-vpn && bats tests/test_common.bats`
Expected: 6 tests fail with "common.sh: No such file".

- [ ] **Step 3: Write implementation**

Content of `/opt/cf-vpn/scripts/lib/common.sh`:
```bash
#!/usr/bin/env bash
# Shared helpers. Source-only. No top-level side effects.

log() {
  printf '[cf-vpn] %s\n' "$*"
}

die() {
  printf '[cf-vpn] ERROR: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  local cmd="$1"
  command -v "$cmd" >/dev/null 2>&1 || die "required command not found: $cmd"
}

# env_write <file> <key> <value>
# Creates file if missing. Replaces existing KEY=... line or appends.
env_write() {
  local file="$1" key="$2" value="$3"
  [ -f "$file" ] || { touch "$file"; chmod 600 "$file"; }
  if grep -q "^${key}=" "$file" 2>/dev/null; then
    # Escape for sed: \ / &
    local esc_value
    esc_value=$(printf '%s' "$value" | sed -e 's/[\/&]/\\&/g')
    sed -i.bak "s/^${key}=.*/${key}=${esc_value}/" "$file"
    rm -f "${file}.bak"
  else
    printf '%s=%s\n' "$key" "$value" >> "$file"
  fi
}

# env_read <file> <key>
# Prints value (unquoted) or empty if missing.
env_read() {
  local file="$1" key="$2"
  [ -f "$file" ] || return 1
  # Take last occurrence, strip KEY= prefix
  grep "^${key}=" "$file" | tail -n 1 | sed "s/^${key}=//"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /opt/cf-vpn && bats tests/test_common.bats`
Expected: 6 tests pass.

- [ ] **Step 5: Run shellcheck**

Run: `shellcheck scripts/lib/common.sh`
Expected: no output (clean).

- [ ] **Step 6: Commit**

```bash
git add scripts/lib/common.sh tests/test_common.bats
git commit -m "feat(lib): common helpers (log, die, require_cmd, env R/W)"
```

---

## Task 4: Shared Library — `lib/uri.sh`

**Files:**
- Create: `scripts/lib/uri.sh`
- Create: `tests/test_uri.bats`

- [ ] **Step 1: Write the failing test**

Content of `/opt/cf-vpn/tests/test_uri.bats`:
```bash
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /opt/cf-vpn && bats tests/test_uri.bats`
Expected: 4 tests fail with "uri.sh: No such file".

- [ ] **Step 3: Write implementation**

Content of `/opt/cf-vpn/scripts/lib/uri.sh`:
```bash
#!/usr/bin/env bash
# URI and subscription builders. Source-only.

# urlencode <string>
# RFC 3986 percent-encoding for unreserved chars kept: A-Za-z0-9-_.~
urlencode() {
  local s="$1" out="" c
  local i
  for ((i = 0; i < ${#s}; i++)); do
    c="${s:i:1}"
    case "$c" in
      [A-Za-z0-9._~-]) out+="$c" ;;
      *) out+=$(printf '%%%02X' "'$c") ;;
    esac
  done
  printf '%s' "$out"
}

# build_vless_uri <uuid> <domain> <name>
build_vless_uri() {
  local uuid="$1" domain="$2" name="$3"
  local enc_name
  enc_name=$(urlencode "$name-VLESS")
  printf 'vless://%s@%s:443?encryption=none&security=tls&type=ws&host=%s&path=%%2Fvless&sni=%s#%s' \
    "$uuid" "$domain" "$domain" "$domain" "$enc_name"
}

# build_trojan_uri <password> <domain> <name>
build_trojan_uri() {
  local password="$1" domain="$2" name="$3"
  local enc_pass enc_name
  enc_pass=$(urlencode "$password")
  enc_name=$(urlencode "$name-Trojan")
  printf 'trojan://%s@%s:443?security=tls&type=ws&host=%s&path=%%2Ftrojan&sni=%s#%s' \
    "$enc_pass" "$domain" "$domain" "$domain" "$enc_name"
}

# build_subscription_b64 <uri1> <uri2> [...]
# Output: base64-encoded newline-joined URIs (v2rayN/v2rayNG subscription format)
build_subscription_b64() {
  local plain=""
  local uri
  for uri in "$@"; do
    plain+="$uri"$'\n'
  done
  printf '%s' "$plain" | base64 -w 0
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /opt/cf-vpn && bats tests/test_uri.bats`
Expected: 4 tests pass.

- [ ] **Step 5: Run shellcheck**

Run: `shellcheck scripts/lib/uri.sh`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add scripts/lib/uri.sh tests/test_uri.bats
git commit -m "feat(lib): URI and subscription builders (VLESS/Trojan/base64)"
```

---

## Task 5: Shared Library — `lib/xray_config.sh`

**Files:**
- Create: `scripts/lib/xray_config.sh`
- Create: `tests/fixtures/xray_config.empty.json`
- Create: `tests/fixtures/xray_config.2users.json`
- Create: `tests/test_xray_config.bats`

- [ ] **Step 1: Write fixture — empty config**

Content of `/opt/cf-vpn/tests/fixtures/xray_config.empty.json`:
```json
{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {
      "tag": "vless-ws",
      "listen": "0.0.0.0",
      "port": 10001,
      "protocol": "vless",
      "settings": {"clients": [], "decryption": "none"},
      "streamSettings": {"network": "ws", "wsSettings": {"path": "/vless"}}
    },
    {
      "tag": "trojan-ws",
      "listen": "0.0.0.0",
      "port": 10002,
      "protocol": "trojan",
      "settings": {"clients": []},
      "streamSettings": {"network": "ws", "wsSettings": {"path": "/trojan"}}
    }
  ],
  "outbounds": [
    {"tag": "direct", "protocol": "freedom"},
    {"tag": "block", "protocol": "blackhole"}
  ],
  "routing": {
    "rules": [
      {"type": "field", "ip": ["geoip:private"], "outboundTag": "block"}
    ]
  }
}
```

- [ ] **Step 2: Write fixture — 2 users**

Content of `/opt/cf-vpn/tests/fixtures/xray_config.2users.json`:
```json
{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {
      "tag": "vless-ws",
      "listen": "0.0.0.0",
      "port": 10001,
      "protocol": "vless",
      "settings": {
        "clients": [
          {"id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "email": "alice@vpn"},
          {"id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "email": "bob@vpn"}
        ],
        "decryption": "none"
      },
      "streamSettings": {"network": "ws", "wsSettings": {"path": "/vless"}}
    },
    {
      "tag": "trojan-ws",
      "listen": "0.0.0.0",
      "port": 10002,
      "protocol": "trojan",
      "settings": {
        "clients": [
          {"password": "alice-pass", "email": "alice@vpn"},
          {"password": "bob-pass", "email": "bob@vpn"}
        ]
      },
      "streamSettings": {"network": "ws", "wsSettings": {"path": "/trojan"}}
    }
  ],
  "outbounds": [
    {"tag": "direct", "protocol": "freedom"},
    {"tag": "block", "protocol": "blackhole"}
  ],
  "routing": {"rules": [{"type": "field", "ip": ["geoip:private"], "outboundTag": "block"}]}
}
```

- [ ] **Step 3: Write the failing test**

Content of `/opt/cf-vpn/tests/test_xray_config.bats`:
```bash
#!/usr/bin/env bats

setup() {
  PROJECT_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  # shellcheck source=/dev/null
  source "$PROJECT_ROOT/scripts/lib/xray_config.sh"
  TMP="$(mktemp -d)"
  cp "$PROJECT_ROOT/tests/fixtures/xray_config.empty.json" "$TMP/config.json"
  cp "$PROJECT_ROOT/tests/fixtures/xray_config.2users.json" "$TMP/2users.json"
}

teardown() {
  rm -rf "$TMP"
}

@test "count_clients returns 0 for empty config" {
  run count_clients "$TMP/config.json"
  [ "$status" -eq 0 ]
  [ "$output" = "0" ]
}

@test "count_clients returns 2 for 2-user config" {
  run count_clients "$TMP/2users.json"
  [ "$output" = "2" ]
}

@test "add_client appends to both inbounds" {
  add_client "$TMP/config.json" "charlie" "ccccccc1-cccc-cccc-cccc-cccccccccccc" "charlie-secret"
  run count_clients "$TMP/config.json"
  [ "$output" = "1" ]
  # Verify UUID in vless
  run jq -r '.inbounds[] | select(.tag=="vless-ws") | .settings.clients[0].id' "$TMP/config.json"
  [ "$output" = "ccccccc1-cccc-cccc-cccc-cccccccccccc" ]
  # Verify password in trojan
  run jq -r '.inbounds[] | select(.tag=="trojan-ws") | .settings.clients[0].password' "$TMP/config.json"
  [ "$output" = "charlie-secret" ]
  # Verify email matches
  run jq -r '.inbounds[] | select(.tag=="vless-ws") | .settings.clients[0].email' "$TMP/config.json"
  [ "$output" = "charlie@vpn" ]
}

@test "add_client rejects duplicate name" {
  add_client "$TMP/2users.json" "alice" "new-uuid" "new-pass"
  # count should still be 2 (no-op or error)
  run count_clients "$TMP/2users.json"
  [ "$output" = "2" ]
}

@test "remove_client drops user from both inbounds" {
  remove_client "$TMP/2users.json" "alice"
  run count_clients "$TMP/2users.json"
  [ "$output" = "1" ]
  run jq -r '.inbounds[] | select(.tag=="vless-ws") | .settings.clients[0].email' "$TMP/2users.json"
  [ "$output" = "bob@vpn" ]
}

@test "remove_client no-op for missing user" {
  remove_client "$TMP/2users.json" "zach"
  run count_clients "$TMP/2users.json"
  [ "$output" = "2" ]
}

@test "list_client_names returns sorted names" {
  run list_client_names "$TMP/2users.json"
  [ "${lines[0]}" = "alice" ]
  [ "${lines[1]}" = "bob" ]
}

@test "get_client_uuid returns uuid by name" {
  run get_client_uuid "$TMP/2users.json" "alice"
  [ "$output" = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" ]
}

@test "get_client_password returns trojan password by name" {
  run get_client_password "$TMP/2users.json" "bob"
  [ "$output" = "bob-pass" ]
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `cd /opt/cf-vpn && bats tests/test_xray_config.bats`
Expected: 9 tests fail with "xray_config.sh: No such file".

- [ ] **Step 5: Write implementation**

Content of `/opt/cf-vpn/scripts/lib/xray_config.sh`:
```bash
#!/usr/bin/env bash
# Xray config manipulation via jq. Source-only.
# Convention: "email" field = "<name>@vpn" — name is the primary key.

# count_clients <config-file>
count_clients() {
  jq -r '.inbounds[] | select(.tag=="vless-ws") | .settings.clients | length' "$1"
}

# list_client_names <config-file>
# Prints one name per line, sorted.
list_client_names() {
  jq -r '.inbounds[] | select(.tag=="vless-ws") | .settings.clients[].email' "$1" \
    | sed 's/@vpn$//' | sort
}

# add_client <config-file> <name> <uuid> <trojan-password>
# No-op if name already exists. Atomic: writes via tmp file.
add_client() {
  local file="$1" name="$2" uuid="$3" password="$4"
  local email="${name}@vpn"
  # Check duplicate
  if jq -e --arg e "$email" '.inbounds[] | select(.tag=="vless-ws") | .settings.clients[] | select(.email==$e)' "$file" >/dev/null 2>&1; then
    return 0
  fi
  local tmp="${file}.tmp"
  jq --arg email "$email" --arg uuid "$uuid" --arg pw "$password" '
    (.inbounds[] | select(.tag=="vless-ws") | .settings.clients) +=
      [{"id": $uuid, "email": $email}]
    | (.inbounds[] | select(.tag=="trojan-ws") | .settings.clients) +=
      [{"password": $pw, "email": $email}]
  ' "$file" > "$tmp" && mv "$tmp" "$file"
}

# remove_client <config-file> <name>
remove_client() {
  local file="$1" name="$2"
  local email="${name}@vpn"
  local tmp="${file}.tmp"
  jq --arg email "$email" '
    (.inbounds[] | select(.tag=="vless-ws") | .settings.clients) |=
      map(select(.email != $email))
    | (.inbounds[] | select(.tag=="trojan-ws") | .settings.clients) |=
      map(select(.email != $email))
  ' "$file" > "$tmp" && mv "$tmp" "$file"
}

# get_client_uuid <config-file> <name>
get_client_uuid() {
  local file="$1" name="$2"
  jq -r --arg e "${name}@vpn" \
    '.inbounds[] | select(.tag=="vless-ws") | .settings.clients[] | select(.email==$e) | .id' \
    "$file"
}

# get_client_password <config-file> <name>
get_client_password() {
  local file="$1" name="$2"
  jq -r --arg e "${name}@vpn" \
    '.inbounds[] | select(.tag=="trojan-ws") | .settings.clients[] | select(.email==$e) | .password' \
    "$file"
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /opt/cf-vpn && bats tests/test_xray_config.bats`
Expected: 9 tests pass.

- [ ] **Step 7: Run shellcheck**

Run: `shellcheck scripts/lib/xray_config.sh`
Expected: no output.

- [ ] **Step 8: Commit**

```bash
git add scripts/lib/xray_config.sh tests/test_xray_config.bats tests/fixtures/
git commit -m "feat(lib): xray config manipulation (add/remove/list clients via jq)"
```

---

## Task 6: Shared Library — `lib/cf_api.sh`

**Files:**
- Create: `scripts/lib/cf_api.sh`
- Create: `tests/test_cf_api.bats`

**Design:** All CF HTTP calls go through `cf_req`. Tests override `cf_req` to return fixture JSON, so we can unit-test selection logic without a real API.

- [ ] **Step 1: Write the failing test**

Content of `/opt/cf-vpn/tests/test_cf_api.bats`:
```bash
#!/usr/bin/env bats

setup() {
  PROJECT_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  # shellcheck source=/dev/null
  source "$PROJECT_ROOT/scripts/lib/cf_api.sh"
  export CF_API_TOKEN="fake-token"
  export CF_ACCOUNT_ID="fake-account"
}

# Mock cf_req: reads fixture from $CF_MOCK_RESPONSE env var
mock_cf_req() {
  printf '%s' "$CF_MOCK_RESPONSE"
  return 0
}

@test "get_zone_id extracts id from API response" {
  export CF_MOCK_RESPONSE='{"success":true,"result":[{"id":"zone-abc","name":"example.com"}]}'
  cf_req() { mock_cf_req "$@"; }
  run get_zone_id "example.com"
  [ "$status" -eq 0 ]
  [ "$output" = "zone-abc" ]
}

@test "get_zone_id fails when zone not found" {
  export CF_MOCK_RESPONSE='{"success":true,"result":[]}'
  cf_req() { mock_cf_req "$@"; }
  run get_zone_id "missing.com"
  [ "$status" -ne 0 ]
  [[ "$output" == *"missing.com"* ]]
}

@test "get_tunnel_by_name returns tunnel id when present" {
  export CF_MOCK_RESPONSE='{"success":true,"result":[{"id":"t-123","name":"cf-vpn-example","deleted_at":null}]}'
  cf_req() { mock_cf_req "$@"; }
  run get_tunnel_by_name "cf-vpn-example"
  [ "$output" = "t-123" ]
}

@test "get_tunnel_by_name returns empty when not present" {
  export CF_MOCK_RESPONSE='{"success":true,"result":[{"id":"t-999","name":"other","deleted_at":null}]}'
  cf_req() { mock_cf_req "$@"; }
  run get_tunnel_by_name "cf-vpn-example"
  [ "$output" = "" ]
}

@test "get_tunnel_by_name ignores deleted tunnels" {
  export CF_MOCK_RESPONSE='{"success":true,"result":[{"id":"t-del","name":"cf-vpn-example","deleted_at":"2024-01-01"}]}'
  cf_req() { mock_cf_req "$@"; }
  run get_tunnel_by_name "cf-vpn-example"
  [ "$output" = "" ]
}

@test "cf_req_check errors on success=false" {
  run cf_req_check '{"success":false,"errors":[{"code":1000,"message":"bad token"}]}'
  [ "$status" -ne 0 ]
  [[ "$output" == *"bad token"* ]]
}

@test "cf_req_check passes on success=true" {
  run cf_req_check '{"success":true,"result":{}}'
  [ "$status" -eq 0 ]
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /opt/cf-vpn && bats tests/test_cf_api.bats`
Expected: 7 tests fail with "cf_api.sh: No such file".

- [ ] **Step 3: Write implementation**

Content of `/opt/cf-vpn/scripts/lib/cf_api.sh`:
```bash
#!/usr/bin/env bash
# Cloudflare API wrappers. Source-only.
# Requires env: CF_API_TOKEN, CF_ACCOUNT_ID.
# All calls go through cf_req so tests can stub it.

CF_API_BASE="${CF_API_BASE:-https://api.cloudflare.com/client/v4}"

# cf_req <method> <path> [json-body]
# Prints raw response body. Does NOT validate success — use cf_req_check on the output.
cf_req() {
  local method="$1" path="$2" body="${3:-}"
  local url="${CF_API_BASE}${path}"
  if [ -n "$body" ]; then
    curl -sS -X "$method" "$url" \
      -H "Authorization: Bearer ${CF_API_TOKEN}" \
      -H "Content-Type: application/json" \
      --data "$body"
  else
    curl -sS -X "$method" "$url" \
      -H "Authorization: Bearer ${CF_API_TOKEN}"
  fi
}

# cf_req_check <response-json>
# Exits non-zero with error messages if success != true.
cf_req_check() {
  local resp="$1"
  local ok
  ok=$(printf '%s' "$resp" | jq -r '.success // false')
  if [ "$ok" != "true" ]; then
    local msgs
    msgs=$(printf '%s' "$resp" | jq -r '.errors[]? | "\(.code): \(.message)"' 2>/dev/null)
    printf 'cf api error: %s\n' "$msgs" >&2
    return 1
  fi
  return 0
}

# get_zone_id <domain>
# Matches the zone whose name is an apex suffix of <domain>.
get_zone_id() {
  local domain="$1"
  # Extract apex (last two labels) — naive but works for most TLDs.
  # CF API: GET /zones?name=<apex>
  local apex="$domain"
  # Try progressively shorter suffixes: domain, parent, grandparent...
  local resp id
  while [ -n "$apex" ]; do
    resp=$(cf_req GET "/zones?name=${apex}")
    id=$(printf '%s' "$resp" | jq -r '.result[0].id // empty')
    if [ -n "$id" ]; then
      printf '%s' "$id"
      return 0
    fi
    # Strip first label
    if [[ "$apex" == *.* ]]; then
      apex="${apex#*.}"
    else
      break
    fi
  done
  printf 'zone not found for domain: %s\n' "$domain" >&2
  return 1
}

# get_tunnel_by_name <name>
# Prints tunnel id if a non-deleted tunnel with that name exists, else empty.
get_tunnel_by_name() {
  local name="$1"
  local resp
  resp=$(cf_req GET "/accounts/${CF_ACCOUNT_ID}/cfd_tunnel?name=${name}")
  printf '%s' "$resp" | jq -r --arg n "$name" \
    '[.result[]? | select(.name==$n) | select(.deleted_at==null)] | .[0].id // empty'
}

# create_tunnel <name>
# Returns tunnel id on stdout. Generates a random 32-byte base64 secret.
# Prints credentials JSON to stderr for the caller to capture separately if needed.
create_tunnel() {
  local name="$1"
  local secret tunnel_id resp
  secret=$(openssl rand -base64 32 | tr -d '\n')
  local body
  body=$(jq -n --arg n "$name" --arg s "$secret" \
    '{name: $n, tunnel_secret: $s, config_src: "local"}')
  resp=$(cf_req POST "/accounts/${CF_ACCOUNT_ID}/cfd_tunnel" "$body")
  cf_req_check "$resp" || return 1
  tunnel_id=$(printf '%s' "$resp" | jq -r '.result.id')
  printf '%s' "$tunnel_id"
  # Emit credentials JSON to FD 3 if open, else stderr
  local cred
  cred=$(jq -n --arg acct "$CF_ACCOUNT_ID" --arg tid "$tunnel_id" --arg s "$secret" \
    '{AccountTag: $acct, TunnelID: $tid, TunnelSecret: $s}')
  if { true >&3; } 2>/dev/null; then
    printf '%s' "$cred" >&3
  else
    printf '%s\n' "$cred" >&2
  fi
}

# delete_tunnel <tunnel-id>
delete_tunnel() {
  local tid="$1"
  local resp
  resp=$(cf_req DELETE "/accounts/${CF_ACCOUNT_ID}/cfd_tunnel/${tid}")
  cf_req_check "$resp"
}

# upsert_dns_cname <zone-id> <name> <target>
# Creates CNAME proxied=true if missing; updates if present.
upsert_dns_cname() {
  local zone_id="$1" name="$2" target="$3"
  local resp existing_id
  resp=$(cf_req GET "/zones/${zone_id}/dns_records?type=CNAME&name=${name}")
  existing_id=$(printf '%s' "$resp" | jq -r '.result[0].id // empty')
  local body
  body=$(jq -n --arg name "$name" --arg content "$target" \
    '{type: "CNAME", name: $name, content: $content, proxied: true, ttl: 1}')
  if [ -n "$existing_id" ]; then
    resp=$(cf_req PUT "/zones/${zone_id}/dns_records/${existing_id}" "$body")
  else
    resp=$(cf_req POST "/zones/${zone_id}/dns_records" "$body")
  fi
  cf_req_check "$resp"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /opt/cf-vpn && bats tests/test_cf_api.bats`
Expected: 7 tests pass.

- [ ] **Step 5: Run shellcheck**

Run: `shellcheck scripts/lib/cf_api.sh`
Expected: no output (may need `# shellcheck disable=SC2155` for some lines; add minimally if needed).

- [ ] **Step 6: Commit**

```bash
git add scripts/lib/cf_api.sh tests/test_cf_api.bats
git commit -m "feat(lib): Cloudflare API wrappers (zones, tunnels, DNS)"
```

---

## Task 7: Xray Config Template

**Files:**
- Create: `xray/config.template.json`

- [ ] **Step 1: Write template**

Content of `/opt/cf-vpn/xray/config.template.json`:
```json
{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {
      "tag": "vless-ws",
      "listen": "0.0.0.0",
      "port": 10001,
      "protocol": "vless",
      "settings": {
        "clients": [
          {"id": "${UUID_USER1}", "email": "${USER1_NAME}@vpn"}
        ],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "ws",
        "wsSettings": {"path": "/vless"}
      }
    },
    {
      "tag": "trojan-ws",
      "listen": "0.0.0.0",
      "port": 10002,
      "protocol": "trojan",
      "settings": {
        "clients": [
          {"password": "${TROJAN_PASS_USER1}", "email": "${USER1_NAME}@vpn"}
        ]
      },
      "streamSettings": {
        "network": "ws",
        "wsSettings": {"path": "/trojan"}
      }
    }
  ],
  "outbounds": [
    {"tag": "direct", "protocol": "freedom"},
    {"tag": "block", "protocol": "blackhole"}
  ],
  "routing": {
    "rules": [
      {"type": "field", "ip": ["geoip:private"], "outboundTag": "block"}
    ]
  }
}
```

- [ ] **Step 2: Verify template renders to valid JSON**

Run:
```bash
USER1_NAME=alice UUID_USER1=11111111-2222-3333-4444-555555555555 TROJAN_PASS_USER1=secret \
  envsubst < xray/config.template.json | jq . >/dev/null && echo OK
```
Expected: `OK`.

- [ ] **Step 3: Commit**

```bash
git add xray/config.template.json
git commit -m "feat: Xray config template (VLESS+Trojan WS inbounds, private-IP block)"
```

---

## Task 8: Cloudflared Config Template

**Files:**
- Create: `cloudflared/config.template.yml`

- [ ] **Step 1: Write template**

Content of `/opt/cf-vpn/cloudflared/config.template.yml`:
```yaml
tunnel: ${TUNNEL_UUID}
credentials-file: /etc/cloudflared/${TUNNEL_UUID}.json

ingress:
  - hostname: ${DOMAIN}
    path: ^/vless$
    service: http://xray:10001
    originRequest:
      noTLSVerify: true
  - hostname: ${DOMAIN}
    path: ^/trojan$
    service: http://xray:10002
    originRequest:
      noTLSVerify: true
  - service: http_status:404
```

- [ ] **Step 2: Verify template renders**

Run:
```bash
DOMAIN=vpn.example.com TUNNEL_UUID=deadbeef-1234 envsubst < cloudflared/config.template.yml
```
Expected: valid YAML with substituted values.

- [ ] **Step 3: Commit**

```bash
git add cloudflared/config.template.yml
git commit -m "feat: cloudflared ingress template (path routing + 404 fallback)"
```

---

## Task 9: Docker Compose

**Files:**
- Create: `docker-compose.yml`
- Create: `.env.example`

- [ ] **Step 1: Write `.env.example`**

Content of `/opt/cf-vpn/.env.example`:
```bash
# Cloudflare
# Required scopes: Zone:DNS:Edit (on your zone), Account:Cloudflare Tunnel:Edit
CF_API_TOKEN=
CF_ACCOUNT_ID=

# Domain for tunnel (e.g., vpn.example.com). Must be a subdomain of a zone in CF_ACCOUNT_ID.
DOMAIN=

# Populated by scripts/install.sh — do not edit by hand.
TUNNEL_UUID=
USER1_NAME=user1
UUID_USER1=
TROJAN_PASS_USER1=

# Pinned image tags. install.sh fills latest stable if empty.
XRAY_VERSION=24.11.30
CLOUDFLARED_VERSION=2025.2.0
```

- [ ] **Step 2: Write `docker-compose.yml`**

Content of `/opt/cf-vpn/docker-compose.yml`:
```yaml
services:
  xray:
    image: teddysun/xray:${XRAY_VERSION}
    container_name: cf-vpn-xray
    restart: unless-stopped
    read_only: true
    tmpfs:
      - /tmp
    volumes:
      - ./xray/config.json:/etc/xray/config.json:ro
    networks:
      - cfvpn_net
    healthcheck:
      test: ["CMD-SHELL", "ss -tln 2>/dev/null | grep -q ':10001' || nc -z 127.0.0.1 10001"]
      interval: 10s
      timeout: 3s
      retries: 3
      start_period: 10s
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"

  cloudflared:
    image: cloudflare/cloudflared:${CLOUDFLARED_VERSION}
    container_name: cf-vpn-cloudflared
    restart: unless-stopped
    command: tunnel --config /etc/cloudflared/config.yml run
    volumes:
      - ./cloudflared/config.yml:/etc/cloudflared/config.yml:ro
      - ./cloudflared/${TUNNEL_UUID}.json:/etc/cloudflared/${TUNNEL_UUID}.json:ro
    networks:
      - cfvpn_net
    depends_on:
      xray:
        condition: service_started
    healthcheck:
      test: ["CMD", "cloudflared", "tunnel", "info", "${TUNNEL_UUID}"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 15s
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"

networks:
  cfvpn_net:
    driver: bridge
```

- [ ] **Step 3: Verify compose config is valid**

Run (with stub .env values):
```bash
cp .env.example .env
sed -i 's/^TUNNEL_UUID=$/TUNNEL_UUID=stub-uuid/' .env
docker compose config >/dev/null && echo OK
rm -f .env
```
Expected: `OK`.

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml .env.example
git commit -m "feat: docker-compose (xray+cloudflared, healthchecks, log rotation)"
```

---

## Task 10: `install.sh` — Prereq Check & Secret Generation

**Files:**
- Create: `scripts/install.sh`

This task implements the first half of install.sh: dependency check, env loading, secret generation. Tunnel creation comes in Task 11.

- [ ] **Step 1: Write initial script**

Content of `/opt/cf-vpn/scripts/install.sh`:
```bash
#!/usr/bin/env bash
# Bootstrap the cf-vpn stack. Idempotent — safe to re-run.
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
```

- [ ] **Step 2: Run shellcheck**

Run: `shellcheck -x scripts/install.sh`
Expected: no output.

- [ ] **Step 3: Dry-run test with stub env**

```bash
cp .env.example .env
sed -i 's/^CF_API_TOKEN=$/CF_API_TOKEN=stub/' .env
sed -i 's/^CF_ACCOUNT_ID=$/CF_ACCOUNT_ID=stub/' .env
sed -i 's|^DOMAIN=$|DOMAIN=vpn.example.com|' .env
bash scripts/install.sh
```

Expected: script runs to completion, prints "prereq + secrets ready", and `.env` now contains non-empty `UUID_USER1`, `TROJAN_PASS_USER1`, `USER1_NAME=user1`.

Verify idempotency:
```bash
uuid1=$(grep ^UUID_USER1= .env | cut -d= -f2)
bash scripts/install.sh
uuid2=$(grep ^UUID_USER1= .env | cut -d= -f2)
[ "$uuid1" = "$uuid2" ] && echo "idempotent OK"
```

Cleanup: `rm .env`.

- [ ] **Step 4: Commit**

```bash
git add scripts/install.sh
git commit -m "feat(install): prereq check + secret generation (idempotent)"
```

---

## Task 11: `install.sh` — Tunnel + DNS + Render + Compose Up

**Files:**
- Modify: `scripts/install.sh` (append to `main`)

- [ ] **Step 1: Extend install.sh with tunnel + DNS logic**

Insert these functions into `/opt/cf-vpn/scripts/install.sh` **before** `main()`:

```bash
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
```

Replace the existing `main` with:
```bash
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
```

- [ ] **Step 2: Run shellcheck**

Run: `shellcheck -x scripts/install.sh`
Expected: no output.

- [ ] **Step 3: Integration test (manual, with real CF account)**

**Đại ca chạy:**
```bash
# Prerequisite: fill .env with real CF_API_TOKEN, CF_ACCOUNT_ID, DOMAIN
bash scripts/install.sh
```

Expected success output (tail):
```
[cf-vpn] tunnel 'cf-vpn-...' exists: <uuid>   (or "tunnel created")
[cf-vpn] DNS CNAME ...
[cf-vpn] templates rendered
NAME                  STATUS
cf-vpn-xray           Up (healthy)
cf-vpn-cloudflared    Up (healthy)
[cf-vpn] probe OK: /vless returned 400
```

- [ ] **Step 4: Verify idempotency**

Run: `bash scripts/install.sh` **a second time**.
Expected: same output, no new tunnel created, no error, services remain Up.

- [ ] **Step 5: Commit**

```bash
git add scripts/install.sh
git commit -m "feat(install): tunnel+DNS+render+compose up (idempotent orchestration)"
```

---

## Task 12: `gen-subscription.sh`

**Files:**
- Create: `scripts/gen-subscription.sh`

- [ ] **Step 1: Write script**

Content of `/opt/cf-vpn/scripts/gen-subscription.sh`:
```bash
#!/usr/bin/env bash
# Print VLESS + Trojan URIs, base64 subscription, and QR codes for a user.
# Usage: gen-subscription.sh [user-name]  (default: $USER1_NAME from .env)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/xray_config.sh
source "$SCRIPT_DIR/lib/xray_config.sh"
# shellcheck source=lib/uri.sh
source "$SCRIPT_DIR/lib/uri.sh"

ENV_FILE="$PROJECT_ROOT/.env"
CONFIG_FILE="$PROJECT_ROOT/xray/config.json"

[ -f "$ENV_FILE" ] || die ".env not found"
[ -f "$CONFIG_FILE" ] || die "xray/config.json not found — run install.sh first"

set -a
# shellcheck source=/dev/null
source "$ENV_FILE"
set +a

NAME="${1:-${USER1_NAME:-user1}}"

uuid=$(get_client_uuid "$CONFIG_FILE" "$NAME")
pw=$(get_client_password "$CONFIG_FILE" "$NAME")
[ -n "$uuid" ] || die "no vless client for user '$NAME'"
[ -n "$pw" ] || die "no trojan client for user '$NAME'"

vless_uri=$(build_vless_uri "$uuid" "$DOMAIN" "$NAME")
trojan_uri=$(build_trojan_uri "$pw" "$DOMAIN" "$NAME")
sub_b64=$(build_subscription_b64 "$vless_uri" "$trojan_uri")

mkdir -p "$PROJECT_ROOT/subscriptions"
OUT="$PROJECT_ROOT/subscriptions/${NAME}.txt"
{
  printf '# User: %s\n' "$NAME"
  printf '# Domain: %s\n\n' "$DOMAIN"
  printf '## VLESS\n%s\n\n' "$vless_uri"
  printf '## Trojan\n%s\n\n' "$trojan_uri"
  printf '## Base64 Subscription (paste into v2rayN/v2rayNG "Add Subscription")\n%s\n' "$sub_b64"
} > "$OUT"
chmod 600 "$OUT"

log "saved to $OUT"
echo
echo "=== VLESS URI ==="
echo "$vless_uri"
echo
echo "=== VLESS QR ==="
printf '%s' "$vless_uri" | qrencode -t UTF8
echo
echo "=== Trojan URI ==="
echo "$trojan_uri"
echo
echo "=== Trojan QR ==="
printf '%s' "$trojan_uri" | qrencode -t UTF8
echo
echo "=== Base64 Subscription ==="
echo "$sub_b64"
```

- [ ] **Step 2: Make executable**

Run: `chmod +x scripts/gen-subscription.sh`

- [ ] **Step 3: Run shellcheck**

Run: `shellcheck -x scripts/gen-subscription.sh`
Expected: no output.

- [ ] **Step 4: Smoke test (requires Task 11 completed)**

Run: `bash scripts/gen-subscription.sh`
Expected: prints VLESS URI, VLESS QR, Trojan URI, Trojan QR, base64 sub. Creates `subscriptions/user1.txt`.

- [ ] **Step 5: Commit**

```bash
git add scripts/gen-subscription.sh
git commit -m "feat: gen-subscription.sh (URIs + base64 sub + terminal QR)"
```

---

## Task 13: `add-user.sh`

**Files:**
- Create: `scripts/add-user.sh`

- [ ] **Step 1: Write script**

Content of `/opt/cf-vpn/scripts/add-user.sh`:
```bash
#!/usr/bin/env bash
# Add a new user to xray config, restart xray, print subscription.
# Usage: add-user.sh <name>
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/xray_config.sh
source "$SCRIPT_DIR/lib/xray_config.sh"

CONFIG_FILE="$PROJECT_ROOT/xray/config.json"
MAX_USERS=5

NAME="${1:-}"
[ -n "$NAME" ] || die "usage: add-user.sh <name>"
[[ "$NAME" =~ ^[a-zA-Z0-9_-]{1,32}$ ]] || die "name must match [A-Za-z0-9_-], 1-32 chars"
[ -f "$CONFIG_FILE" ] || die "xray/config.json not found — run install.sh first"

current=$(count_clients "$CONFIG_FILE")
if [ "$current" -ge "$MAX_USERS" ]; then
  die "user cap reached ($MAX_USERS). Remove a user first with remove-user.sh"
fi

existing_names=$(list_client_names "$CONFIG_FILE")
if grep -qxF "$NAME" <<<"$existing_names"; then
  die "user '$NAME' already exists"
fi

uuid=$(uuidgen)
password=$(openssl rand -base64 24 | tr -d '\n')

add_client "$CONFIG_FILE" "$NAME" "$uuid" "$password"
log "added user '$NAME' (uuid=$uuid)"

cd "$PROJECT_ROOT"
docker compose restart xray
log "xray restarted"

exec "$SCRIPT_DIR/gen-subscription.sh" "$NAME"
```

- [ ] **Step 2: Make executable + shellcheck**

Run:
```bash
chmod +x scripts/add-user.sh
shellcheck -x scripts/add-user.sh
```
Expected: no output from shellcheck.

- [ ] **Step 3: Smoke test (requires Task 11 completed)**

Run:
```bash
bash scripts/add-user.sh alice
```
Expected: adds user alice, restarts xray (~2s), prints alice's subscription.

Verify:
```bash
jq '.inbounds[] | select(.tag=="vless-ws") | .settings.clients | map(.email)' xray/config.json
```
Expected: `["user1@vpn", "alice@vpn"]`.

- [ ] **Step 4: Test duplicate rejection**

Run: `bash scripts/add-user.sh alice`
Expected: exits non-zero with "user 'alice' already exists".

- [ ] **Step 5: Test cap enforcement**

Temporarily set `MAX_USERS=2` in the script, run:
```bash
bash scripts/add-user.sh bob
bash scripts/add-user.sh carol   # should fail: cap reached
```
Revert MAX_USERS to 5. Remove any test users manually before committing.

- [ ] **Step 6: Commit**

```bash
git add scripts/add-user.sh
git commit -m "feat: add-user.sh (validation, cap, restart, subscription print)"
```

---

## Task 14: `remove-user.sh`

**Files:**
- Create: `scripts/remove-user.sh`

- [ ] **Step 1: Write script**

Content of `/opt/cf-vpn/scripts/remove-user.sh`:
```bash
#!/usr/bin/env bash
# Remove a user from xray config and restart xray.
# Usage: remove-user.sh <name>
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/xray_config.sh
source "$SCRIPT_DIR/lib/xray_config.sh"

CONFIG_FILE="$PROJECT_ROOT/xray/config.json"

NAME="${1:-}"
[ -n "$NAME" ] || die "usage: remove-user.sh <name>"
[ -f "$CONFIG_FILE" ] || die "xray/config.json not found"

existing_names=$(list_client_names "$CONFIG_FILE")
if ! grep -qxF "$NAME" <<<"$existing_names"; then
  die "user '$NAME' not found"
fi

remove_client "$CONFIG_FILE" "$NAME"
log "removed user '$NAME'"

# Also remove subscription file
rm -f "$PROJECT_ROOT/subscriptions/${NAME}.txt"

cd "$PROJECT_ROOT"
docker compose restart xray
log "xray restarted"
```

- [ ] **Step 2: Make executable + shellcheck**

Run:
```bash
chmod +x scripts/remove-user.sh
shellcheck -x scripts/remove-user.sh
```

- [ ] **Step 3: Smoke test**

Run (following Task 13's alice):
```bash
bash scripts/remove-user.sh alice
jq '.inbounds[] | select(.tag=="vless-ws") | .settings.clients | length' xray/config.json
```
Expected: `1` (only user1 remains).

Test not-found:
```bash
bash scripts/remove-user.sh ghost
```
Expected: exits non-zero with "user 'ghost' not found".

- [ ] **Step 4: Commit**

```bash
git add scripts/remove-user.sh
git commit -m "feat: remove-user.sh (no-op on missing, restart xray)"
```

---

## Task 15: `healthcheck.sh` + Cron Installer

**Files:**
- Create: `scripts/healthcheck.sh`

- [ ] **Step 1: Write script**

Content of `/opt/cf-vpn/scripts/healthcheck.sh`:
```bash
#!/usr/bin/env bash
# Probe the tunnel from the VPS itself. On 3 consecutive failures, restart.
# Intended to run via cron every 5 minutes.
# Usage: healthcheck.sh            (run probe, restart if needed)
#        healthcheck.sh --install  (install cron entry)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"

ENV_FILE="$PROJECT_ROOT/.env"
STATE_FILE="$PROJECT_ROOT/.healthcheck.state"
LOG_FILE="/var/log/cf-vpn-health.log"
FAIL_THRESHOLD=3

install_cron() {
  local script_path="$SCRIPT_DIR/healthcheck.sh"
  local line="*/5 * * * * $script_path >> $LOG_FILE 2>&1"
  # Remove any existing cf-vpn lines first (idempotent)
  local tmp
  tmp=$(mktemp)
  crontab -l 2>/dev/null | grep -v 'healthcheck.sh' > "$tmp" || true
  printf '%s\n' "$line" >> "$tmp"
  crontab "$tmp"
  rm -f "$tmp"
  log "installed cron: $line"
}

if [ "${1:-}" = "--install" ]; then
  install_cron
  exit 0
fi

[ -f "$ENV_FILE" ] || die ".env not found"
set -a
# shellcheck source=/dev/null
source "$ENV_FILE"
set +a
[ -n "${DOMAIN:-}" ] || die "DOMAIN not set"

code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "https://${DOMAIN}/vless" || printf '000')
ts=$(date -Iseconds)

case "$code" in
  400|426)
    # Healthy — reset counter
    printf '0' > "$STATE_FILE"
    printf '[%s] OK code=%s\n' "$ts" "$code"
    ;;
  *)
    fails=0
    [ -f "$STATE_FILE" ] && fails=$(cat "$STATE_FILE")
    fails=$((fails + 1))
    printf '%d' "$fails" > "$STATE_FILE"
    printf '[%s] FAIL code=%s fails=%d\n' "$ts" "$code" "$fails"
    if [ "$fails" -ge "$FAIL_THRESHOLD" ]; then
      printf '[%s] RESTART triggered after %d consecutive failures\n' "$ts" "$fails"
      cd "$PROJECT_ROOT"
      docker compose restart
      printf '0' > "$STATE_FILE"
    fi
    ;;
esac
```

- [ ] **Step 2: Make executable + shellcheck**

Run:
```bash
chmod +x scripts/healthcheck.sh
shellcheck -x scripts/healthcheck.sh
```

- [ ] **Step 3: Smoke test**

Healthy probe (after Task 11):
```bash
bash scripts/healthcheck.sh
cat .healthcheck.state
```
Expected: `[timestamp] OK code=400` printed; state file contains `0`.

Simulate failure:
```bash
# Temporarily break env
sed -i.bak 's|^DOMAIN=.*|DOMAIN=definitely-not-a-domain.invalid|' .env
bash scripts/healthcheck.sh; bash scripts/healthcheck.sh; bash scripts/healthcheck.sh
# After 3 fails, should trigger restart
mv .env.bak .env
```

Expected: 3 FAIL lines, then RESTART line. Revert .env.

- [ ] **Step 4: Install cron**

Run: `bash scripts/healthcheck.sh --install`
Verify: `crontab -l | grep healthcheck.sh`
Expected: line `*/5 * * * * /opt/cf-vpn/scripts/healthcheck.sh >> /var/log/cf-vpn-health.log 2>&1`.

- [ ] **Step 5: Add state file to .gitignore**

Append to `/opt/cf-vpn/.gitignore`:
```
.healthcheck.state
```

- [ ] **Step 6: Commit**

```bash
git add scripts/healthcheck.sh .gitignore
git commit -m "feat: healthcheck.sh (probe + auto-restart + cron installer)"
```

---

## Task 16: `rotate-domain.sh`

**Files:**
- Create: `scripts/rotate-domain.sh`

- [ ] **Step 1: Write script**

Content of `/opt/cf-vpn/scripts/rotate-domain.sh`:
```bash
#!/usr/bin/env bash
# Rotate the tunnel to a new domain (must be in the same CF account).
# Keeps the old tunnel for 24h grace — caller can run with --cleanup <old-uuid> later.
# Usage: rotate-domain.sh <new-domain>
#        rotate-domain.sh --cleanup <tunnel-uuid>
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/cf_api.sh
source "$SCRIPT_DIR/lib/cf_api.sh"

ENV_FILE="$PROJECT_ROOT/.env"

usage() {
  cat <<EOF
usage: rotate-domain.sh <new-domain>
       rotate-domain.sh --cleanup <old-tunnel-uuid>
EOF
  exit 1
}

[ $# -ge 1 ] || usage
[ -f "$ENV_FILE" ] || die ".env not found"
set -a
# shellcheck source=/dev/null
source "$ENV_FILE"
set +a

if [ "$1" = "--cleanup" ]; then
  [ -n "${2:-}" ] || usage
  log "deleting old tunnel $2"
  delete_tunnel "$2"
  rm -f "$PROJECT_ROOT/cloudflared/${2}.json"
  log "cleanup complete"
  exit 0
fi

NEW_DOMAIN="$1"
OLD_DOMAIN="${DOMAIN:-}"
OLD_TUNNEL="${TUNNEL_UUID:-}"

[ -n "$OLD_DOMAIN" ] || die "no current DOMAIN in .env — run install.sh first"
[ "$NEW_DOMAIN" != "$OLD_DOMAIN" ] || die "new-domain matches current DOMAIN"

# Validate new domain has a zone in this account
log "validating $NEW_DOMAIN belongs to CF account"
get_zone_id "$NEW_DOMAIN" >/dev/null

log "rotating: $OLD_DOMAIN -> $NEW_DOMAIN (old tunnel $OLD_TUNNEL kept for grace)"

# Swap DOMAIN in .env, then run install.sh (it will create a new tunnel since name differs)
env_write "$ENV_FILE" "DOMAIN" "$NEW_DOMAIN"
# Force install to create a new tunnel by clearing TUNNEL_UUID
env_write "$ENV_FILE" "TUNNEL_UUID" ""

bash "$SCRIPT_DIR/install.sh"

log "rotation complete. Old tunnel $OLD_TUNNEL is still active."
log "After verifying clients work on $NEW_DOMAIN, run:"
log "  bash scripts/rotate-domain.sh --cleanup $OLD_TUNNEL"

# Regenerate subscriptions for all existing users
source "$SCRIPT_DIR/lib/xray_config.sh"
while IFS= read -r name; do
  bash "$SCRIPT_DIR/gen-subscription.sh" "$name" >/dev/null
  log "regenerated subscription for $name"
done < <(list_client_names "$PROJECT_ROOT/xray/config.json")
```

- [ ] **Step 2: Make executable + shellcheck**

Run:
```bash
chmod +x scripts/rotate-domain.sh
shellcheck -x scripts/rotate-domain.sh
```

- [ ] **Step 3: Smoke test (Đại ca manual, on a spare domain)**

```bash
# Assume current DOMAIN=vpn.a.com, test rotation to vpn.b.com
bash scripts/rotate-domain.sh vpn.b.com
```
Expected: new tunnel created, DNS on b.com points to new tunnel, subscriptions regenerated, old tunnel still active.

Verify new domain works via client import, then:
```bash
bash scripts/rotate-domain.sh --cleanup <old-tunnel-uuid>
```

- [ ] **Step 4: Commit**

```bash
git add scripts/rotate-domain.sh
git commit -m "feat: rotate-domain.sh (swap domain + grace cleanup)"
```

---

## Task 17: Manual Testing Checklist

**Files:**
- Create: `docs/TESTING.md`

- [ ] **Step 1: Write testing checklist**

Content of `/opt/cf-vpn/docs/TESTING.md`:
```markdown
# Testing Checklist — cf-vpn

Run these checks after `install.sh` completes and before declaring a deploy healthy.

## 1. Local Smoke Test (on VPS)

- [ ] `docker compose ps` — both `cf-vpn-xray` and `cf-vpn-cloudflared` show `Up (healthy)`
- [ ] `docker compose exec xray xray version` — prints Xray version
- [ ] `docker compose logs cloudflared 2>&1 | grep -c "Registered tunnel connection"` — returns ≥1 (typically 2-4 connections)
- [ ] `curl -I -s https://${DOMAIN}/` — returns HTTP 404
- [ ] `curl -I -s https://${DOMAIN}/vless` — returns HTTP 400 or 426
- [ ] `curl -I -s https://${DOMAIN}/trojan` — returns HTTP 400 or 426

## 2. Port Scan Check

From a machine **outside** the VPS:
- [ ] `nmap -Pn -p- <vps-ip>` — only SSH port open, nothing else

## 3. End-to-End Client Test (at least one platform)

**Windows (v2rayN):**
- [ ] Import subscription from `subscriptions/<user>.txt` (paste base64)
- [ ] Enable VLESS outbound → `curl https://ifconfig.me` via proxy — IP differs from VPS IP
- [ ] Switch to Trojan outbound → same test, different CF egress IP possible but not same as VPS
- [ ] Browse to `https://www.youtube.com` — loads

**iOS (Shadowrocket):**
- [ ] Scan VLESS QR → connect → `ifconfig.me` in Safari
- [ ] Scan Trojan QR → connect → same test

**Android (v2rayNG):**
- [ ] Import VLESS URI → connect → `Connection test` in app returns 200
- [ ] Import Trojan URI → same test

## 4. DNS Leak Test

- [ ] Connect via VPN on any client → visit `https://dnsleaktest.com/` → only Cloudflare DNS shown

## 5. Latency Baseline

Record for reference (ping via proxy to 1.1.1.1):
- From VN/SG: target <100ms
- From CN: target <150ms
- From UAE: target <200ms

| Region | Latency | Date tested |
|---|---|---|
| VN | TBD | TBD |
| CN | TBD | TBD |
| UAE | TBD | TBD |

## 6. Bypass Verification

**China:**
- [ ] google.com loads
- [ ] youtube.com plays video
- [ ] 24h stability: check again after 24 hours — still connects

**UAE:**
- [ ] facebook.com loads
- [ ] WhatsApp calls work (if UDP not required; WhatsApp chat only here)
- [ ] 24h stability

## 7. Scripts Idempotency

- [ ] `bash scripts/install.sh` twice in a row — second run completes without creating new tunnel
- [ ] `bash scripts/add-user.sh alice` then `bash scripts/add-user.sh alice` — second fails with "already exists"
- [ ] `bash scripts/remove-user.sh alice` then again — second fails with "not found"

## 8. Failure Recovery

- [ ] `docker compose stop cloudflared` → wait 30s → probe `curl -I https://${DOMAIN}/vless` returns 5xx → `docker compose start cloudflared` → probe OK within 1 min
- [ ] `docker compose stop xray` → wait 30s → probe returns 502 → healthcheck.sh after 15 min triggers restart automatically
- [ ] `reboot` the VPS → after boot, `docker compose ps` shows stack Up within 60s

## 9. User Management

- [ ] `bash scripts/add-user.sh alice` → alice receives working subscription
- [ ] Add 4 more users (total 5) → all 5 work
- [ ] `bash scripts/add-user.sh sixth` → fails with "user cap reached"
- [ ] `bash scripts/remove-user.sh alice` → alice's old config no longer authenticates (verify by trying old URI → connection fails)

## 10. Domain Rotation

- [ ] `bash scripts/rotate-domain.sh vpn.b.com` → new tunnel + DNS created
- [ ] New subscription (regenerated) works on a client
- [ ] Old subscription still works (24h grace)
- [ ] `bash scripts/rotate-domain.sh --cleanup <old-tunnel-uuid>` → old tunnel deleted, old subscription stops working
```

- [ ] **Step 2: Commit**

```bash
git add docs/TESTING.md
git commit -m "docs: manual testing checklist (smoke, E2E, bypass, failure recovery)"
```

---

## Task 18: Final README

**Files:**
- Modify: `README.md` (replace stub)

- [ ] **Step 1: Write full README**

Content of `/opt/cf-vpn/README.md`:
```markdown
# cf-vpn

Personal VPN over Cloudflare Tunnel. VLESS + Trojan protocols on WebSocket. Designed for 1-5 users bypassing GFW/UAE firewalls.

## Quick Start

**Prerequisites:**
- Linux VPS with Docker + Docker Compose v2
- `jq`, `curl`, `openssl`, `uuid-runtime`, `gettext` (envsubst), `qrencode` installed
- Cloudflare account with a domain zone already configured
- CF API token with scopes: `Zone:DNS:Edit`, `Account:Cloudflare Tunnel:Edit`

**Install:**
```bash
cd /opt/cf-vpn
cp .env.example .env
# Edit .env: set CF_API_TOKEN, CF_ACCOUNT_ID, DOMAIN (e.g. vpn.example.com)
bash scripts/install.sh
```

On success, `install.sh` prints the subscription for `user1`. To print again:
```bash
bash scripts/gen-subscription.sh
```

**Install cron healthcheck:**
```bash
bash scripts/healthcheck.sh --install
```

## Daily Ops

| Task | Command |
|---|---|
| Add user | `bash scripts/add-user.sh <name>` (max 5) |
| Remove user | `bash scripts/remove-user.sh <name>` |
| Print user subscription | `bash scripts/gen-subscription.sh <name>` |
| Check status | `docker compose ps` |
| View logs | `docker compose logs -f xray` / `-f cloudflared` |
| Restart | `docker compose restart` |
| Health probe | `bash scripts/healthcheck.sh` |
| Rotate to new domain | `bash scripts/rotate-domain.sh <new-domain>` |
| Cleanup old tunnel after rotation | `bash scripts/rotate-domain.sh --cleanup <uuid>` |

## Verify Installation

```bash
docker compose ps                           # both Up (healthy)
curl -I https://${DOMAIN}/                  # expect 404
curl -I https://${DOMAIN}/vless             # expect 400 or 426
docker compose logs cloudflared | grep "Registered tunnel"
bash scripts/healthcheck.sh                 # expect "OK code=400"
```

Full checklist: [docs/TESTING.md](docs/TESTING.md).

## Development

```bash
make lint      # shellcheck all scripts
make test      # run bats unit tests
make all       # lint + test
```

## Architecture

See [docs/superpowers/specs/2026-04-19-cf-vpn-design.md](docs/superpowers/specs/2026-04-19-cf-vpn-design.md) for full design.

TL;DR:
- Client → CF edge (TLS, WSS) → cloudflared (HTTP/2 tunnel, outbound only) → Xray (VLESS/Trojan WS) → internet
- VPS has no inbound ports. CF Tunnel hides the VPS IP. Path routing at cloudflared ingress (`/vless`, `/trojan`, fallback 404).

## Security

- VPS: `ufw default deny incoming`, only SSH allowed
- Containers: `read_only: true`, no docker socket mount
- `.env`, credentials JSON, subscriptions: all `.gitignore`d with `chmod 600`
- Xray routes `geoip:private` → blackhole (no LAN scanning through tunnel)

## Files of Note

```
.env                         # Secrets (gitignored). Edit for CF creds + DOMAIN.
docker-compose.yml           # Service definitions
xray/config.json             # Active Xray config (generated; edited by add/remove-user)
cloudflared/config.yml       # Active cloudflared config (generated)
cloudflared/<uuid>.json      # Tunnel credentials (generated, chmod 600)
subscriptions/               # Per-user subscription files (generated, chmod 600)
```

## Troubleshooting

**`install.sh` fails at "cf api error: 1000: Invalid API token"**
→ Token scopes wrong. Recreate with `Zone:DNS:Edit` + `Account:Cloudflare Tunnel:Edit`.

**`curl https://${DOMAIN}/vless` returns 530 or 502**
→ Tunnel not yet connected. Check `docker compose logs cloudflared` for "Registered tunnel connection". Wait 1-2 min.

**Client connects but no internet**
→ Check `docker compose logs xray` for auth failures. Verify UUID/password in client matches `xray/config.json`.

**Domain blocked in CN/UAE**
→ Rotate: `bash scripts/rotate-domain.sh <another-domain-in-your-cf-account>`.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: full README (quick start, ops, troubleshooting)"
```

---

## Task 19: End-to-End Integration Test

This task is a **manual gate** run by Đại ca on the real VPS with a real CF account. No code changes.

- [ ] **Step 1: Fresh install on a clean VPS**

Pick a sacrificial subdomain (e.g. `vpntest.<yourdomain>`), a domain you won't mind experimenting with.

```bash
cd /opt/cf-vpn
cp .env.example .env
# Edit .env with real CF_API_TOKEN, CF_ACCOUNT_ID, DOMAIN
bash scripts/install.sh
```

Run through the full checklist in `docs/TESTING.md` sections 1, 2, 3, 4, 7.

- [ ] **Step 2: Run bypass verification**

If Đại ca has access to a Chinese or UAE IP (VPN test node, friend in-region, etc.), run section 6.

If not, document the untested status in TESTING.md and plan a follow-up when someone in-region can test.

- [ ] **Step 3: Run for 24h under normal use**

Leave stack running 24h, check:
- `crontab -l` still shows healthcheck entry
- `/var/log/cf-vpn-health.log` shows periodic OK entries
- `docker compose logs --since 24h xray | grep -c 'ERROR'` — ideally 0

- [ ] **Step 4: Final commit with test results**

Fill in TESTING.md latency table and mark completed checkboxes. Commit:
```bash
git add docs/TESTING.md
git commit -m "test: E2E verification on production (filled checklist)"
```

---

## Task 20: Run `make all` & Final Cleanup

- [ ] **Step 1: Run full lint + test**

Run: `cd /opt/cf-vpn && make all`
Expected: shellcheck clean, all bats tests pass.

- [ ] **Step 2: Verify git state clean**

Run: `git status`
Expected: `working tree clean`.

Run: `git log --oneline`
Expected: chronological history with clear messages, roughly 18-20 commits.

- [ ] **Step 3: Archive plan and spec completion**

Confirm these exist and are committed:
- `docs/superpowers/specs/2026-04-19-cf-vpn-design.md`
- `docs/superpowers/plans/2026-04-19-cf-vpn-implementation.md`
- `docs/TESTING.md`
- `README.md`

No further commit.

---

## Summary of Deliverables

- 10 shell scripts (4 lib, 6 ops) — all shellcheck-clean
- 4 bats test files — all passing
- Docker Compose stack (xray + cloudflared)
- 2 config templates (Xray, cloudflared)
- 3 doc files (README, TESTING, spec+plan)
- 1 Makefile

**Success:** `make all` passes, `install.sh` idempotent, client can import subscription and reach blocked sites from behind GFW/UAE firewalls.
