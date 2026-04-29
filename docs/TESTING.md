# Testing Checklist — cf-vpn

Run these checks after `sudo cfvpnctl install --mode direct` or `sudo cfvpnctl upgrade --mode direct` completes and before declaring a direct node healthy. For VPSs that cannot expose TCP 443, run the same checklist with `sudo cfvpnctl upgrade --mode cloudflare` and use the Cloudflare-mode notes below.

## 0. Prerequisites

- [ ] Debian/Ubuntu VPS with systemd
- [ ] `curl`, `jq`, `openssl`, `uuidgen`, `qrencode` installed
- [ ] Cloudflare account with DNS zones configured
- [ ] Cloudflare API token with `Zone:DNS:Edit` and `Account:Cloudflare Tunnel:Edit`
- [ ] root access

## 1. Bootstrap

- [ ] `make build` succeeds and produces `bin/cfvpnctl` plus `bin/cfvpn-agent`
- [ ] `sudo install -m 0755 bin/cfvpnctl /usr/local/bin/cfvpnctl`
- [ ] `sudo install -m 0755 bin/cfvpn-agent /usr/local/bin/cfvpn-agent`
- [ ] `/etc/cfvpn/cfvpn.env` exists with Cloudflare credentials and node inputs, chmod 600
- [ ] Fresh deploy: `sudo cfvpnctl install --mode direct` completes without error
- [ ] Existing deploy: `sudo cfvpnctl upgrade --mode direct` completes without error
- [ ] Restricted VPS deploy: `sudo cfvpnctl upgrade --mode cloudflare` completes without error when TCP 443 cannot be exposed directly

## 2. Local smoke test on VPS

- [ ] `systemctl is-active cfvpn-xray cfvpn-hysteria cfvpn-cloudflared` reports `active`
- [ ] `sudo cfvpnctl status` reports VLESS, Hysteria2, admin tunnel, direct host, public IP, HY2 host, and HY2 port
- [ ] `journalctl -u cfvpn-cloudflared 2>&1 | grep -c "Registered tunnel connection"` returns at least 1
- [ ] `curl -k -I -s https://${DOMAIN}/vless` returns a WebSocket/TLS rejection such as HTTP 400 or 426
- [ ] `sudo cfvpnctl healthcheck run` prints an OK health result

## 3. Public reachability and port check

From a machine outside the VPS:

- [ ] Direct mode: `dig +short ${DOMAIN}` resolves to the VPS public IP
- [ ] Direct mode: `nmap -Pn -p 22,443 <vps-ip>` shows SSH and TCP 443 as expected
- [ ] Cloudflare mode: `${DOMAIN}` routes through cloudflared and `journalctl -u cfvpn-cloudflared` shows registered tunnel connections
- [ ] UDP Hysteria2 port from `cfvpnctl status` is reachable from a client network that supports UDP
- [ ] Direct-mode client hostnames are DNS-only, not proxied

## 4. End-to-end client test

**VLESS:**

- [ ] Import the user subscription from `sudo cfvpnctl gen-sub <user>` or panel `/sub/:token`
- [ ] Connect with v2rayN, v2rayNG, Shadowrocket, or another VLESS client
- [ ] `https://ifconfig.me` through the proxy returns the VPS egress IP
- [ ] `https://www.youtube.com` loads through the proxy

**Hysteria2:**

- [ ] Confirm the subscription includes a `hysteria2://` line for the node
- [ ] Connect with a Hysteria2-capable client using the generated port and obfs password
- [ ] `https://ifconfig.me` through the proxy returns the VPS egress IP
- [ ] A UDP-sensitive workload works on a client network that allows UDP

## 5. DNS leak test

- [ ] Connect through either protocol and visit `https://dnsleaktest.com/`
- [ ] DNS results do not expose the local ISP resolver

## 6. Latency baseline

Record for reference:

| Region | VLESS latency | HY2 latency | Date tested |
|---|---:|---:|---|
| VN |  |  |  |
| CN |  |  |  |
| UAE |  |  |  |

## 7. Bypass verification

**China:**

- [ ] google.com loads
- [ ] youtube.com plays video
- [ ] 24h stability check still connects

**UAE:**

- [ ] facebook.com loads
- [ ] WhatsApp chat works
- [ ] 24h stability check still connects

## 8. Command idempotency

- [ ] `sudo cfvpnctl install --mode direct` twice in a row does not create duplicate runtime state
- [ ] `sudo cfvpnctl upgrade --mode direct` twice in a row preserves users and remains healthy
- [ ] `sudo cfvpnctl add-user alice` then again fails with `already exists`
- [ ] `sudo cfvpnctl remove-user alice` then again fails with `not found`

## 9. Failure recovery

- [ ] `sudo systemctl stop cfvpn-cloudflared` makes admin control unavailable; restarting it restores admin connectivity
- [ ] `sudo systemctl stop cfvpn-xray` makes VLESS fail; restarting it restores VLESS
- [ ] `sudo systemctl stop cfvpn-hysteria` makes HY2 fail; restarting it restores HY2
- [ ] `sudo systemctl reboot` returns all cfvpn services to `active` within 60 seconds

## 10. User management

- [ ] `sudo cfvpnctl add-user alice` creates VLESS and HY2 credentials
- [ ] `sudo cfvpnctl gen-sub alice` prints both VLESS and HY2 subscription lines
- [ ] Removing a user revokes both protocol credentials after service reload

## 11. Domain rotation

- [ ] `sudo cfvpnctl rotate-domain <new-domain>` creates new VLESS and HY2 hosts
- [ ] New subscription works on clients after refresh
- [ ] Old DNS records are removed or cleaned according to rotation output
- [ ] Panel node row reflects the new direct hosts, public IP, HY2 port, and status after sync/refresh

## 12. Control panel smoke

- [ ] Command Center renders node status, direct hosts, latency, and rotate action
- [ ] Nodes page renders VLESS host, HY2 host:port, public IP, and mode
- [ ] Users page can copy the public subscription URL and show the QR modal
- [ ] User node sync reports added, failed, and total counts
- [ ] Events page loads recent audit events

CLI smoke equivalent:

- [ ] `npm --prefix panel/worker run check`
- [ ] `npm --prefix panel/worker test`
- [ ] `npm --prefix panel/web run test:run`
- [ ] `npm --prefix panel/web run build`
