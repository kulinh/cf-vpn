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
