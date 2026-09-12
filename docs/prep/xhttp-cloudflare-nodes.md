# XHTTP on the cloudflare-mode nodes (OR-001, JPY-01, VNM-01) — DEPLOYED 2026-09-12

Status: **live** on all three nodes as a second inbound next to HTTPUpgrade,
shipped as a first-class feature (not a hand edit):

- env key `XHTTP_ENABLED=1` in `/etc/cfvpn/cfvpn.env`; `cfvpnctl xhttp enable|disable`
  flips it and re-renders xray + cloudflared in place.
- xray inbound `vless-xhttp` on `127.0.0.1:10002`, `network xhttp`,
  path `/api/v2/stream`, mode **`packet-up`**, same clients as HTTPUpgrade
  (`internal/templates/render.go`, `RenderXrayCloudflare`).
- cloudflared ingress rule `path ^/api/v2/stream → http://127.0.0.1:10002`
  emitted before the HTTPUpgrade rule (`RenderCloudflaredWithAdminOpts`).
- subscription line `<user>@<NODE>-XHTTP` from both builders (Go + Worker,
  golden-tested); D1 column `nodes.xhttp_enabled` (migration 0020) set with
  `scripts/d1-set-node.sh <NODE> xhttp-on`. Listed in the Shadowrocket PROXY
  group, **not** in AUTO; omitted from the Clash output (mihomo has no xhttp).

## Feasibility result (2026-09-12, OR-001, probe from VNM-01)

| mode | result |
|---|---|
| packet-up | **204, 611 ms** |
| auto | 204, 305 ms (falls back to packet-up) |
| stream-up | fail (same as the May 2026 finding) |
| stream-one | fail |

Client URI:

```
vless://<uuid>@<DOMAIN>:443?encryption=none&security=tls&type=xhttp&host=<DOMAIN>&path=%2Fapi%2Fv2%2Fstream&mode=packet-up&sni=<DOMAIN>#kulinh%40<NODE>-XHTTP
```

| Node | DOMAIN | probe after enable |
|---|---|---|
| OR-001 | static-df60bd79.duylinh.org | XHTTP 645 ms, HTTPUpgrade 855 ms |
| JPY-01 | edge-fd34b370.rwl247.dev | XHTTP 179 ms, HTTPUpgrade 405 ms |
| VNM-01 | edge-f7c5683a.888vn.net | XHTTP 181 ms, HTTPUpgrade 217 ms |

## Rollback (per node)

```bash
cfvpnctl xhttp disable          # removes inbound + ingress, restarts what changed
bash scripts/d1-set-node.sh <NODE> xhttp-off   # from VNM-01: drops the -XHTTP line
```

HTTPUpgrade stays up throughout either direction. The China test is still the
one that matters: if XHTTP proves more stable there than HTTPUpgrade, swap the
AUTO member for OR-001 from `-HTTPUpgrade` to `-XHTTP` in
`panel/worker/src/lib/shadowrocket.ts`.

## Direct XHTTP route on JPY-01 (no Cloudflare) — DEPLOYED 2026-09-12

A backup route for iOS/Android that does not depend on the Cloudflare tunnel:

- Hostname `cdn-82169439.duylinh.net` → 45.143.131.36 (A, not proxied), Let's
  Encrypt via Caddy DNS-01. Caddy (`/usr/local/bin/caddy-naive`, unit
  `naive-caddy`, Caddyfile `/etc/naive/Caddyfile` — the names predate the
  NaiveProxy removal) terminates TLS on :443, serves a real site from
  `/var/www/site` for every path (`try_files {path} /index.html`) and
  reverse-proxies exactly one long random path (`XHTTP_DIRECT_PATH` in
  `cfvpn.env`) to xray with `transport http { versions h2c 1.1 }` and
  `flush_interval -1`.
- xray inbound `vless-xhttp-direct` on `127.0.0.1:10003`, network xhttp, mode
  **stream-one** (server pinned; packet-up/auto clients are rejected, verified).
  Rendered from `XHTTP_DIRECT_HOST/PATH` by every renderer, so panel syncs keep it.
- `cfvpnctl xhttp-direct enable --host <h> --path </long-random> | disable`,
  then `scripts/d1-set-node.sh JPY-01 xhttp-direct` from VNM-01.
- Subscription line `kulinh@JPY-01-XHTTP-Direct` (address = hostname, real
  TLS, `sni` = hostname), listed in the Shadowrocket PROXY group as a
  standalone backup, never in AUTO.

Verified from VNM-01: probe through the route 350–383 ms (204, three runs);
`https://cdn-82169439.duylinh.net/` and any wrong path → the real site (200);
a plain GET on the exact path → xray's empty 404 (only someone who already
knows the secret path can see that).

Rollback: `cfvpnctl xhttp-direct disable` on JPY-01, `d1-set-node.sh JPY-01 xhttp-direct`,
and remove the `handle <path>*` block from the Caddyfile (`systemctl reload naive-caddy`).
