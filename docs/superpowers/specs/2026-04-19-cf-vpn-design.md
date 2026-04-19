# Design Spec — Cloudflare Tunnel VPN (VLESS + Trojan)

**Date:** 2026-04-19
**Status:** Draft, pending user approval
**Owner:** Đại ca
**Author:** Tiểu Long

## 1. Purpose

Xây một VPN cá nhân (1-5 users) chạy qua Cloudflare Tunnel + VPS backend để vượt firewall tại China và UAE. Mục tiêu: truy cập internet ổn định, ẩn IP VPS khỏi GFW/UAE scanner, dễ maintain và rotate domain khi cần.

## 2. Goals & Non-Goals

### Goals
- Multi-protocol: VLESS + Trojan cùng lúc (dự phòng khi 1 protocol bị chặn).
- Không expose IP VPS công khai — tất cả traffic đi qua Cloudflare edge.
- Deploy bằng Docker Compose, reproducible, pin version.
- Subscription URL chuẩn để import vào v2rayN/v2rayNG/Shadowrocket/Clash Meta.
- Healthcheck + auto-restart khi service down.
- Rotate domain dễ dàng (Đại ca có nhiều domain trên cùng CF account).

### Non-Goals
- Không làm panel multi-tenant (X-UI/Marzban) — scale chỉ 1-5 users.
- Không làm billing, traffic quota, user dashboard.
- Không support UDP-based protocol (Hysteria2, WireGuard) — CF Tunnel chỉ TCP/WS/gRPC.
- Không tối ưu cho gaming (latency thêm 20-50ms qua CF edge).
- Không Telegram alerting (basic logs + healthcheck đủ).
- Không fake website ở root path (trả 404, YAGNI cho scale 1-5 users).

## 3. Architecture

```
┌──────────────┐   TLS 443    ┌──────────────────┐   cloudflared tunnel   ┌──────────────────┐
│ Client (VN/  │ ──────────▶  │ Cloudflare Edge  │ ───── HTTP/2 ───────▶ │ VPS (Docker)     │
│  CN/UAE)     │   WSS        │ (vpn.domain.tld) │   outbound only       │ ┌──────────────┐ │
│ v2rayN/NG,   │              │                  │                        │ │ cloudflared  │ │
│ Shadowrocket │              │ - TLS cert       │                        │ │ (ingress     │ │
└──────────────┘              │ - Path-based     │                        │ │  router)     │ │
                              │   routing via    │                        │ └──────┬───────┘ │
                              │   tunnel ingress │                        │        │         │
                              └──────────────────┘                        │   /vless  /trojan│
                                                                          │ ┌──────▼───────┐ │
                                                                          │ │ xray-core    │ │
                                                                          │ │ :10001 VLESS │ │
                                                                          │ │ :10002 Trojan│ │
                                                                          │ └──────┬───────┘ │
                                                                          └────────┼─────────┘
                                                                                   ▼ freedom
                                                                              Internet
```

**Key properties:**
- VPS không bind host port — chỉ outbound qua cloudflared.
- TLS terminate tại CF edge, client chỉ thấy CF Anycast IP.
- Path routing ở tầng cloudflared ingress → không cần nginx.
- Inside-tunnel traffic là HTTP/2 plain (cloudflared lo encryption tới edge).

**Trade-offs chấp nhận:**
- CF Free plan có soft cap bandwidth — scale 1-5 users thường OK.
- Latency +20-50ms so với VPS trực tiếp.
- Nếu domain bị CF flag → rotate sang domain khác trong tài khoản.

## 4. File Layout

```
/opt/cf-vpn/
├── docker-compose.yml           # 2 services: xray + cloudflared
├── .env                         # TUNNEL_TOKEN, DOMAIN, UUIDs (gitignored)
├── .env.example                 # template commit vào git
├── .gitignore
├── xray/
│   ├── config.template.json     # template envsubst (committed)
│   └── config.json              # generated từ template (gitignored)
├── cloudflared/
│   ├── config.template.yml      # template envsubst (committed)
│   ├── config.yml               # generated (gitignored)
│   └── <tunnel-uuid>.json       # credentials (gitignored)
├── scripts/
│   ├── install.sh               # bootstrap (idempotent)
│   ├── gen-subscription.sh      # xuất URI/base64/QR
│   ├── add-user.sh <name>
│   ├── remove-user.sh <name>
│   ├── rotate-domain.sh <new-domain>
│   └── healthcheck.sh           # cron 5 phút
├── subscriptions/               # output (gitignored)
│   └── <user>.txt
├── docs/
│   └── TESTING.md               # manual test checklist
└── README.md                    # vận hành
```

## 5. Components

| Service | Image | Role | Restart | Healthcheck |
|---|---|---|---|---|
| `xray` | `teddysun/xray:<pinned>` | VLESS+Trojan WS inbound, freedom outbound | `unless-stopped` | TCP probe port 10001 |
| `cloudflared` | `cloudflare/cloudflared:<pinned>` | Tunnel client + ingress router | `unless-stopped` | `cloudflared tunnel info` exit 0 |

- Cả 2 join chung Docker network `cfvpn_net`.
- Xray expose port nội bộ 10001 (VLESS) và 10002 (Trojan). Không bind host.
- cloudflared không bind host — chỉ outbound tới CF.
- Log driver: `json-file`, `max-size: 10m`, `max-file: 3`.
- Version pin trong `.env`: `XRAY_VERSION`, `CLOUDFLARED_VERSION`. Không dùng `:latest`.

## 6. Data Flow

1. Client build `wss://vpn.domain.tld/vless?uuid=<UUID>` với Host header đúng.
2. DNS resolve → CF Anycast edge IP.
3. TLS 1.3 + WS handshake client ↔ CF edge. GFW/UAE chỉ thấy HTTPS tới CF.
4. CF edge match hostname → forward qua argo tunnel (HTTP/2) tới cloudflared local.
5. cloudflared match ingress rule: `/vless` → `xray:10001`, `/trojan` → `xray:10002`, fallback → `http_status:404`.
6. Xray verify UUID/password → freedom outbound → internet.

## 7. Config Templates

### 7.1 Xray (`xray/config.template.json`)

```json
{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {
      "tag": "vless-ws",
      "listen": "0.0.0.0",
      "port": 10001,
      "protocol": "vless",
      "settings": {
        "clients": [{"id": "${UUID_USER1}", "email": "user1@vpn"}],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "ws",
        "wsSettings": {"path": "/vless"}
      }
    },
    {
      "tag": "trojan-ws",
      "listen": "0.0.0.0",
      "port": 10002,
      "protocol": "trojan",
      "settings": {
        "clients": [{"password": "${TROJAN_PASS_USER1}", "email": "user1@vpn"}]
      },
      "streamSettings": {
        "network": "ws",
        "wsSettings": {"path": "/trojan"}
      }
    }
  ],
  "outbounds": [
    {"tag": "direct", "protocol": "freedom"},
    {"tag": "block",  "protocol": "blackhole"}
  ],
  "routing": {
    "rules": [
      {"type": "field", "ip": ["geoip:private"], "outboundTag": "block"}
    ]
  }
}
```

Block `geoip:private` outbound để user không dùng tunnel scan LAN của VPS.

### 7.2 cloudflared (`cloudflared/config.template.yml`)

```yaml
tunnel: ${TUNNEL_UUID}
credentials-file: /etc/cloudflared/${TUNNEL_UUID}.json
ingress:
  - hostname: ${DOMAIN}
    path: ^/vless$
    service: http://xray:10001
  - hostname: ${DOMAIN}
    path: ^/trojan$
    service: http://xray:10002
  - service: http_status:404
```

### 7.3 `.env.example`

```
DOMAIN=vpn.example.com
CF_API_TOKEN=                 # scoped: Zone:DNS:Edit + Account:Cloudflare Tunnel:Edit
TUNNEL_UUID=                  # populated by install.sh
UUID_USER1=                   # uuidgen
TROJAN_PASS_USER1=            # openssl rand -base64 24
XRAY_VERSION=24.11.30         # teddysun/xray tag (pin concrete tại install time)
CLOUDFLARED_VERSION=2025.2.0  # cloudflare/cloudflared tag (install.sh query latest stable nếu để trống)
```

## 8. Security Design

| Threat | Mitigation |
|---|---|
| GFW/UAE DPI detect VPN | WSS tới CF edge, cùng pattern HTTPS web thường, rất khó block toàn bộ CF. |
| Port scan tìm VPS | VPS không mở port inbound. `ufw default deny incoming`, chỉ allow SSH. |
| IP VPS lộ | cloudflared outbound-only, không A record trỏ về VPS. |
| Mò endpoint qua domain | Root path `/` trả 404. `/vless`, `/trojan` chỉ trả WS upgrade. |
| Tunnel abuse scan LAN | Xray route `geoip:private` → blackhole. |
| UUID/password leak | Mỗi user 1 UUID + 1 password riêng. Rotate qua `add-user.sh` + remove client cũ. |
| Credential leak trong git | `.env`, `subscriptions/`, `cloudflared/*.json` trong `.gitignore`. |
| Abuse bandwidth CF | Accept risk — scale 1-5 users không đáng lo. |

### Hardening (thực hiện trong `install.sh`)
- `ufw`: deny all incoming trừ SSH, allow all outgoing.
- Docker `read_only: true` cho xray và cloudflared rootfs (trừ volume config).
- Không mount `/var/run/docker.sock` vào container.
- Log rotation 10MB × 3 file per container.

### Không làm (YAGNI)
- Fail2ban cho xray (UUID 128-bit đủ mạnh).
- Encrypt `.env` at rest.
- Auto-rotate UUID theo lịch.
- CF WAF rule custom.

## 9. Operations

### 9.1 Bootstrap (`install.sh`)

Idempotent — chạy lại với domain tồn tại thì skip tạo tunnel, chỉ refresh config.

```
1. Check prereq: docker, docker compose v2, curl, jq, openssl, uuidgen, envsubst, qrencode
2. Đọc DOMAIN + CF_API_TOKEN (args hoặc prompt)
3. CF API:
   - Nếu tunnel cho DOMAIN chưa tồn tại: POST /accounts/:id/cfd_tunnel → lấy TUNNEL_UUID + credentials JSON
   - Ghi credentials vào cloudflared/<TUNNEL_UUID>.json (chmod 600)
   - POST /zones/:id/dns_records CNAME vpn → <TUNNEL_UUID>.cfargotunnel.com (proxied=true)
4. Gen secrets cho user đầu: UUID_USER1=$(uuidgen), TROJAN_PASS_USER1=$(openssl rand -base64 24)
5. Ghi .env
6. envsubst < xray/config.template.json > xray/config.json
   envsubst < cloudflared/config.template.yml > cloudflared/config.yml
7. docker compose up -d
8. Wait 10s, probe healthcheck
9. scripts/gen-subscription.sh user1 → in URI + QR + lưu subscriptions/user1.txt
```

### 9.2 User management

- `add-user.sh <name>`:
  - Cap 5 users (đếm trong config.json, warn nếu vượt).
  - Gen UUID + password mới.
  - `jq` append vào `clients` array của cả 2 inbound.
  - `docker compose restart xray` (downtime <2s chấp nhận được).
  - Xuất `subscriptions/<name>.txt`.
- `remove-user.sh <name>`: `jq` filter ra client có email `<name>@vpn` → restart xray.

### 9.3 Subscription output

Mỗi user xuất 3 format:

1. **VLESS URI:**
   `vless://<UUID>@${DOMAIN}:443?encryption=none&security=tls&type=ws&host=${DOMAIN}&path=%2Fvless&sni=${DOMAIN}#<name>-VLESS`

2. **Trojan URI:**
   `trojan://<PASS>@${DOMAIN}:443?security=tls&type=ws&host=${DOMAIN}&path=%2Ftrojan&sni=${DOMAIN}#<name>-Trojan`

3. **Base64 subscription** (import 1 URL lấy cả 2 protocol) — lưu `subscriptions/<name>.txt`. Chia sẻ qua kênh riêng (KHÔNG serve qua tunnel này để không làm lộ pattern).

4. **QR code** in terminal qua `qrencode -t UTF8`.

### 9.4 Healthcheck

- Docker `healthcheck` trên cả 2 service (interval 10s, retries 3).
- `restart: unless-stopped` → tự dậy sau VPS reboot.
- `scripts/healthcheck.sh` (cron 5 phút):
  - `curl -I --max-time 10 https://${DOMAIN}/vless` — expect HTTP 400 hoặc 426 = healthy.
  - Nếu 5xx/timeout 3 lần liên tiếp → log vào `/var/log/cf-vpn-health.log` + `docker compose restart`.

### 9.5 Multi-domain rotation (`rotate-domain.sh <new-domain>`)

Tận dụng Đại ca có nhiều CF domain.

```
1. Validate new-domain thuộc CF account (CF API GET zone).
2. Tạo tunnel mới cho new-domain (gọi install.sh với flag --rotate).
3. Swap DNS + .env → DOMAIN mới.
4. Regenerate subscriptions với DOMAIN mới.
5. Giữ tunnel cũ chạy 24h grace (cron cleanup sau 24h hoặc manual).
```

### 9.6 Logs
- `docker compose logs -f xray`
- `docker compose logs -f cloudflared`
- json-file rotate 10MB × 3.

## 10. Testing Strategy

### 10.1 Local smoke test (trên VPS)
```bash
docker compose ps                           # cả 2 Up + healthy
docker compose exec xray xray version
docker compose logs cloudflared | grep "Registered tunnel connection"
curl -I https://${DOMAIN}/                  # expect 404
curl -I https://${DOMAIN}/vless             # expect 400 hoặc 426
```

### 10.2 E2E test (client)
- Import subscription vào v2rayN Windows → test cả VLESS + Trojan outbound.
- `curl https://ifconfig.me` qua proxy → IP khác IP thật, match CF egress.
- DNS leak test: `https://dnsleaktest.com/`.
- Latency baseline ping 1.1.1.1 (ghi vào TESTING.md).

### 10.3 Bypass verification (China/UAE)
- Test qua user thật hoặc VPS test ở region target.
- Target sites: google.com / youtube.com (CN), facebook.com (UAE).
- Run liên tục 24-48h, ghi kết quả vào TESTING.md.

### 10.4 Scripts idempotency
- `install.sh` chạy 2 lần → lần 2 không tạo tunnel trùng.
- `add-user.sh` → user mới kết nối được, user cũ vẫn OK.
- `remove-user.sh` → user bị remove không kết nối được.
- `rotate-domain.sh` trên 1 domain phụ → full flow OK.

### 10.5 Failure scenarios
- Stop cloudflared → tunnel down → restart tự phục hồi.
- Stop xray → cloudflared 502 → healthcheck restart.
- VPS reboot → stack tự dậy.
- CF API token invalid → install.sh báo lỗi rõ ràng (không silent fail).

### 10.6 Out of scope
- Load test.
- Chaos engineering.
- CI/CD tự động.

### Deliverable
- `docs/TESTING.md` — checklist manual có tick boxes.
- README section "Verify installation" với 5 lệnh smoke test.

## 11. Open Questions / Future Work

- Nếu CF bắt đầu throttle bandwidth → cân nhắc nâng CF Pro ($20/tháng) hoặc chuyển sang Approach B (nginx + fake site) để giảm traffic pattern đáng nghi.
- Nếu nhu cầu UDP (gaming, voice call chất lượng cao) xuất hiện → cần kiến trúc khác (Hysteria2 trực tiếp trên VPS, không qua CF tunnel).
- Multi-node/failover (VPS HK + SG) — để sau khi setup 1 node chạy ổn.

## 12. Success Criteria

1. Client iOS/Android/Windows import subscription → cả 2 protocol kết nối được.
2. Truy cập được google.com / youtube.com từ Trung Quốc hoặc VPS test ở CN region.
3. `curl ifconfig.me` qua proxy trả IP CF, không phải IP VPS.
4. Port scan VPS từ internet → không mở port nào (trừ SSH do Đại ca).
5. `install.sh` chạy 2 lần không lỗi — idempotent.
6. `rotate-domain.sh` đổi domain thành công trong <5 phút, subscriptions mới chạy được.
7. VPS reboot → stack tự dậy trong <60s.
