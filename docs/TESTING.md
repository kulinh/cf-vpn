# Testing Checklist — cf-vpn

Run these checks after `sudo cfvpnctl install` completes and before declaring a deploy healthy.

## 0. Prerequisites

- [ ] Debian/Ubuntu VPS with systemd
- [ ] `curl`, `jq`, `openssl`, `uuidgen`, `qrencode` installed
- [ ] Cloudflare account with zone configured
- [ ] CF API token with scopes `Zone:DNS:Edit` + `Account:Cloudflare Tunnel:Edit`
- [ ] root access

## 1. Bootstrap

- [ ] `make build` succeeds (produces `bin/cfvpnctl`)
- [ ] `sudo install -m 0755 bin/cfvpnctl /usr/local/bin/cfvpnctl`
- [ ] `/etc/cfvpn/cfvpn.env` populated with `CF_API_TOKEN`, `CF_ACCOUNT_ID`, `DOMAIN`, `USER1_NAME` (chmod 600)
- [ ] `sudo cfvpnctl install` completes without error

## 2. Local Smoke Test (on VPS)

- [ ] `systemctl is-active cfvpn-xray cfvpn-cloudflared` — both report `active`
- [ ] `sudo cfvpnctl status` — reports both services active and tunnel registered
- [ ] `journalctl -u cfvpn-cloudflared 2>&1 | grep -c "Registered tunnel connection"` — returns ≥1 (typically 2-4)
- [ ] `curl -I -s https://${DOMAIN}/` — returns HTTP 404
- [ ] `curl -I -s https://${DOMAIN}/vless` — returns HTTP 400 or 426
- [ ] `curl -I -s https://${DOMAIN}/trojan` — returns HTTP 400 or 426
- [ ] `sudo cfvpnctl healthcheck run` — prints "OK code=400" or "OK code=426"

## 3. Port Scan Check

From a machine **outside** the VPS:
- [ ] `nmap -Pn -p- <vps-ip>` — only SSH port open, nothing else

## 4. End-to-End Client Test (at least one platform)

**Windows (v2rayN):**
- [ ] Import subscription from `/var/lib/cfvpn/subscriptions/<user>.txt` (paste base64)
- [ ] Enable VLESS outbound → `curl https://ifconfig.me` via proxy — IP differs from VPS IP
- [ ] Switch to Trojan outbound → same test, different CF egress IP possible but not same as VPS
- [ ] Browse to `https://www.youtube.com` — loads

**iOS (Shadowrocket):**
- [ ] Scan VLESS QR → connect → `ifconfig.me` in Safari
- [ ] Scan Trojan QR → connect → same test

**Android (v2rayNG):**
- [ ] Import VLESS URI → connect → `Connection test` in app returns 200
- [ ] Import Trojan URI → same test

## 5. DNS Leak Test

- [ ] Connect via VPN on any client → visit `https://dnsleaktest.com/` → only Cloudflare DNS shown

## 6. Latency Baseline

Record for reference (ping via proxy to 1.1.1.1):
- From VN/SG: target <100ms
- From CN: target <150ms
- From UAE: target <200ms

| Region | Latency | Date tested |
|---|---|---|
| VN | TBD | TBD |
| CN | TBD | TBD |
| UAE | TBD | TBD |

## 7. Bypass Verification

**China:**
- [ ] google.com loads
- [ ] youtube.com plays video
- [ ] 24h stability: check again after 24 hours — still connects

**UAE:**
- [ ] facebook.com loads
- [ ] WhatsApp calls work (if UDP not required; WhatsApp chat only here)
- [ ] 24h stability

## 8. Command Idempotency

- [ ] `sudo cfvpnctl install` twice in a row — second run completes without creating a new tunnel
- [ ] `sudo cfvpnctl add-user alice` then again — second fails with "already exists"
- [ ] `sudo cfvpnctl remove-user alice` then again — second fails with "not found"

## 9. Failure Recovery

- [ ] `sudo systemctl stop cfvpn-cloudflared` → wait 30s → probe `curl -I https://${DOMAIN}/vless` returns 5xx → `sudo systemctl start cfvpn-cloudflared` → probe OK within 1 min
- [ ] `sudo systemctl stop cfvpn-xray` → wait 30s → probe returns 502 → `cfvpnctl healthcheck run` reports failure; timer-driven recovery restarts unit
- [ ] `sudo systemctl reboot` the VPS → after boot, `systemctl is-active cfvpn-xray cfvpn-cloudflared` both `active` within 60s

## 10. User Management

- [ ] `sudo cfvpnctl add-user alice` → alice receives working subscription via `sudo cfvpnctl gen-sub alice`
- [ ] Add 4 more users (total 5) → all 5 work
- [ ] `sudo cfvpnctl add-user sixth` → fails with "user cap reached"
- [ ] `sudo cfvpnctl remove-user alice` → alice's old config no longer authenticates (verify by trying old URI → connection fails)

## 11. Domain Rotation

- [ ] `sudo cfvpnctl rotate-domain vpn.b.com` → new tunnel + DNS created
- [ ] New subscription (regenerated via `cfvpnctl gen-sub`) works on a client
- [ ] Old subscription still works (24h grace)
- [ ] `sudo cfvpnctl rotate-domain --cleanup <old-tunnel-uuid>` → old tunnel deleted, old subscription stops working
