# Custom Tailscale DERP relays on HKG-01, JPY-01, JPY-03 and HAN-01 — DEPLOYED 2026-09-12

Status: `derper` runs on HKG-01 and the tailnet policy carries `derpMap`
with regions 900 (HKG-01) and 901 (JPY-01). `OmitDefaultRegions` was set to `true` for ~40
minutes on 2026-09-12 and then **reverted to `false`** (operator decision,
see the SIN-01 gap below): the public relays are back and region 900 is an
extra region. The flag is now managed by `cfvpnctl derp china-mode on|off`
(on = only the custom regions, for travel inside China).

**Update 2026-09-13:** china-mode is kept **on permanently** (`cfvpnctl derp
china-mode on`) — the default relays are blocked from China and the three
private regions (900 HKG-01, 901 JPY-01, 902 JPY-03) serve everywhere, so the
travel-mode switch (and the Telegram control bot that flipped it) was removed.
**Update 2026-09-15:** china-mode is **off** again (operator decision: travel to
the UAE and elsewhere must work with no switch; globalping from du AS15802 and
Etisalat AS5384 reached controlplane/login.tailscale.com and the Dubai relays
derp23b/c/d at 49–122 ms, the private relays at 650–1900 ms). Region **903
HAN-01** added so devices in Vietnam home on a relay 5–7 ms away instead of
HKG-01. The test table below records the two states as measured on 2026-09-12.

| Item | HKG-01 (region 900) | JPY-01 (region 901) |
|---|---|---|
| Hostname | `derp-f2a4f360.duylinh.net` → 96.9.228.81 | `derp-da32d5af.duylinh.net` → 45.143.131.36 |
| Why | first relay | reachable from SIN-01 (HKG-01 is not, see below) |

Both: A record not proxied, no AAAA (tailscale logs a harmless v6 lookup error); same unit, ports, cert handling and renew cron (`/etc/cron.d/derper-cert-renew`, 04:17 on HKG-01 / 04:23 on JPY-01 on the 1st of each month); certs expire 2026-12-10.

| Item | Value |
|---|---|
| DERP | TCP **8443** (443 is xray Reality on this node) |
| STUN | UDP **3478** |
| Binary | `/usr/local/bin/derper` (tailscale.com/cmd/derper@latest, built on VNM-01) |
| Unit | `derper.service`: `derper --hostname <host> -a :8443 --http-port -1 --stun --stun-port 3478 --certmode manual --certdir /etc/derper --verify-clients` |
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
work (it is the providers' routing between their own sites) which is why region 901 on
JPY-01 exists: with china-mode on, SIN-01 relays through JPY-01 (verified
`tailscale debug derp 901` from SIN-01: DERP connection + STUN OK; SIN-01
netcheck shows `jpy 70 ms`, HKG-01 blank). A SIN-01 ↔ HKG-01 Tailscale path
still cannot exist. The SSH fallback via public IP `:17722` is unaffected.

## china-mode test (2026-09-12, via `cfvpnctl derp`)

| | VNM-01 netcheck | SIN-01 netcheck |
|---|---|---|
| china-mode **on** | only `hkg 58 ms (HKG-01)`, `jpy 121 ms (JPY-01)` | `jpy 70 ms (JPY-01)`; HKG-01 unreachable | 
| china-mode **off** | Hong Kong 25, HKG-01 50, Singapore 54, Tokyo 64, JPY-01 111 ms | public relays back |

SSH over Tailscale to SIN-01, JPY-02, USA-01 worked in both states. Final
state: **off**. Managed with `cfvpnctl derp china-mode on|off` (README);
every run snapshots the policy under `/root/cfvpn-backups/acl/`.

## The policy snippet that is live

```json
"derpMap": {
  "OmitDefaultRegions": false,
  "Regions": {
    "900": { "RegionID": 900, "RegionCode": "hkg", "RegionName": "HKG-01", "Nodes": [{ "Name": "900a", "RegionID": 900, "HostName": "derp-f2a4f360.duylinh.net", "DERPPort": 8443, "STUNPort": 3478 }] },
    "901": { "RegionID": 901, "RegionCode": "jpy", "RegionName": "JPY-01", "Nodes": [{ "Name": "901a", "RegionID": 901, "HostName": "derp-da32d5af.duylinh.net", "DERPPort": 8443, "STUNPort": 3478 }] },
    "902": { "RegionID": 902, "RegionCode": "osa", "RegionName": "JPY-03", "Nodes": [{ "Name": "902a", "RegionID": 902, "HostName": "derp-de29e117.duylinh.net", "DERPPort": 8443, "STUNPort": 3478 }] }
  }
}
```

Rollback = remove `derpMap` from the policy; on the node
`systemctl disable --now derper; ufw delete allow 8443/tcp; ufw delete allow 3478/udp`.

## Region 902 — JPY-03 (Oracle Cloud Osaka, arm64), added 2026-09-12 21:40

Same recipe: `derper` 1.102.4 cross-compiled on VNM-01 (`GOOS=linux GOARCH=arm64 go install tailscale.com/cmd/derper@v1.102.4`, binary under `$GOPATH/bin/linux_arm64/`), host `derp-de29e117.duylinh.net` → 129.225.185.197 (A, not proxied), lego DNS-01 cert expiring 2026-12-11, `/etc/cron.d/derper-cert-renew` at 04:31 on the 1st, unit identical to JPY-01's, ufw 8443/tcp + 3478/udp, plus the OCI VCN security list for the same two ports. derper ignores bare STUN binding requests, so a raw-socket STUN test says nothing — verify UDP reachability with a packet counter (`iptables -I INPUT -p udp --dport 3478 -j ACCEPT` + `-L -v`) or simply `tailscale netcheck` from another node. Netcheck after the add: osa 121 ms from VNM-01, 118 ms from SIN-01 (which now has two usable private relays), 0.5 ms locally.

## Region 903 — HAN-01 (Hanoi, AS63734), added 2026-09-15

Chosen as the strongest fleet VPS with a Vietnamese IP (4 vCPU / 8 GB, idle,
IPv4 + IPv6; VNM-02 is 2 vCPU / 4 GB with n8n and no IPv6; VNM-01 is the home PC,
not 24/7). Same recipe as 902: `derper` 1.102.4 (amd64 binary copied from HKG-01),
host `derp-bb5eccce.duylinh.net` → A 103.199.17.69 + AAAA 2404:fbc0:0:209c::a (not
proxied), lego DNS-01 cert expiring 2026-12-14, `/etc/cron.d/derper-cert-renew`
at 04:37 on the 1st, unit identical to JPY-03's, ufw 8443/tcp + 3478/udp (xray
keeps 443). VNM-01 netcheck after the add: `han 6.7 ms (HAN-01)`, Hong Kong 36,
Singapore 45, HKG-01 55. Rollback: `cfvpnctl derp region remove --id 903`, then on
HAN-01 `systemctl disable --now derper; ufw delete allow 8443/tcp; ufw delete allow 3478/udp`.
