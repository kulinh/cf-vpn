# Custom Tailscale DERP relay on HKG-01 — RELAY DEPLOYED 2026-09-12, ACL NOT APPLIED

Status: `derper` is **running on HKG-01**; the tailnet **ACL has not been
changed** (that is done in the admin console by the operator). Until the ACL
lists the region, no device uses it.

| Item | Value |
|---|---|
| Hostname | `derp-f2a4f360.duylinh.net` → 96.9.228.81 (A, not proxied) |
| DERP | TCP **8443** (443 is xray Reality on this node) |
| STUN | UDP **3478** |
| Binary | `/usr/local/bin/derper` (tailscale.com/cmd/derper@latest, built on VNM-01) |
| Unit | `derper.service`: `derper --hostname derp-f2a4f360.duylinh.net -a :8443 --http-port -1 --stun --stun-port 3478 --certmode manual --certdir /etc/derper --verify-clients` |
| Cert | Let's Encrypt via lego DNS-01, `/etc/derper/lego/certificates/` symlinked to `/etc/derper/<host>.crt|.key` |
| ufw | `8443/tcp`, `3478/udp` |

Verified from VNM-01: `https://derp-f2a4f360.duylinh.net:8443/derp/probe` → 200,
STUN binding request (tailscale format, with FINGERPRINT) → 44-byte response.
Note: a bare 20-byte STUN request without the FINGERPRINT attribute is
ignored by derper; that is not a fault.

`--verify-clients` accepts only nodes of the tailnet the local `tailscaled`
belongs to, so nobody else can relay through it.

## Cert renewal (not automated yet)

lego certs last 90 days (this one until ~2026-12-11). Add to root's crontab on HKG-01:

```
17 4 1 * * cd /etc/derper && CLOUDFLARE_DNS_API_TOKEN=$(awk -F= '/^CF_API_TOKEN=/{print $2}' /etc/cfvpn/cfvpn.env) lego --email admin@duylinh.net --dns cloudflare --domains derp-f2a4f360.duylinh.net --path /etc/derper/lego --accept-tos renew --days 30 && systemctl restart derper
```

## ACL — to apply in the admin console (Access Controls → policy file)

Step 1, add the region **without** omitting the defaults and verify:

```json
"derpMap": {
  "Regions": {
    "900": {
      "RegionID": 900,
      "RegionCode": "hkg",
      "RegionName": "HKG-01",
      "Nodes": [
        {
          "Name": "900a",
          "RegionID": 900,
          "HostName": "derp-f2a4f360.duylinh.net",
          "DERPPort": 8443,
          "STUNPort": 3478
        }
      ]
    }
  }
}
```

Check on two devices: `tailscale netcheck` shows region 900 with a latency;
`tailscale debug derp 900` reports no errors; `tailscale ping <peer>` shows
`via DERP(hkg)` for a pair without a direct path.

Step 2, only then, add `"OmitDefaultRegions": true` inside `derpMap`. From that
moment HKG-01 is the **only** relay for the tailnet: if it is down, every
device pair without a direct path loses connectivity, including
SSH-over-Tailscale from VNM-01. The public-IP `:17722` SSH path on every node
remains the fallback.

Rollback = remove `derpMap`; devices return to the default regions within a
minute. On the node: `systemctl disable --now derper; ufw delete allow 8443/tcp; ufw delete allow 3478/udp`.
