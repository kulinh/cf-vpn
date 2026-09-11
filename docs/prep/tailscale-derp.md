# Custom Tailscale DERP relay on HKG-01 — prepared, not applied

Status: **prepared 2026-09-12, nothing installed, ACL not changed.**

Why HKG-01: it is a direct (Reality) node, so TCP 443 is taken by xray.
derper therefore listens on **8443/tcp** for DERP and **3478/udp** for STUN.
(SIN-01 would be identical; it was not chosen so the primary Reality node
stays single-purpose.) If you want DERP on 443 instead, put derper on JPY-01
or OR-001 where 443 is free (see the NaiveProxy prep for the port check) —
but only one of NaiveProxy/DERP can own 443 there.

## 1. Hostname and certificate

- DNS: `derp-<8 hex>.duylinh.net` A → 96.9.228.81 (proxied **false**; DERP
  must be reached directly).
- Certificate via lego DNS-01 (already installed on every node for HY2):

```bash
export CF_DNS_API_TOKEN="$(awk -F= '/^CF_API_TOKEN=/{print $2}' /etc/cfvpn/cfvpn.env)"
mkdir -p /etc/derper && chmod 700 /etc/derper
lego --email admin@duylinh.net --dns cloudflare --domains <host> --path /etc/derper/lego --accept-tos run
# derper --certmode manual expects <certdir>/<host>.crt and <host>.key
ln -sf /etc/derper/lego/certificates/<host>.crt /etc/derper/<host>.crt
ln -sf /etc/derper/lego/certificates/<host>.key /etc/derper/<host>.key
```

Renewal: add `lego ... renew --days 30` plus `systemctl restart derper` to a
monthly cron (the cfvpn cert-renew timer does not know about this cert).

## 2. derper

```bash
GOFLAGS= go install tailscale.com/cmd/derper@latest      # on VNM-01, then scp ~/go/bin/derper to HKG-01:/usr/local/bin/derper
```

`/etc/systemd/system/derper.service`:

```
[Unit]
Description=Tailscale DERP relay
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/derper --hostname <host> -a :8443 --http-port -1 --stun --stun-port 3478 --certmode manual --certdir /etc/derper --verify-clients
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
```

`--verify-clients` makes derper accept only nodes of the tailnet the local
`tailscaled` belongs to (HKG-01 is already on the tailnet). `--http-port -1`
disables the plain-HTTP listener.

```bash
ufw allow 8443/tcp && ufw allow 3478/udp
systemctl daemon-reload && systemctl enable --now derper
curl -sk https://<host>:8443/derp/probe -o /dev/null -w '%{http_code}\n'   # 200
```

## 3. ACL snippet (tailnet policy file, Access Controls in the admin console)

```json
"derpMap": {
  "OmitDefaultRegions": true,
  "Regions": {
    "900": {
      "RegionID": 900,
      "RegionCode": "hkg",
      "RegionName": "HKG-01",
      "Nodes": [
        {
          "Name": "900a",
          "RegionID": 900,
          "HostName": "<host>",
          "DERPPort": 8443,
          "STUNPort": 3478
        }
      ]
    }
  }
}
```

## 4. Apply order (when you decide to)

1. Add the region **without** `OmitDefaultRegions` first. Run
   `tailscale netcheck` on two devices: region 900 must appear with a
   latency. Run `tailscale ping <peer>` and confirm `via DERP(hkg)` appears
   for a peer that cannot connect directly.
2. Only then set `"OmitDefaultRegions": true`. From that moment HKG-01 is the
   **only** relay for the whole tailnet: if it is down, every device pair
   without a direct path loses connectivity, including the SSH-over-Tailscale
   fleet access from VNM-01. Keep the public-IP :17722 SSH path as the
   fallback (it exists on every node).
3. Rollback = remove `derpMap` from the policy file; devices return to the
   default regions within a minute.
