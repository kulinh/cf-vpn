# cf-vpn

Personal VPN over Cloudflare Tunnel. VLESS + Trojan protocols on WebSocket. Designed for 1-5 users bypassing GFW/UAE firewalls.

## Quick Start

**Prerequisites:**
- Debian/Ubuntu VPS with systemd
- `curl`, `jq`, `openssl`, `uuidgen`, `qrencode` installed
- Cloudflare account with a domain zone already configured
- CF API token with scopes: `Zone:DNS:Edit`, `Account:Cloudflare Tunnel:Edit`
- root access

**Install:**
```bash
cd /opt/cf-vpn
make build
sudo install -m 0755 bin/cfvpnctl /usr/local/bin/cfvpnctl

sudo mkdir -p /etc/cfvpn
sudo install -m 0600 /dev/null /etc/cfvpn/cfvpn.env
sudo tee /etc/cfvpn/cfvpn.env >/dev/null <<'EOF'
CF_API_TOKEN=...
CF_ACCOUNT_ID=...
DOMAIN=vpn.example.com
USER1_NAME=user1
EOF
sudo chmod 600 /etc/cfvpn/cfvpn.env

sudo cfvpnctl install
```

After install completes, print the subscription for `user1` with:
```bash
sudo cfvpnctl gen-sub user1
```

**Install healthcheck timer:**
```bash
sudo cfvpnctl healthcheck install
```

## Daily Ops

| Task | Command |
|---|---|
| Add user | `sudo cfvpnctl add-user <name>` (max 5) |
| Remove user | `sudo cfvpnctl remove-user <name>` |
| Print user subscription | `sudo cfvpnctl gen-sub <name>` |
| Check status | `sudo cfvpnctl status` |
| View xray logs | `journalctl -u cfvpn-xray -f` |
| View cloudflared logs | `journalctl -u cfvpn-cloudflared -f` |
| Restart services | `sudo systemctl restart cfvpn-xray cfvpn-cloudflared` |
| Health probe | `sudo cfvpnctl healthcheck run` |
| Install healthcheck timer | `sudo cfvpnctl healthcheck install` |
| Rotate to new domain | `sudo cfvpnctl rotate-domain <new-domain>` |
| Cleanup old tunnel after rotation | `sudo cfvpnctl rotate-domain --cleanup <uuid>` |

## Verify Installation

```bash
systemctl is-active cfvpn-xray cfvpn-cloudflared   # both "active"
sudo cfvpnctl status
sudo cfvpnctl healthcheck run                       # expect "OK code=400" or "OK code=426"
```

Full checklist: [docs/TESTING.md](docs/TESTING.md).

Minimal VPS install guide: [docs/INSTALL_MINIMAL.md](docs/INSTALL_MINIMAL.md).

## Files of Note

```
/etc/cfvpn/cfvpn.env                       # Secrets (chmod 600): CF creds + DOMAIN + user state
/etc/cfvpn/xray/config.json                # Active Xray config (generated; edited by add/remove-user)
/etc/cfvpn/cloudflared/config.yml          # Active cloudflared config (generated)
/etc/cfvpn/cloudflared/<tunnel-uuid>.json  # Tunnel credentials (chmod 600)
/var/lib/cfvpn/subscriptions/<user>.txt    # Per-user subscription (chmod 600)
/var/lib/cfvpn/state/                      # Runtime state (rotation markers, etc.)
/etc/systemd/system/cfvpn-xray.service
/etc/systemd/system/cfvpn-cloudflared.service
/etc/systemd/system/cfvpn-healthcheck.service
/etc/systemd/system/cfvpn-healthcheck.timer
```

## Security

- VPS: `ufw default deny incoming`, only SSH allowed
- `/etc/cfvpn/cfvpn.env`, tunnel credentials, subscriptions: all `chmod 600`
- Xray routes `geoip:private` → blackhole (no LAN scanning through tunnel)

## Architecture

See [docs/superpowers/specs/2026-04-19-cf-vpn-design.md](docs/superpowers/specs/2026-04-19-cf-vpn-design.md) for full design.

TL;DR:
- Client → CF edge (TLS, WSS) → cloudflared (HTTP/2 tunnel, outbound only) → Xray (VLESS/Trojan WS) → internet
- VPS has no inbound ports. CF Tunnel hides the VPS IP. Path routing at cloudflared ingress (`/vless`, `/trojan`, fallback 404).

## Troubleshooting

**`cfvpnctl install` fails with "cf api error: 1000: Invalid API token"**
→ Token scopes wrong. Recreate with `Zone:DNS:Edit` + `Account:Cloudflare Tunnel:Edit`.

**`curl https://${DOMAIN}/vless` returns 530 or 502; "tunnel not registered"**
→ Tunnel not yet connected. Check `journalctl -u cfvpn-cloudflared` for "Registered tunnel connection". Wait 1-2 min.

**Client connects but no internet**
→ Check `journalctl -u cfvpn-xray` for auth failures. Verify UUID/password in client matches `/etc/cfvpn/xray/config.json`.

**Domain blocked in CN/UAE**
→ Rotate: `sudo cfvpnctl rotate-domain <another-domain-in-your-cf-account>`.

## Control Panel Frontend

```bash
npm --prefix panel/web install
npm --prefix panel/web dev
npm --prefix panel/web test -- --run
npm --prefix panel/web e2e
```

### Public subscription URL (`/sub/:token`)

The Users tab exposes a stable, token-based subscription URL per user at
`https://<panel-host>/sub/<32-hex-token>`. Shadowrocket and v2rayNG fetch this
URL on their own schedule, so adding or removing a node on the panel
automatically propagates to every installed client without requiring them to
re-import anything.

Because mobile apps cannot present a Cloudflare Access identity, the panel's
Access application **must bypass** `/sub/*`. In the Zero Trust dashboard, add a
policy to the panel Access app:

- Action: **Bypass**
- Path: `/sub/*` (include subdomain if your Access app is scoped wider)

The `/api/*` routes remain behind Access. The `/sub/:token` route itself
validates the 32-hex token against the `users.sub_token` column and returns
`404` for malformed or unknown tokens, so public exposure is safe.

## Development

```bash
make build     # build bin/cfvpnctl
make test      # go test ./...
make all       # test + build
```
