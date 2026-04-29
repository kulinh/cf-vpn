# cf-vpn

Multi-VPS VPN control system for VLESS + Hysteria2 nodes, with Cloudflare used for DNS, the admin tunnel, the Worker API, D1 storage, and the React management panel.

## Architecture

- Direct-mode clients connect to each VPS public IP through Cloudflare DNS-only hostnames.
- Cloudflare-mode VLESS clients connect through Cloudflare Tunnel when a VPS cannot expose TCP 443 directly.
- VLESS runs on Xray with WebSocket path `/vless`; direct mode uses TCP 443 with TLS, and cloudflare mode uses local `127.0.0.1:10001` behind cloudflared.
- Hysteria2 runs over UDP on a generated high port with salamander obfs.
- Cloudflare Tunnel always carries node admin/control traffic to the local `cfvpn-agent` on `127.0.0.1:6788`.
- The Worker stores nodes, users, per-user node credentials, public subscription tokens, zones, and events in D1.
- The panel calls authenticated `/api/*` routes; mobile clients fetch unauthenticated token subscriptions from `/sub/:token`.

## Build

```bash
make build
sudo install -m 0755 bin/cfvpnctl /usr/local/bin/cfvpnctl
sudo install -m 0755 bin/cfvpn-agent /usr/local/bin/cfvpn-agent
```

## Fresh VPS install

Create `/etc/cfvpn/cfvpn.env` with Cloudflare credentials and the target domain or zone inputs:

```bash
sudo mkdir -p /etc/cfvpn
sudo tee /etc/cfvpn/cfvpn.env >/dev/null <<'EOF'
CF_API_TOKEN=...
CF_ACCOUNT_ID=...
DOMAIN=vpn.example.com
USER1_NAME=user1
EOF
sudo chmod 600 /etc/cfvpn/cfvpn.env
```

Install a direct node:

```bash
sudo cfvpnctl install --mode direct
```

The installer writes Xray, Hysteria2, cloudflared admin tunnel, systemd units, TLS certs, DNS records, firewall rules, and the bootstrap user subscription.

## Upgrade an existing VPS

Use upgrade for nodes already deployed by older cf-vpn builds. Direct is the default and preferred mode:

```bash
sudo cfvpnctl upgrade --mode direct
# equivalent legacy entrypoint:
sudo cfvpnctl install --upgrade --mode direct
```

Use Cloudflare tunnel mode when the VPS cannot expose direct TCP 443:

```bash
sudo cfvpnctl upgrade --mode cloudflare
# equivalent legacy entrypoint:
sudo cfvpnctl install --upgrade --mode cloudflare
```

Upgrade preserves existing users where possible, keeps the admin tunnel, backfills Hysteria2 config and credentials, and renders VLESS either as direct TCP 443 or as a Cloudflare Tunnel ingress depending on `--mode`.

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

Full checklist: [docs/TESTING.md](docs/TESTING.md).

## Files of note

```text
/etc/cfvpn/cfvpn.env                       # Secrets and runtime settings, chmod 600
/etc/cfvpn/xray/config.json                # Generated VLESS config
/etc/cfvpn/hysteria/config.yaml            # Generated Hysteria2 config
/etc/cfvpn/cloudflared/config.yml          # Admin tunnel ingress to cfvpn-agent
/etc/cfvpn/cloudflared/<tunnel-uuid>.json  # Tunnel credentials, chmod 600
/var/lib/cfvpn/subscriptions/<user>.txt    # Per-user subscription, chmod 600
/var/lib/cfvpn/state/                      # Runtime state
/etc/systemd/system/cfvpn-xray.service
/etc/systemd/system/cfvpn-hysteria.service
/etc/systemd/system/cfvpn-cloudflared.service
/etc/systemd/system/cfvpn-agent.service
```

## Control panel

```bash
npm --prefix panel/web install
npm --prefix panel/web dev
npm --prefix panel/web test -- --run
```

The Users tab exposes a stable public subscription URL per user at `https://<panel-host>/sub/<32-hex-token>`. Cloudflare Access must bypass `/sub/*`; `/api/*` should remain protected.

## Development validation

```bash
go test ./...
go build ./cmd/cfvpnctl ./cmd/cfvpn-agent
npm --prefix panel/worker run check
npm --prefix panel/worker test
npm --prefix panel/web run test:run
npm --prefix panel/web run build
```
