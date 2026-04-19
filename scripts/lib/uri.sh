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
