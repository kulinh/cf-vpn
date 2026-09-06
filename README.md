# cfvpn

Multi-VPS VPN control plane. Cloudflare Worker (Hono + D1) + React panel + per-node `cfvpn-agent` driving Xray (VLESS) and Hysteria2 over systemd. Operator CLI is `cfvpnctl`.

## Architecture

```
Panel (React, Cloudflare Pages)
  └─ Worker API (Hono + D1) ──┐  Bearer (AGENT_SHARED_SECRET)
                               │
  cfvpn-agent (:6788) ◄────────┘  via cloudflared admin tunnel
    ├─ xray
    │    ├─ direct mode:    VLESS + XTLS-Reality on :443 (xtls-rprx-vision)
    │    └─ cloudflare mode: VLESS + HTTPUpgrade on 127.0.0.1:10001, path /api/v1/sync
    ├─ hysteria2 (UDP, salamander obfs)
    └─ cloudflared
         ├─ admin ingress: <NODE_ID>.rwl247.dev → 127.0.0.1:6788
         └─ vpn ingress (cloudflare mode only): <DOMAIN>/api/v1/sync → 127.0.0.1:10001
```

Mode is chosen at install time:

- **direct** — port 443 reachable. VLESS+Reality. No public TLS cert; Reality piggybacks `dest=www.microsoft.com:443` for SNI camouflage. x25519 keypair + 8-byte shortId per node.
- **cloudflare** — port 443 blocked by ISP. VLESS+HTTPUpgrade on a loopback port; cloudflared fronts it. No `:443` listener.

Hysteria2 runs in both modes on a random UDP port (20000–60000) with a salamander obfuscation password. Hysteria2 always uses a real Let's Encrypt cert (DNS-01 via Cloudflare).

## Security model

- Admin tunnel `<NODE_ID>.rwl247.dev` is publicly resolvable. The agent **refuses to start** if `AGENT_SHARED_SECRET` is empty; every `/admin/v1/*` request requires `Authorization: Bearer <secret>` (constant-time compare).
- `install-node.sh` auto-generates `AGENT_SHARED_SECRET` if not provided and persists it in `/etc/cfvpn/cfvpn.env` (mode 600). It then writes the same value into the Worker's D1 `nodes.agent_secret` column itself (`INSERT OR REPLACE INTO nodes …`) — there is no SQL for you to copy and paste.
- `CF_API_TOKEN` is never passed on `argv`. The installer routes it via curl `--config -` heredoc to keep it out of `/proc/<pid>/cmdline`.
- If install fails partway through, the EXIT/INT/TERM trap diffs the Cloudflare tunnel list and prints `cfvpnctl rotate-domain --cleanup <uuid>` for each orphan tunnel created during the run.
- Both installers refuse to run on a node that has already been provisioned (`/etc/cfvpn/.installed`, written only after `cfvpnctl install` succeeds; or an env file holding installer-generated keys) — a re-run regenerates every credential and breaks every configured client. A run that fails *before* that point stays retryable. Use `cfvpnctl upgrade`, or `FORCE_REINSTALL=1` to re-provision deliberately (the old env file is backed up to `/etc/cfvpn/cfvpn.env.bak-<unixtime>`, mode 0600). See `docs/INSTALL_MINIMAL.md` §7.
- `scripts/install-node-CN.sh` verifies the sha256 of every binary it stages for the China target against the upstream checksum, and never interpolates a value into the remote root shell: the env file is built locally and streamed over ssh's stdin.

## Build

```bash
go build -o bin/cfvpnctl    ./cmd/cfvpnctl
go build -o bin/cfvpn-agent ./cmd/cfvpn-agent
```

Go 1.22+. Binaries are static; copy to `/usr/local/bin/` on the target VPS.

## Fresh install on a new VPS

See `docs/INSTALL_MINIMAL.md` for the full prerequisite list (Debian packages, Cloudflare token scopes, zone pool). Quick form:

```bash
sudo -E \
  CF_API_TOKEN='cf-token-with-zone-rw-and-tunnels-rw' \
  CF_ACCOUNT_ID='your_account_id' \
  NODE_ID='hk-01' \
  bash scripts/install-node.sh
```

`install-node.sh` auto-detects mode by probing `:443`. To force a mode, pass `--mode=direct` or `--mode=cloudflare` to the underlying `cfvpnctl install` (edit the script, or run `cfvpnctl install` manually after you've populated `cfvpn.env`).

`AGENT_SHARED_SECRET` is generated automatically when not exported, written to `/etc/cfvpn/cfvpn.env` and mirrored into D1 (`nodes.agent_secret`) by the installer itself — nothing to copy by hand.

## Upgrading an existing VPS

```bash
sudo cfvpnctl upgrade --mode auto       # auto-detect from :443
sudo cfvpnctl upgrade --mode direct     # force Reality
sudo cfvpnctl upgrade --mode cloudflare # force HTTPUpgrade behind cloudflared
```

Same-mode upgrades re-render xray + cloudflared configs against the EXISTING host (idempotent — only writes + restarts when the rendered bytes actually changed). No DNS work, no cert reissue, no host rotation.

## Daily ops

| Task | Command |
|---|---|
| Add user | `sudo cfvpnctl add-user <name>` |
| Remove user | `sudo cfvpnctl remove-user <name> --yes` |
| Print user subscription | `sudo cfvpnctl gen-sub <name>` |
| Local node status | `sudo cfvpnctl status` |
| Run healthcheck | `sudo cfvpnctl healthcheck run` |
| Install healthcheck timer | `sudo cfvpnctl healthcheck install` |
| Cleanup orphan rotation tunnel | `sudo cfvpnctl rotate-domain --cleanup <uuid> --yes` |
| Apply network tuning (BBR/fq) | `sudo cfvpnctl tune-net` |
| Xray logs | `journalctl -u cfvpn-xray -f` |
| Hysteria2 logs | `journalctl -u cfvpn-hysteria -f` |
| Admin tunnel logs | `journalctl -u cfvpn-cloudflared -f` |

User mutations (add/remove/sync) re-render the xray config in-place using the active mode + Reality params; they do not trigger DNS work or cert reissue. Per-user subscription files are regenerated automatically after every mutation.

`cfvpnctl rotate-domain <new-domain>` (without `--cleanup`) is **deprecated**; domain rotation is now driven from the panel (`POST /api/nodes/:id/rotate`) which calls the agent's `/admin/v1/rotate-domain`.

## Network tuning (BBR)

`cfvpnctl install` and `cfvpnctl upgrade` apply the node network tuning
automatically; `cfvpnctl tune-net` runs just that step, which is how existing
nodes get it without a full re-deploy:

```bash
sudo cfvpnctl tune-net          # idempotent, safe to re-run
```

It writes `/etc/sysctl.d/90-cfvpn.conf`:

```text
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.ipv4.tcp_mtu_probing = 1
```

then `modprobe tcp_bbr` (a failure is fine — BBR is built into most kernels)
and `sysctl --system`, and finally re-reads
`net.ipv4.tcp_congestion_control` to confirm `bbr` is actually active. If it is
not, the command warns and still exits 0: the node works, it is just slower.

**Why:** on the VN ↔ APAC path this fleet serves, 25–30% packet loss at peak is
normal. Cubic (the Linux default) reads that as congestion and collapses the
window to under 1 KB/s — the "node is up but the VPN is dead" symptom. BBR
models bandwidth and RTT instead of counting losses and keeps the pipe full
through the same loss; `fq` is the qdisc BBR's pacing needs, and
`tcp_mtu_probing` recovers from the black-holed ICMP "fragmentation needed"
that tunnels on this path routinely hit.

Note the drop-in sorts *before* any `99-*.conf` an operator has added, so an
existing `99-network-perf.conf` still wins for keys it also sets.

## Healthcheck

`cfvpnctl healthcheck run` is mode-aware:

- **Reality direct nodes** (`MODE=direct` + `REALITY_PRIVATE_KEY` set): TCP connect to `<DOMAIN>:443`. Reality camouflages the TLS handshake, so an HTTPS probe would always fail — a successful TCP open is the success signal.
- **Cloudflare / legacy direct nodes**: HTTPS GET `https://<DOMAIN>/api/v1/sync`, accept HTTP 400 / 426 (xray's response to a non-WS / non-HTTPUpgrade request).

The systemd timer (`cfvpn-healthcheck.timer`) is safe to enable on every node.

## Verify

```bash
systemctl is-active cfvpn-xray cfvpn-hysteria cfvpn-cloudflared cfvpn-agent
sudo cfvpnctl status
sudo cfvpnctl healthcheck run
```

## Files

```text
/etc/cfvpn/cfvpn.env                       # secrets, runtime settings (chmod 600)
/etc/cfvpn/xray/config.json                # generated VLESS config
/etc/cfvpn/hysteria/config.yaml            # generated Hysteria2 config
/etc/cfvpn/cloudflared/config.yml          # admin/vpn ingress
/etc/cfvpn/cloudflared/<tunnel-uuid>.json  # tunnel credentials (chmod 600)
/var/lib/cfvpn/subscriptions/<user>.txt    # per-user subscription (chmod 600)
/var/lib/cfvpn/state/                      # runtime state
/etc/systemd/system/cfvpn-xray.service
/etc/systemd/system/cfvpn-hysteria.service
/etc/systemd/system/cfvpn-cloudflared.service
/etc/systemd/system/cfvpn-agent.service
/etc/systemd/system/cfvpn-healthcheck.{service,timer}
```

### `cfvpn.env` keys

| Key | Mode | Description |
|---|---|---|
| `AGENT_SHARED_SECRET` | both | Bearer for `/admin/v1/*`. **Required** — agent refuses to start if empty. |
| `CF_API_TOKEN` | both | Cloudflare API token (Zone:Edit, Tunnel:Edit, Account:Read) |
| `CF_ACCOUNT_ID` | both | Cloudflare account ID |
| `MODE` | both | `direct` or `cloudflare` |
| `DOMAIN` | both | Public VPN hostname |
| `PUBLIC_IP` | direct | Detected IPv4 (A record target) |
| `REALITY_PRIVATE_KEY` | direct | x25519 private key (node-local, never synced to D1) |
| `REALITY_PUBLIC_KEY` | direct | x25519 public key (synced to D1 for clients) |
| `REALITY_SHORT_ID` | direct | 8-byte hex shortId |
| `REALITY_DEST` | direct | Reality `dest` (e.g. `www.microsoft.com:443`) |
| `REALITY_SNI` | direct | Reality `serverNames[0]` (e.g. `www.microsoft.com`) |
| `XHTTP_PATH` | cloudflare | VLESS HTTPUpgrade path (default `/api/v1/sync`) |
| `ADMIN_HOST` | both | cloudflared admin hostname (`<NODE_ID>.rwl247.dev`) |
| `ADMIN_TUNNEL_UUID` | both | Admin tunnel UUID |
| `HY2_HOST` | both | Hysteria2 hostname |
| `HY2_PORT` | both | Hysteria2 UDP port |
| `HY2_OBFS_PW` | both | Hysteria2 salamander obfs password |

## Panel (React) and Worker

```bash
# Web panel
npm --prefix panel/web install
npm --prefix panel/web run dev
npm --prefix panel/web run test:run
npm --prefix panel/web run build

# Worker (plain fetch handler + D1, no framework)
npm --prefix panel/worker install
npm --prefix panel/worker run check
npm --prefix panel/worker test
```

Public subscription URL per user is `https://<panel-host>/sub/<32-hex-token>`. Cloudflare Access **must** allow `/sub/*` and `/telegram/webhook` unauthenticated (Telegram cannot send Access headers; the webhook is protected by its own secret-token + group-id check); `/api/*` should remain protected.

### Subscription formats

The same token serves two formats; both list exactly the same nodes with the same names.

| URL | Format | Clients |
|---|---|---|
| `https://<panel-host>/sub/<token>` | base64 URI list (default) | Shadowrocket, v2rayN, v2rayNG, Nekobox |
| `https://<panel-host>/sub/<token>?format=clash` | mihomo/Clash YAML | Clash Verge (Rev), mihomo, Stash, Shadowrocket's Clash import |

Any other `?format=` value returns `400 invalid_format` rather than silently serving base64.

The Clash config ships two proxy groups:

- **`Auto`** — `url-test` against `http://www.gstatic.com/generate_204` every 300s with a 100ms tolerance, so it **picks the lowest-latency node automatically** and re-picks as latency changes.
- **`Proxy`** — a `select` group listing `Auto` first, then every node, for pinning one node by hand.

The single rule is `MATCH,Proxy`, i.e. all traffic goes through the `Proxy` group (which defaults to `Auto`). Proxy names match the fragment of the corresponding base64 URI: `<user>@<NODE>-Reality`, `<user>@<NODE>-HTTPUpgrade`, `<user>@<NODE>-HY2`.

## Telegram bot

A Telegram bot provides a second control surface (manage users/nodes, view
status) from one private group. It runs inside the `cfvpn-panel-api` Worker as
`POST /telegram/webhook`.

Setup:

```bash
# 1. Secrets (never commit these)
wrangler --config panel/worker/wrangler.toml secret put TELEGRAM_BOT_TOKEN
wrangler --config panel/worker/wrangler.toml secret put TELEGRAM_WEBHOOK_SECRET   # any random string

# 2. TELEGRAM_GROUP_ID is already set in wrangler.toml [vars] (-1003806233980)

# 3. Deploy, then register the webhook + command menu
npm --prefix panel/worker run deploy
TELEGRAM_BOT_TOKEN=...  TELEGRAM_WEBHOOK_SECRET=...  PANEL_HOST=panel.rwl247.dev \
  bash scripts/telegram-setup.sh
```

Security: the Worker rejects any webhook whose `X-Telegram-Bot-Api-Secret-Token`
header does not equal `TELEGRAM_WEBHOOK_SECRET`, and ignores any update whose
`chat.id` is not `TELEGRAM_GROUP_ID`. Mutations are logged to the `events` table
with actor `tg:<telegram_user_id>`.

Commands: `/help`, `/nodes`, `/status <node>`, `/health <node>`, `/sync <node>`,
`/rotate <node>`, `/users`, `/adduser <name>`, `/deluser <name>`, `/sub <name>`,
`/upgrade <name>`. Destructive actions (`/deluser`, `/rotate`) ask for
confirmation via inline buttons.

## D1 schema migrations

```bash
# Local dev
wrangler d1 execute cfvpn --local  --file panel/worker/migrations/0011_nodes_agent_secret.sql

# Production
wrangler d1 execute cfvpn --remote --file panel/worker/migrations/0011_nodes_agent_secret.sql
```

Migrations 0010 and 0011 are purely additive (nullable columns) — backwards compatible with older rows.

- `0010_nodes_reality_xhttp.sql` adds `reality_pubkey`, `reality_sid`, `reality_sni`, `reality_dest`, `xhttp_path`.
- `0011_nodes_agent_secret.sql` adds `agent_secret` — the per-node bearer the Worker sends on `/admin/v1/*`. Mirror `/etc/cfvpn/cfvpn.env`'s `AGENT_SHARED_SECRET` into this column with the SQL printed by `install-node.sh`. The Worker falls back to the global `AGENT_SHARED_SECRET` env var when the column is null, so existing nodes keep working until you migrate them.

### Deploy order (migrations 0016–0019)

**Migrations first, Worker second.** The cron sweep reads `nodes.consecutive_failures` (0017) and `nodes.last_sweep_outcome` (0019); a Worker deployed ahead of them makes every sweep throw, and `last_seen_at` silently stops advancing fleet-wide.

`d1_migrations` on prod is stuck at `0013` even though `0014` and `0015` were applied out of band, so `wrangler d1 migrations apply` would replay `0014` and abort on `duplicate column name`, taking everything after it down with it. Backfill the bookkeeping first:

```bash
# 1. Backfill the two out-of-band migrations so the runner skips them
wrangler --config panel/worker/wrangler.toml d1 execute cfvpn_panel_prod --remote \
  --command "INSERT INTO d1_migrations (name) VALUES ('0014_nodes_tunnel_uuid.sql'), ('0015_normalize_zone_created_at_ms.sql');"

# 2. Verify: max(id) should now cover 0015 and nothing should be pending but 0016+
wrangler --config panel/worker/wrangler.toml d1 migrations list cfvpn_panel_prod --remote

# 3. Apply 0016 (created_at normalisation), 0017 + 0019 (sweep columns), 0018 (drop dead indexes)
wrangler --config panel/worker/wrangler.toml d1 migrations apply cfvpn_panel_prod --remote

# 4. Only now deploy the Worker
npm --prefix panel/worker run deploy
```

Each file holds a single `ALTER` on purpose — D1 has no `ADD COLUMN IF NOT EXISTS`, so a partial failure stays recoverable.

## Tests

```bash
go test ./internal/...
go build ./cmd/cfvpnctl ./cmd/cfvpn-agent
npm --prefix panel/worker test
npm --prefix panel/web run test:run
```
