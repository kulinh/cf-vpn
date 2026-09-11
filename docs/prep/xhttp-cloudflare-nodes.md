# XHTTP for the cloudflare-mode nodes (OR-001, VNM-01, JPY-01) — prepared, not enabled

Status: **prepared 2026-09-12, nothing changed on any node.** Xray-core
26.3.27 logs at every start that HTTPUpgrade is deprecated and asks for a
migration to XHTTP. This document is the config to test it as a **second
inbound next to HTTPUpgrade**, so the working route is never touched.

## What is already known

`docs/superpowers/plans/2026-05-02-reality-xhttp-migration.md` records the
first attempt: XHTTP through cloudflared failed — `stream-up` got a 404 and
`stream-one` a 403 at the Cloudflare edge — and HTTPUpgrade was adopted. The
untested mode is **`packet-up`** (each upload is a plain POST, downloads a
plain streamed GET), which is the mode designed for CDNs that buffer or
reject streaming request bodies. Test that first; if it also fails, XHTTP
through the Cloudflare tunnel is not viable and this document can be closed.

## Important: hand edits do not survive

The xray config and the cloudflared ingress are **rendered from templates**
(`internal/templates/render.go`, `RenderXrayCloudflareHTTPUpgrade` and
`cloudflaredWithAdminTemplate`). Every panel user sync (`POST
/admin/v1/sync`), `cfvpnctl upgrade` and `cfvpnctl hy2 ...` re-renders them
and drops a hand-added inbound. So:

- for a **test**: hand-edit, test, then either roll back or accept that the
  next sync removes it;
- for **production**: add the inbound + ingress line to the two renderers
  (gated by a new env key, e.g. `XHTTP_ENABLED=1`, the same pattern as
  `HY2_ENABLED` / `CLOUDFLARED_PROTOCOL`) and ship it through `cfvpnctl`.

## Test configuration (per node)

Second xray inbound, appended to `inbounds` in `/etc/cfvpn/xray/config.json`
(same clients block as the existing `vless-httpupgrade` inbound):

```json
{
  "tag": "vless-xhttp",
  "listen": "127.0.0.1",
  "port": 10002,
  "protocol": "vless",
  "settings": { "clients": [ { "id": "<same uuid as vless-httpupgrade>", "email": "kulinh@vpn" } ], "decryption": "none" },
  "streamSettings": {
    "network": "xhttp",
    "xhttpSettings": { "path": "/api/v2/stream", "host": "<DOMAIN>", "mode": "packet-up" }
  },
  "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"] }
}
```

cloudflared ingress line, inserted **before** the existing `path: ^/api/v1/sync`
rule in `/etc/cfvpn/cloudflared/config.yml` (first match wins):

```yaml
  - hostname: <DOMAIN>
    path: ^/api/v2/stream
    service: http://127.0.0.1:10002
```

Client URI (Shadowrocket / xray):

```
vless://<uuid>@<DOMAIN>:443?encryption=none&security=tls&type=xhttp&host=<DOMAIN>&path=%2Fapi%2Fv2%2Fstream&mode=packet-up&sni=<DOMAIN>#kulinh%40<NODE>-XHTTP
```

Values per node (`DOMAIN` = `vpn_host` in D1):

| Node | DOMAIN |
|---|---|
| OR-001 | static-df60bd79.duylinh.org |
| VNM-01 | edge-f7c5683a.888vn.net |
| JPY-01 | edge-fd34b370.rwl247.dev |

## Enable (test) — one node, OR-001 first

```bash
cp /etc/cfvpn/xray/config.json /root/xray.json.pre-xhttp
cp /etc/cfvpn/cloudflared/config.yml /root/cloudflared.yml.pre-xhttp
# edit both files as above
/usr/local/bin/xray run -test -c /etc/cfvpn/xray/config.json     # must print "Configuration OK"
systemctl restart cfvpn-xray && systemctl restart cfvpn-cloudflared
ss -lntp | grep -E '1000[12]'                                     # both inbounds listening
journalctl -u cfvpn-cloudflared -n 8 --no-pager | grep -c Registered   # 4
```

Verify from VNM-01: add the XHTTP line to a file and run
`python3 scripts/fleet-probe.py --once --sub-file that-file` (the probe
already understands `type=xhttp` + `mode`). Expect `OK` for both the XHTTP
and the existing HTTPUpgrade route. Then test from a phone on mobile data; the
China test is the real one.

## Rollback

```bash
cp /root/xray.json.pre-xhttp /etc/cfvpn/xray/config.json
cp /root/cloudflared.yml.pre-xhttp /etc/cfvpn/cloudflared/config.yml
systemctl restart cfvpn-xray && systemctl restart cfvpn-cloudflared
```

Or simply run `cfvpnctl hy2 enable|disable` (whichever the node already is) —
the re-render restores the canonical config.
