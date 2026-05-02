# cfvpn

Multi-VPS VPN control system: Cloudflare Worker backend with D1 storage, React admin panel, and per-node `cfvpn-agent` managing Xray (VLESS) + Hysteria2 via systemd.

## Architecture

```
Panel (React + Cloudflare Pages)
  └─ Worker API (Hono + D1) ──┐
                                │ Bearer
  cfvpn-agent (:6788) ◄────────┘  cloudflared tunnel (admin)
    ├─ xray (VLESS Reality / HTTPUpgrade) :10001
    ├─ hysteria2 (UDP salamander obfs)
    └─ cloudflared ingress → /api/v1/sync → 127.0.0.1:10001
```

**Direct mode** (port 443 reachable): VLESS + XTLS-Reality (Vision flow `xtls-rprx-vision`), TLS camouflage via `dest=www.microsoft.com:443`, X25519 keypair + 8-byte shortId per node. No Let's Encrypt cert needed.

**Cloudflare mode** (no inbound 443): VLESS + HTTPUpgrade on `127.0.0.1:10001`, path `/api/v1/sync`, tunneled via cloudflared.

**Hysteria2** runs alongside VLESS on both modes over UDP with salamander obfs.

## Build

```bash
go build -o bin/cfvpnctl ./cmd/cfvpnctl
go build -o bin/cfvpn-agent ./cmd/cfvpn-agent
```

## Fresh VPS install

```bash
sudo -E \
  CF_API_TOKEN='your_token' \
  CF_ACCOUNT_ID='your_account_id' \
  NODE_ID='hk-01' \
  bash scripts/install-node.sh
```

Port 443 is auto-detected — free → `direct+Reality`, blocked → `cloudflare+HTTPUpgrade`. Explicit override: `--mode=direct` or `--mode=cloudflare`.

## Upgrade an existing VPS

```bash
sudo cfvpnctl upgrade --mode auto      # auto-detect
sudo cfvpnctl upgrade --mode direct    # force Reality
sudo cfvpnctl upgrade --mode cloudflare # force HTTPUpgrade via tunnel
```

## Daily ops

| Task | Command |
|---|---|
| Add user | `sudo cfvpnctl add-user <name>` |
| Remove user | `sudo cfvpnctl remove-user <name>` |
| Print user subscription | `sudo cfvpnctl gen-sub <name>` |
| Check local status | `sudo cfvpnctl status` |
| Run healthcheck | `sudo cfvpnctl healthcheck run` |
| Install healthcheck timer | `sudo cfvpnctl healthcheck install` |
| Rotate node domains | `sudo cfvpnctl rotate-domain <new-domain>` |
| Cleanup old rotation tunnel | `sudo cfvpnctl rotate-domain --cleanup <uuid>` |
| View Xray logs | `journalctl -u cfvpn-xray -f` |
| View Hysteria2 logs | `journalctl -u cfvpn-hysteria -f` |
| View admin tunnel logs | `journalctl -u cfvpn-cloudflared -f` |

## Verify installation

```bash
systemctl is-active cfvpn-xray cfvpn-hysteria cfvpn-cloudflared
sudo cfvpnctl status
sudo cfvpnctl healthcheck run
```

## Files of note

```text
/etc/cfvpn/cfvpn.env                       # Secrets, runtime settings, Reality keys (chmod 600)
/etc/cfvpn/xray/config.json                # Generated VLESS config
/etc/cfvpn/hysteria/config.yaml            # Generated Hysteria2 config
/etc/cfvpn/cloudflared/config.yml          # Admin tunnel ingress
/etc/cfvpn/cloudflared/<tunnel-uuid>.json  # Tunnel credentials (chmod 600)
/var/lib/cfvpn/subscriptions/<user>.txt    # Per-user subscription (chmod 600)
/var/lib/cfvpn/state/                      # Runtime state
/etc/systemd/system/cfvpn-xray.service
/etc/systemd/system/cfvpn-hysteria.service
/etc/systemd/system/cfvpn-cloudflared.service
/etc/systemd/system/cfvpn-agent.service
/etc/systemd/system/cfvpn-healthcheck.service
/etc/systemd/system/cfvpn-healthcheck.timer
```

### cfvpn.env keys

| Key | Mode | Description |
|---|---|---|
| `MODE` | both | `direct` or `cloudflare` |
| `DOMAIN` | both | Public VPN hostname |
| `PUBLIC_IP` | direct | Detected IPv4 |
| `REALITY_PRIVATE_KEY` | direct | X25519 private key |
| `REALITY_PUBLIC_KEY` | direct | X25519 public key |
| `REALITY_SHORT_ID` | direct | 16-hex shortId for Reality |
| `REALITY_DEST` | direct | Fallback dest (e.g. `www.microsoft.com:443`) |
| `REALITY_SNI` | direct | Fallback SNI (e.g. `www.microsoft.com`) |
| `XHTTP_PATH` | cloudflare | VLESS path (`/api/v1/sync`) |
| `ADMIN_HOST` | both | cloudflared admin hostname |
| `ADMIN_TUNNEL_UUID` | both | Admin tunnel ID |
| `HY2_HOST` | both | Hysteria2 hostname |
| `HY2_PORT` | both | Hysteria2 UDP port |
| `HY2_OBFS_PW` | both | Hysteria2 obfuscation password |

## Control panel

```bash
npm --prefix panel/web install
npm --prefix panel/web dev
npm --prefix panel/web test -- --run
```

The Users tab exposes a public subscription URL per user at `https://<panel-host>/sub/<32-hex-token>`. Cloudflare Access must bypass `/sub/*`; `/api/*` should remain protected.

## Development

```bash
go test ./internal/...
go build ./cmd/cfvpnctl ./cmd/cfvpn-agent
npm --prefix panel/worker run check
npm --prefix panel/worker test
npm --prefix panel/web run test:run
npm --prefix panel/web run build
```

## D1 schema migrations

```bash
# Local
wrangler d1 execute cfvpn --local --file panel/worker/migrations/0010_nodes_reality_xhttp.sql

# Production
wrangler d1 execute cfvpn --remote --file panel/worker/migrations/0010_nodes_reality_xhttp.sql
```
