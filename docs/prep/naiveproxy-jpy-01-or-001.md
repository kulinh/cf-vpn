# NaiveProxy on JPY-01 and OR-001 — prepared, not deployed

Status: **prepared 2026-09-12, nothing installed.** Operator decision: JPY-01
and OR-001 (not SIN-01). Both are cloudflare-mode nodes, so TCP 443 is free
(xray listens on 127.0.0.1:10001; cloudflared dials out) and Caddy can take
the standard `:443`. Kept **out of AUTO** and out of the subscription; the
client lines go in a separate `naive.txt`.

| Node | Public IP | Naive hostname (pick at enable time) |
|---|---|---|
| JPY-01 | 45.143.131.36 | `naive-<8 hex>.duylinh.net` |
| OR-001 | 51.81.245.144 | `naive-<8 hex>.duylinh.net` |

`duylinh.net` zone id: look it up with the CF API (`GET /zones?name=duylinh.net`)
using `CF_API_TOKEN` from `/etc/cfvpn/cfvpn.env`; HAN-01's `vpn_host` already
lives in this zone so the token has DNS edit rights on it.

## 1. Build Caddy with forwardproxy (once, on VNM-01, copy the binary)

```bash
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
~/go/bin/xcaddy build \
  --with github.com/caddyserver/forwardproxy@caddy2=github.com/klzgrad/forwardproxy@naive \
  --with github.com/caddy-dns/cloudflare
./caddy version
scp -i /root/rwl01.key ./caddy root@<tailscale-ip>:/tmp/caddy   # then on node: install -m 0755 /tmp/caddy /usr/local/bin/caddy-naive
```

## 2. Per-node files

`/etc/naive/Caddyfile` (replace `<host>` and `<password>`; password = `openssl rand -base64 24`):

```
{
  order forward_proxy before file_server
  admin off
}
<host>:443 {
  tls {
    dns cloudflare {env.CF_API_TOKEN}
  }
  forward_proxy {
    basic_auth kulinh <password>
    hide_ip
    hide_via
    probe_resistance
  }
  file_server {
    root /var/www/naive-decoy
  }
}
```

`/etc/systemd/system/naive-caddy.service`:

```
[Unit]
Description=NaiveProxy (Caddy + forwardproxy)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/cfvpn/cfvpn.env
ExecStart=/usr/local/bin/caddy-naive run --config /etc/naive/Caddyfile --adapter caddyfile
Restart=on-failure
RestartSec=3
AmbientCapabilities=CAP_NET_BIND_SERVICE
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
```

Decoy content: `mkdir -p /var/www/naive-decoy && echo '<h1>It works</h1>' > /var/www/naive-decoy/index.html`.

## 3. Enable (one node at a time)

```bash
# 0. nothing else on :443 (JPY-01 also runs the operator's personal cloudflared)
ss -lntp | grep ':443 ' && echo "PORT 443 BUSY — stop" || echo "443 free"
# 1. DNS A record <host> -> node IP (proxied=false) via CF API
curl -s -X POST "https://api.cloudflare.com/client/v4/zones/<zone-id>/dns_records" \
  -H "Authorization: Bearer $CF_API_TOKEN" -H "Content-Type: application/json" \
  --data '{"type":"A","name":"<host>","content":"<node-ip>","ttl":120,"proxied":false}'
# 2. files from section 2, then
mkdir -p /etc/naive && chmod 700 /etc/naive
systemctl daemon-reload && ufw allow 443/tcp && systemctl enable --now naive-caddy
journalctl -u naive-caddy -n 20 --no-pager        # expect "certificate obtained successfully"
```

## 4. Verify

```bash
# on the node: cert + forward proxy
curl -sv --proxy https://kulinh:<password>@<host>:443 http://cp.cloudflare.com/generate_204 -o /dev/null 2>&1 | grep -E "< HTTP|SSL connection"
# from VNM-01: naive path AND the existing HTTPUpgrade route still 204
curl -s -o /dev/null -w '%{http_code}\n' --proxy https://kulinh:<password>@<host>:443 http://cp.cloudflare.com/generate_204
python3 scripts/fleet-probe.py --once --sub-file <(curl -s "$SUB_URL")
```

## 5. Client line (separate file, never in the subscription or AUTO)

`naive.txt`:

```
https://kulinh:<password>@<host>:443#kulinh%40JPY-01-Naive
https://kulinh:<password>@<host>:443#kulinh%40OR-001-Naive
```

Shadowrocket imports these as type HTTPS (TLS CONNECT proxy). That is
standard HTTPS-proxy framing without NaiveProxy's padding; for the padded
protocol use the NaiveProxy client (`naive --listen socks://127.0.0.1:1080
--proxy https://kulinh:<password>@<host>`).

## 6. Rollback

```bash
systemctl disable --now naive-caddy
ufw delete allow 443/tcp
# optional: delete the DNS record and /etc/naive
```

The HTTPUpgrade route is untouched throughout: xray and cloudflared are never
restarted by these steps.
