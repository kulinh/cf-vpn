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

After install completes, print the subscription for `user1` with:
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
source .env
curl -I https://${DOMAIN}/                  # expect 404
curl -I https://${DOMAIN}/vless             # expect 400 or 426
docker compose logs cloudflared | grep "Registered tunnel"
bash scripts/healthcheck.sh                 # expect "OK code=400" or "OK code=426"
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
