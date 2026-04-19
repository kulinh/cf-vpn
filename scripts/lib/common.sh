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
