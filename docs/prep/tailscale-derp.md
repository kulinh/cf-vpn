# Custom Tailscale DERP relay on HKG-01 — DEPLOYED 2026-09-12

Status: `derper` runs on HKG-01 and the tailnet policy carries `derpMap`
with region 900 (HKG-01). `OmitDefaultRegions` was set to `true` for ~40
minutes on 2026-09-12 and then **reverted to `false`** (operator decision,
see the SIN-01 gap below): the public relays are back and region 900 is an
extra region. The flag is now managed by `cfvpnctl derp china-mode on|off`
(on = only the custom regions, for travel inside China).

| Item | Value |
|---|---|
| Hostname | `derp-f2a4f360.duylinh.net` → 96.9.228.81 (A, not proxied; no AAAA — tailscale logs a harmless v6 lookup error) |
| DERP | TCP **8443** (443 is xray Reality on this node) |
| STUN | UDP **3478** |
| Binary | `/usr/local/bin/derper` (tailscale.com/cmd/derper@latest, built on VNM-01) |
| Unit | `derper.service`: `derper --hostname derp-f2a4f360.duylinh.net -a :8443 --http-port -1 --stun --stun-port 3478 --certmode manual --certdir /etc/derper --verify-clients` |
| Cert | Let's Encrypt via lego DNS-01, `/etc/derper/lego/certificates/` symlinked to `/etc/derper/<host>.crt|.key`, expires 2026-12-10 |
| Renewal | `/etc/cron.d/derper-cert-renew`: monthly `lego … renew --days 30 && systemctl restart derper`, log `/var/log/derper-cert-renew.log` (dry run 2026-09-12: "renewal is not needed") |
| ufw | `8443/tcp`, `3478/udp` |

Verified after the ACL change: VNM-01 `tailscale netcheck` lists only
`hkg (HKG-01)`; USA-01 sees it at 180 ms; `tailscale debug derp 900` on VNM-01
reports a successful DERP connection and an IPv4 STUN response; 8 clients
connected to derper; SSH over Tailscale to USA-01, SIN-01, JPY-02 still works
(direct paths).

## Known gap: SIN-01 cannot reach the HKG-01 relay

From SIN-01 (GreenCloud SG, 96.9.231.74) the public IP of HKG-01 (GreenCloud
HK, 96.9.228.81) is unreachable on every port and to ping, in **both**
directions, while SIN-01 reaches other providers fine. This predates the DERP
work (it is the providers' routing between their own sites) but it means
SIN-01 has **no relay while `OmitDefaultRegions` is true**: it still connects directly to every peer that
has a public IP (verified), but a SIN-01 ↔ HKG-01 Tailscale path cannot
exist, and any SIN-01 peer that needs a relay cannot be reached. Options:

1. add a second region (e.g. `derper` on USA-01 or SIN-01 itself) to
   `derpMap.Regions` — recommended;
2. or drop `"OmitDefaultRegions": true` again (public relays return within a
   minute) and keep 900 as an extra region.

The SSH fallback via public IP `:17722` is unaffected.

## The policy snippet that is live

```json
"derpMap": {
  "OmitDefaultRegions": false,
  "Regions": {
    "900": {
      "RegionID": 900, "RegionCode": "hkg", "RegionName": "HKG-01",
      "Nodes": [{ "Name": "900a", "RegionID": 900, "HostName": "derp-f2a4f360.duylinh.net", "DERPPort": 8443, "STUNPort": 3478 }]
    }
  }
}
```

Rollback = remove `derpMap` from the policy; on the node
`systemctl disable --now derper; ufw delete allow 8443/tcp; ufw delete allow 3478/udp`.
