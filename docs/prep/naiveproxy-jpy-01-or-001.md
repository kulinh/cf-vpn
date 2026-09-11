# NaiveProxy on JPY-01 and OR-001 — DEPLOYED 2026-09-12

Status: **live** on both nodes since 2026-09-12 (~20:35Z). Caddy 2 built with
`xcaddy --with github.com/caddyserver/forwardproxy@caddy2=github.com/klzgrad/forwardproxy@naive --with github.com/caddy-dns/cloudflare`,
installed as `/usr/local/bin/caddy-naive`, unit `naive-caddy.service`.

| Node | Hostname | Client port | Notes |
|---|---|---|---|
| JPY-01 | naive-62abc468.duylinh.net | **443** | real public IP 45.143.131.36, port 443 was free (cloudflare-mode node) |
| OR-001 | naive-74df6c7b.duylinh.net | **5373** | TierHive NAT: the shared IP 51.81.245.144 has 443 owned by the provider's HAProxy; operator forwarded external **5373/tcp+udp → 10.0.197.10:443**. Caddy still listens on 443 inside the VM |

Client lines (password inside) are in `/root/cfvpn-backups/20260911T195306Z/after3/naive.txt`
on VNM-01 (mode 600). They are deliberately **not** in the subscription or in
AUTO. Shadowrocket imports them as type HTTPS; verified from VNM-01 with
`curl --proxy https://kulinh:<pw>@<host>:<port> https://cp.cloudflare.com/generate_204`
→ 204 on both, wrong password → connection refused, plain GET → decoy page 200.

## Files on each node

`/etc/naive/Caddyfile` (mode 600):

```
{
  order forward_proxy before file_server
  admin off
}
:443, <host>:443 {
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

**The site key must be `:443, <host>:443`, not `<host>:443` alone.** A CONNECT
request carries the *target* host, so a hostname-only site key never matches
it and Caddy answers an empty 200 while the client believes a tunnel is open.
This cost an hour on 2026-09-12; keep the bare `:443` key.

`/etc/systemd/system/naive-caddy.service`: `EnvironmentFile=/etc/cfvpn/cfvpn.env`
(for `CF_API_TOKEN`, DNS-01), `Environment=XDG_DATA_HOME=/var/lib/naive`,
`AmbientCapabilities=CAP_NET_BIND_SERVICE`, `Restart=on-failure`. Certificates
auto-renew inside Caddy (storage `/var/lib/naive/caddy`).

Decoy: `/var/www/naive-decoy/index.html`. ufw: `443/tcp` allowed on both.

## Operate

```bash
systemctl status naive-caddy; journalctl -u naive-caddy -n 20 --no-pager
# rotate the password: edit basic_auth in the Caddyfile, systemctl restart naive-caddy, update naive.txt
# rollback
systemctl disable --now naive-caddy && ufw delete allow 443/tcp
```

Neither xray nor cloudflared is touched by any of this; the HTTPUpgrade and
XHTTP routes were re-probed after deployment (204).
