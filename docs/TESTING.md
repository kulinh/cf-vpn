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
