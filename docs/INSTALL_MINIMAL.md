# Install cf-vpn on a new VPS (minimal)

This is the canonical guide for provisioning a fresh cf-vpn node. It uses
`scripts/install-node.sh`, which validates inputs, builds both binaries
(`cfvpnctl` + `cfvpn-agent`), writes the full env file the Go installer
expects, and verifies systemd units after install.

## 1) Prerequisites

- A clean Debian 12+ or Ubuntu 22.04+ VPS with root access (or a sudo user).
- A Cloudflare API token with these scopes:
  - **Zone → DNS → Edit** on every zone in `internal/zones/pool.go`
    (`888vn.net`, `dongnat247.com`, `abony.xyz`, `duylinh.org`, `duylinh.net`,
    `rwl247.dev`, `rwl265.com`, `rwl265.org`, `rwl.one`) — the installer
    picks a `DOMAIN` from this pool when one is not supplied, and Lego needs
    DNS-01 access to issue the TLS cert.
  - **Zone → DNS → Edit** on `rwl247.dev` specifically — the admin host
    `<NODE_ID>.rwl247.dev` is always created here, regardless of `DOMAIN`.
  - **Account → Cloudflare Tunnel → Edit** on the target Cloudflare account —
    the installer creates a named tunnel and routes the admin hostname
    through it.
- Your Cloudflare **Account ID** (visible on the Cloudflare dashboard
  right-hand sidebar of any zone in the account).

## 2) Debian 12/13 packages and runtime dependencies

`scripts/install-node.sh` installs the OS packages it needs on Debian 12/13:

```bash
apt-get install -y \
  curl jq openssl uuid-runtime qrencode ca-certificates git iproute2 \
  golang-go ufw tar gzip coreutils bash systemd dnsutils
```

The Go installer then downloads and installs these runtime binaries when missing:

| Binary | Installed by | Purpose |
|---|---|---|
| `xray` | `https://github.com/XTLS/Xray-install/raw/main/install-release.sh` | VLESS WebSocket/TLS server |
| `cloudflared` | latest `cloudflared-linux-amd64` GitHub release | Cloudflare admin tunnel and optional Cloudflare-mode VLESS ingress |
| `hysteria` | `https://get.hy2.sh` | Hysteria2 UDP proxy server |
| `lego` | latest `go-acme/lego` `linux_amd64.tar.gz` GitHub release | ACME DNS-01 certificates through Cloudflare DNS |

Network requirements:

- outbound HTTPS/443 to GitHub, Cloudflare API, Let's Encrypt, and OS package mirrors
- outbound DNS/53 to recursive resolvers such as `1.1.1.1` and `8.8.8.8`
- direct mode: inbound TCP/443 to the VPS
- both modes: inbound UDP on the generated Hysteria2 port (`20000-60000`)

## 3) Clone the repo on the VPS

```bash
git clone https://github.com/kulinh/cf-vpn.git
cd cf-vpn
```

## 4) Run the bootstrap

```bash
sudo -E \
  CF_API_TOKEN='your_token' \
  CF_ACCOUNT_ID='your_account_id' \
  NODE_ID='hk-01' \
  bash scripts/install-node.sh
```

That is the only command you need. Optional environment overrides:

| Var          | Default       | Notes                                                                                                                |
| ------------ | ------------- | -------------------------------------------------------------------------------------------------------------------- |
| `NODE_ID`    | **required**  | Single DNS label, lowercase `[a-z0-9-]`, ≤63 chars. Used as `<NODE_ID>.rwl247.dev` for the admin tunnel.             |
| `USER1_NAME` | `user1`       | Must match `^[A-Za-z0-9_-]{1,32}$`. Initial VPN account.                                                              |
| `MODE`       | `auto`        | `direct` (TCP/443 on this host), `cloudflare` (VLESS rides Cloudflare Tunnel), or `auto` (probe :443, fall back).    |
| `DOMAIN`     | auto-selected | Must be a subdomain of a zone in `DefaultPool`. If empty the installer picks one and registers the DNS record itself. |

### Mode decision matrix

- **direct** — fastest path. Requires nothing else listening on `:443`.
  `cfvpnctl install` runs a bind probe; if `:443` is busy the installer
  refuses with `port_443_busy`.
- **cloudflare** — VLESS is bound to `127.0.0.1:10001` and proxied through
  the Cloudflare Tunnel. Use this when `:443` is already taken or when the
  provider blocks inbound TCP/443.
- **auto** *(default in `install-node.sh`)* — probes `:443`. If anything is
  serving or listening there, falls back to `cloudflare`; otherwise picks
  `direct`.

Hysteria2 (UDP, random port in `20000-60000`) is always installed in both
modes. The Cloudflare Tunnel always carries the admin/control plane,
regardless of `MODE`.

## 5) Verify the install

```bash
sudo cfvpnctl status
sudo cfvpnctl healthcheck run
sudo cfvpnctl gen-sub user1
```

`install-node.sh` already runs these checks and reports each unit's state:

```
cfvpn-xray
cfvpn-hysteria
cfvpn-cloudflared
cfvpn-agent
```

All four must be `active`. The first `healthcheck run` may report failure
for ~60s while the Cloudflare Tunnel registers — re-run after a minute.

## 6) Troubleshooting

- **`mode_required` / `node_id_required`** — the env file is missing keys.
  Re-run the bootstrap; it always writes the full set.
- **`port_443_busy`** — something already binds `:443`. Either stop it
  (`ss -tlnp 'sport = :443'`) or set `MODE=cloudflare` and re-run.
- **`lego ... timeout`** — Cloudflare DNS-01 propagation. The bootstrap probes
  Cloudflare authoritative DNS first; when the VPS firewall blocks it, the
  script exports `LEGO_DISABLE_CP=1` and
  `LEGO_DNS_RESOLVERS=1.1.1.1:53,8.8.8.8:53`, causing lego to verify through
  recursive resolvers with `--dns.propagation-rns`. Do not combine this mode
  with `LEGO_PROPAGATION_WAIT`; lego 4.35+ rejects that combination.
- **`cfvpn-agent` inactive** — the agent binary is missing. Re-run the
  bootstrap; it always installs both binaries to `/usr/local/bin/`.
- **healthcheck fails after install** — wait 60s and re-run. The admin
  tunnel record can lag the install by up to a minute.

## 7) Re-running / upgrading

The bootstrap is idempotent for users and config, but to upgrade an existing
node prefer:

```bash
sudo cfvpnctl install --upgrade
```

Use `install-node.sh` for fresh provisioning only.
