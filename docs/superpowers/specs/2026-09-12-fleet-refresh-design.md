# Fleet refresh, September 2026 — design

Operator returned from China (China Mobile Beijing + itdog.cn): no node IP is
banned on TCP 443, but two routes timed out and the fleet needs a cleanup.
This spec covers the six work items the operator listed, in the order they
must be executed.

## Ground rules (non-negotiable)

- Back up every node's `/etc/cfvpn`, the D1 tables and all subscription
  formats into `/root/cfvpn-backups/<UTC timestamp>/` on VNM-01 before any
  node is touched.
- One node at a time. After each node: xray/hysteria/cloudflared active,
  ports listening, and an end-to-end probe through that node to
  `http://cp.cloudflare.com/generate_204` returns 204. Only then move on.
- Never remove or stop a working route until its replacement is verified.
- VNM-01 (the dev box, also a node) is not modified except for repo files,
  the health-check cron and its env file.
- Nodes are reached over SSH via Tailscale using `/root/rwl01.key` and
  `/etc/cfvpn/fleet-hosts`.

## Findings that shape the design (measured 2026-09-12)

- All 17 current routes (9 VLESS + 8 HY2) work from VNM-01. Drift check:
  9/9 nodes match D1. All six direct nodes already run `security=reality`,
  flow `xtls-rprx-vision`, port 443. No node runs a TLS-with-own-cert inbound.
- Xray-core 26.3.27 is on every node and is the latest **stable** upstream
  release (26.9.9 exists only as a pre-release). Decision: keep 26.3.27.
- HKG-01: server-side config is consistent with D1 and the subscription.
  xray logs show China Mobile clients (223.104.x.x) connecting successfully on
  9–11 Sep. The China timeout is attributed to DNS interference on the
  `*.dongnat247.com` hostname the client resolves, not to the Reality
  handshake. Fix = IP address in the URI (item 4) plus a fresh dest (item 3).
- JPY-01: xray + ingress match OR-001 exactly. cloudflared's QUIC transport
  drops every few minutes (`failed to dial to edge with quic: timeout: no
  recent network activity`). Fix = run the cfvpn tunnel on `protocol: http2`.
- Telegram bot token exists only as a Worker secret. Alerts from VNM-01 need
  a locally provided token; operator fills it in.

## Item 1 — repair JPY-01 and HKG-01

**JPY-01.** Add `protocol: http2` to the cloudflared config template
(`internal/templates`), gated by a new env key `CLOUDFLARED_PROTOCOL`
(default empty = cloudflared default). Set it to `http2` in JPY-01's
`cfvpn.env`, re-render, restart `cfvpn-cloudflared`, confirm four registered
connections stay up for 10 minutes and the probe returns 204.

**HKG-01.** No server change in this item. Re-verified after items 3 and 4.

## Item 2 — Hysteria2 only on JPY-01 and HKG-01

New env key `HY2_ENABLED` (`1` default, `0` = off). Honored by:

- install / upgrade: skip HY2 host, cert, config and port backfill when off.
- `reconcile-units`: `cfvpn-hysteria` unit is stopped, disabled and removed
  from the canonical set when off.
- node-side subscription builder: no HY2 line when off.

Per node (JPY-02, SIN-01, HAN-01, USA-01, VNM-02, OR-001): set
`HY2_ENABLED=0`, run `cfvpnctl reconcile-units`, delete the ufw UDP rule,
null the three `hy2_*` columns in D1 so the Worker drops the HY2 line, keep
the old config only in the backup. Verify the node's VLESS route still
returns 204 and the HY2 port no longer answers.

## Item 3 — refresh Reality on every direct node

New command `cfvpnctl rotate-reality --dest <host>:443 [--sni <host>]`:
generates a new x25519 keypair and shortId, writes `REALITY_*` to env,
re-renders xray, restarts it, and the agent's next sync pushes
`reality_pubkey/sid/sni/dest` to D1 (already persisted for direct nodes).
Also thread `REALITY_DEST`/`REALITY_SNI` from env into
`GenerateRealityOptions` at both install/upgrade call sites so a per-node
dest survives future upgrades.

Dest per node (all verified TLS 1.3 + h2 from VNM-01):

| Node | dest / SNI |
|---|---|
| JPY-02 | www.amazon.co.jp |
| SIN-01 | www.singaporeair.com |
| HKG-01 | www.cathaypacific.com |
| USA-01 | www.tesla.com |
| HAN-01 | vtv.vn (Vietnamese, VNPT-hosted, per operator) |
| VNM-02 | www.samsung.com |

Order: HAN-01 first (least critical, validates the tooling), then VNM-02,
USA-01, HKG-01, JPY-02, SIN-01. Each node: run, probe 204, drift check.

## Item 4 — client output

- Reality URI address becomes `PUBLIC_IP` in both builders
  (`internal/subscription` and `panel/worker/src/lib/subscription.ts`);
  golden tests updated on both sides so they stay byte-identical. `sni` stays
  the dest hostname. Cloudflare-mode routes keep their hostname.
- New `?format=shadowrocket` on `/sub/<token>`: a Shadowrocket `.conf`
  containing `[General]`, `[Proxy Group]` and `[Rule]`. Group `AUTO` is
  `url-test` over `kulinh@JPY-02-Reality, kulinh@SIN-01-Reality,
  kulinh@JPY-01-HY2, kulinh@HKG-01-HY2, kulinh@OR-001-HTTPUpgrade`, with
  `url = http://cp.cloudflare.com/generate_204, interval = 600,
  tolerance = 500, timeout = 8`. Membership is a constant in the Worker.
  The base64 subscription is unchanged so Shadowrocket still imports nodes.
- Deploy the Worker; write the final subscription outputs into
  `/root/cfvpn-backups/<ts>/after/`.

## Item 5 — health check from VNM-01

`scripts/fleet-probe.py` (from today's diagnostic probe): parses the
subscription, starts one xray client with a SOCKS inbound per VLESS route and
one hysteria client per HY2 route, curls `generate_204` through each, logs
`timestamp node verdict latency_ms` to `/var/log/cfvpn-fleet-probe.log`, keeps
per-node consecutive-failure counts in `/var/lib/cfvpn/fleet-probe.state`,
and posts to Telegram on the second consecutive failure (and once on
recovery). Cron: `*/10 * * * *` in `/etc/cron.d/cfvpn-fleet-probe`.
Secrets in `/etc/cfvpn/fleet-probe.env` (mode 600): `TELEGRAM_BOT_TOKEN`
(operator fills), `TELEGRAM_CHAT_ID=-1003806233980`, `SUB_URL`.

## Item 6 — prepared, not deployed

`docs/prep/` gets three documents with copy-paste configs, enable steps and
rollback steps:

- `naiveproxy-jpy-01-or-001.md`: Caddy + forwardproxy on JPY-01 and OR-001
  (operator decision 2026-09-12: not SIN-01). Both are cloudflare-mode nodes,
  so `:443` is free (xray listens on 127.0.0.1:10001 and cloudflared dials
  out); Caddy takes the standard `:443`. One hostname per node under
  `duylinh.net`, Let's Encrypt via Caddy DNS-01, ufw rule, Shadowrocket line.
  Kept out of AUTO; exported as its own file.
- `xhttp-cloudflare-nodes.md`: XHTTP inbound for OR-001, VNM-01, JPY-01 as a
  second inbound on 10002 with a distinct path, cloudflared ingress line,
  client URI; enable = add inbound + ingress, rollback = remove both. Notes
  the 2026-05-02 finding that XHTTP failed through cloudflared and what to
  re-test.
- `tailscale-derp.md`: derper unit on HKG-01 (`:443` conflicts with Reality,
  so derper on `:8443` with `--stun-port 3478`), DNS name, cert, ACL snippet
  with `OmitDefaultRegions: true`.

## Output

One commit per item on branch `feat/fleet-refresh-2026-09`; `REPORT.md` at
the end with per-node changes, where new dest/keys live, probe latencies
before/after, and what was left for the operator to decide.
