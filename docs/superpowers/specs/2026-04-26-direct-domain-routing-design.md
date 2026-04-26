---
name: direct-domain-routing-design
description: Replace Cloudflare-tunnel data plane with direct domain → VPS IP routing (Xray native TLS, wildcard cert via acme.sh, Cloudflare API for A records). Admin plane keeps cloudflared.
type: project
---

# Direct Domain Routing Design

## Context

Hiện tại cf-vpn route data theo:

```
Client → CF edge (TLS, WSS) → cloudflared tunnel → Xray (localhost) → internet
```

Khi test ở Trung Quốc, đường đi qua CF edge cho ping rất cao (400ms - >2000ms). Mục tiêu: cho client kết nối **trực tiếp** vào VPS bằng domain (DNS-only A record), bỏ qua CF edge cho data plane. Admin plane (panel ↔ agent) **giữ nguyên** qua cloudflared để không phá auth/CF Access.

Quyết định kiến trúc đã chốt với user:

- **Direct thay thế hoàn toàn tunnel** cho data plane (không coexist).
- **Wildcard TLS cert per zone** (`*.zone.com`) qua acme.sh + Cloudflare DNS-01.
- **Public IP do agent tự detect** qua ipify, cache 5 phút.
- **Domain cũ xoá ngay** sau rotate (không grace period).
- **Admin plane giữ cloudflared** (không thay đổi auth model).
- **TLS terminator: Xray native** (không thêm Caddy).

## Architecture

```
─── DATA PLANE (direct, ping thấp) ──────────────────────────────────────
Client (VLESS/Trojan WS over TLS)
  │  TLS 443 + SNI = vpn_host
  ▼
DNS A record (proxied:false) → IP_VPS
  ▼
VPS:443  →  Xray (native TLS, wildcard cert /etc/cfvpn/certs/<zone>/)
              ├─ path /vless  → VLESS inbound (UUID)
              └─ path /trojan → Trojan inbound (password)

─── ADMIN PLANE (giữ nguyên qua cloudflared) ────────────────────────────
Panel (Cloudflare Worker)
  │  HTTPS + CF Access service token
  ▼
CF edge → cloudflared tunnel → cfvpn-agent (127.0.0.1:6788)
                                  │
                                  ├─ /admin/v1/status
                                  ├─ /admin/v1/healthcheck
                                  ├─ /admin/v1/sync
                                  ├─ /admin/v1/rotate-domain
                                  └─ /admin/v1/issue-cert  (mới)
```

### Trước / Sau

| Khía cạnh | Trước | Sau |
|---|---|---|
| Data path | Client → CF edge → cloudflared → Xray | Client → DNS-only A → Xray:443 (TLS) |
| DNS record kiểu | CNAME proxied (orange) → tunnel | A DNS-only (gray) → IP VPS |
| TLS termination | CF edge | Xray native, wildcard per zone |
| Rotate | New tunnel + new CNAME + restart cloudflared | New A record + Xray reload (~5s) |
| Tunnel UUID lifecycle | Tạo/xóa mỗi rotate | Tạo 1 lần (admin tunnel), không đổi khi rotate |
| Sub URL `/sub/<token>` | host = proxied vpn_host | host = direct vpn_host (token không đổi) |

### Component giữ nguyên

- Sub link `/sub/<32-hex-token>` (Worker)
- CF Access auth cho `/api/*`
- Agent auth bằng CF Access service token
- Cloudflared service trên VPS (nhưng ingress chỉ còn admin_host)
- Toàn bộ panel UI structure (chỉ tweak Node card + add zone cert button)

### Component bị xoá / điều chỉnh

- Cloudflared ingress cho `/vless` và `/trojan` — bỏ
- Tunnel UUID đổi mỗi rotate — bỏ
- `cfvpnctl rotate-domain --cleanup <uuid>` — deprecate (A record xoá luôn trong rotate)

## Cert Management

**Mục tiêu:** mỗi zone enabled trong panel có 1 wildcard cert phủ mọi subdomain → rotate không cần issue cert mới.

### Lifecycle

1. **Bootstrap khi add zone** (lần đầu):
   - Panel gọi `POST /api/zones/:name/issue-cert` → forward xuống agent (mỗi node liên quan).
   - Agent chạy: `acme.sh --issue --dns dns_cf -d zone.com -d *.zone.com`.
   - acme.sh đọc env `CF_Token` (lấy từ `/etc/cfvpn/cfvpn.env`, scope `Zone:DNS:Edit`).
   - Cert install vào `/etc/cfvpn/certs/<zone>/{fullchain.pem,privkey.pem}`.
   - Reload hook: `systemctl reload cfvpn-xray.service`.

2. **Renew tự động:**
   - acme.sh tự cài cron `0 0 * * *` lúc `--install`.
   - Renew khi cert <30 ngày, hook reload Xray (Xray hỗ trợ SIGHUP để reload không drop kết nối).
   - Idempotent, không cần panel can thiệp.

3. **State trên VPS:**
   ```
   /etc/cfvpn/certs/<zone>/
     fullchain.pem    (chmod 644)
     privkey.pem      (chmod 600)
   /root/.acme.sh/    (acme.sh tự quản)
   ```

### Multi-zone trên 1 VPS

- Xray template hỗ trợ nhiều `tlsSettings.certificates[]` — mỗi cert ứng 1 zone.
- Xray match cert theo SNI client gửi.
- Khi node switch zone (rotate sang zone mới), agent đảm bảo cert zone đó đã có (issue lazy nếu chưa).

### Cert là per-VPS, không share

Mỗi VPS có acme.sh riêng và `/etc/cfvpn/certs/` riêng. Không có cơ chế share cert giữa nodes:

- Lần đầu node được rotate sang 1 zone → agent của node đó issue cert local.
- Panel "Issue cert" button (Section 4.2) là tiện ích pre-warm: nó **fan-out** call `/admin/v1/issue-cert` xuống MỌI active node, mỗi node tự issue cert riêng.
- Lazy issue trong rotate flow vẫn hoạt động — button này không bắt buộc dùng.

### Failure handling

- Issue cert fail → log, panel hiện status; rotate block đến khi cert OK.
- DNS-01 propagate có thể 30-120s; agent timeout 180s cho lệnh issue.

### Tại sao acme.sh thay vì certbot

- Pure shell, không cần Python runtime trên VPS minimal.
- Plugin `dns_cf` native cho Cloudflare.
- Cron tự cài, hook script đơn giản.

## Rotate Flow

**Trigger:** Click "Rotate" trên Node card (UI hiện có, không đổi text).

```
[Panel Worker]  POST /api/nodes/:id/rotate
  1. pickEnabledZone() → { name: "example.com", cf_zone_id: "abc..." }
  2. newHost = randomHex(4) + "." + zone.name        ví dụ: "7f3a.example.com"
  3. Lookup old zone:
       SELECT cf_zone_id FROM zones WHERE name = nodes.zone
       (nodes.zone là tên zone hiện tại của node)
  4. callAgent(node.admin_host, POST /admin/v1/rotate-domain, {
       new_host:    "7f3a.example.com",
       new_zone_id: "abc...",
       old_host:    "<current vpn_host>",
       old_zone_id: "<lookup result>"   // null nếu zone cũ không còn trong DB
     }, timeout=60s)

[Agent on VPS]  /admin/v1/rotate-domain
  1. Detect public IP (cache 5 phút):
       GET https://api.ipify.org → "203.0.113.42"
  2. Ensure wildcard cert cho new zone:
       Nếu /etc/cfvpn/certs/<zone>/ chưa có → acme.sh issue (block, ~30-60s).
  3. Cloudflare API:
       POST /zones/<new_zone_id>/dns_records
         { type: "A", name: "7f3a", content: "203.0.113.42",
           proxied: false, ttl: 60 }
  4. Render Xray config:
       - listen 0.0.0.0:443
       - tlsSettings.certificates trỏ vào cert của new zone
       - path /vless và /trojan giữ nguyên
       - UUID/password user GIỮ NGUYÊN
       - Atomic write /etc/cfvpn/xray/config.json
  5. Reload Xray:  systemctl reload cfvpn-xray.service  (SIGHUP)
  6. Update env file:  DOMAIN=7f3a.example.com
  7. Best-effort delete old A record:
       Tìm dns_records?name=<old_host>&type=A trong old_zone_id, DELETE.
       Nếu fail: log warning, KHÔNG return error.
  8. Return 200 { vpn_host: "7f3a.example.com", public_ip: "203.0.113.42" }

[Panel Worker]
  - UPDATE nodes SET vpn_host=?, zone=?, public_ip=?, status='active' WHERE id=?
  - logEvent("node.rotate", "ok", {old_host, new_host, public_ip})
  - Return 200 to UI

[Mobile app]
  - Shadowrocket / v2rayNG pull /sub/<token> theo schedule (24h interval header)
  - Sub Worker đọc nodes.vpn_host mới → trả VLESS/Trojan URI mới
  - App reconnect tới 7f3a.example.com → DNS resolve thẳng IP VPS → ping thấp
```

### Idempotency / failure modes

| Bước fail | Hệ quả | Recovery |
|---|---|---|
| Detect IP | Return 502 ngay, chưa đụng gì | User retry |
| Ensure cert | Return 502, A record chưa tạo | User check CF token / DNS, retry |
| Tạo A record OK, write Xray config fail | DNS đã trỏ về IP nhưng Xray chưa serve. **Rollback**: delete A record vừa tạo trước khi return error | Auto rollback |
| Reload Xray fail | DNS + config đã update, Xray chưa pick up. Return error, log; admin handle (SSH restart) | Manual |
| Delete old A record fail | Domain mới hoạt động, domain cũ dangling. Best-effort, log warning, không return error | Cron cleanup hoặc manual |

**Tốc độ:** rotate ~3-8s khi cert đã có. Lần đầu trên zone mới thêm ~30-60s issue cert.

**Subscription token KHÔNG đổi:**

- `users.sub_token` (32-hex) bất biến qua rotate.
- App vẫn dùng URL `https://panel/sub/<same-token>`.
- Sub Worker khi serve trả `vless://...@7f3a.example.com:443/...` (host mới từ `nodes.vpn_host`).
- App auto pick up ở lần pull tiếp theo.

## Schema Changes (D1)

Migration mới `panel/worker/migrations/0004_nodes_public_ip.sql`:

```sql
ALTER TABLE nodes ADD COLUMN public_ip TEXT;
```

Không xoá cột nào. Tunnel state (UUID) vốn nằm ở env file VPS, không có trong DB.

## Code Changes

### Worker (`panel/worker/`)

| File | Thay đổi |
|---|---|
| `src/routes/nodes.ts` | `nodeRotate`: gửi thêm `old_host` + `old_zone_id` xuống agent. Đọc `public_ip` từ response, cập nhật DB. Bỏ logic tunnel_uuid trong response. Timeout 60s thay vì 120s. |
| `src/types.ts` | `AgentRotateResponse`: `{ vpn_host, public_ip }` thay `{ vpn_host, tunnel_uuid }`. `NodeRow` thêm `public_ip: string \| null`. |
| `src/routes/zones.ts` | Thêm `POST /api/zones/:name/issue-cert` — chọn 1 node active gắn zone đó (hoặc tất cả node) và gọi `POST /admin/v1/issue-cert` xuống. |
| `src/index.ts` | Mount route mới. |

### Agent — file mới

- `internal/cert/acme.go`
  - `EnsureWildcard(ctx, zone, cfToken) error` — idempotent, gọi `acme.sh` chỉ nếu cert thiếu/expired soon.
  - `CertPath(zone) (cert, key string, ok bool)` — return paths nếu tồn tại.
  - Reload hook script được install vào `/etc/cfvpn/acme-reload.sh` chỉ chạy `systemctl reload cfvpn-xray.service`.
- `internal/netinfo/publicip.go`
  - `Detect(ctx) (string, error)` — call ipify, có fallback (icanhazip), cache TTL 5 phút trong process memory.

### Agent — file sửa

| File | Thay đổi |
|---|---|
| `internal/cloudflare/client.go` | Thêm `UpsertARecord(ctx, zoneID, name, ip)` (`proxied:false, ttl:60`). Thêm `DeleteARecordByName(ctx, zoneID, name)`. |
| `internal/templates/render.go` | `cloudflaredTemplate`: bỏ ingress `/vless` + `/trojan`, chỉ còn admin host → `localhost:6788`. `xrayTemplate`: listen `0.0.0.0:443`, `tlsSettings` đọc cert theo zone, path routing `/vless` + `/trojan` qua Xray fallback chain (1 inbound TLS + internal localhost trojan inbound). |
| `internal/commands/rotate.go` | Viết lại: bỏ create tunnel, bỏ write tunnel cred. Thêm: detect IP → ensure cert → upsert A record → render Xray config với cert path đúng → reload Xray → delete old A. |
| `internal/agent/handlers.go` | `RotateDomainHandler`: payload mới `{new_host, new_zone_id, old_host, old_zone_id}`, response `{vpn_host, public_ip}`. Thêm `IssueCertHandler` cho `POST /admin/v1/issue-cert`. |
| `internal/systemd/units.go` | `cfvpn-xray.service`: chạy as root (đơn giản cho 1-5 user, bind 443 không cần cap). Thêm `ExecReload=/bin/kill -HUP $MAINPID`. |
| `cmd/cfvpnctl/main.go` + `internal/cli/dispatch.go` | Bổ sung sub-command `install --upgrade` (xem dưới). |

### CLI (`cfvpnctl`)

- `install`: bootstrap mới — install acme.sh, issue wildcard cho primary zone, render Xray TLS, render cloudflared minimal, `ufw allow 443/tcp`.
- `install --upgrade`: nâng cấp VPS đang chạy tunnel mode lên direct mode (giữ UUID/password user, không invalidate sub link).
- `rotate-domain` (CLI manual): viết lại theo direct mode, deprecate `--cleanup`.

### Frontend (`panel/web/`)

| File | Thay đổi |
|---|---|
| `src/lib/api.ts` | `RotateNodeResponse` thêm `publicIp?: string`. `parseNode` thêm `publicIp` từ `public_ip`. |
| `src/components/nodes/NodeCard.tsx` | Hiển thị badge "Direct" + IP nếu có. |
| `src/pages/NodesPage.tsx` | Confirm dialog rotate: "Đổi domain (direct, ~5s)". |
| `src/pages/ZonesPage.tsx` (hoặc Settings tab cho zones) | Nút "Issue wildcard cert" cho mỗi zone, gọi `POST /api/zones/:name/issue-cert`. |

## Migration cho VPS đang chạy

Existing VPS đang ở tunnel mode → upgrade path qua lệnh CLI mới:

```bash
sudo cfvpnctl install --upgrade
```

Lệnh này thực hiện (atomic flow):

1. Pre-flight check: verify env có `CF_API_TOKEN`, `CF_ACCOUNT_ID`, `DOMAIN`, `TUNNEL_UUID`. Verify scope token đủ. Verify zone của domain hiện tại tồn tại trên CF.
2. Backup: copy `/etc/cfvpn/` sang `/etc/cfvpn.backup-<timestamp>/`.
3. Cài acme.sh (script 1-liner từ get.acme.sh, hoặc bundled binary).
4. Issue wildcard cert cho zone hiện tại (zone của `DOMAIN` trong env).
5. Detect public IP, tạo subdomain mới direct-mode (vd `7f3a.example.com`), upsert A record DNS-only.
6. Render lại `xray/config.json` ở TLS-mode 443, **giữ nguyên UUID + password** của tất cả user → sub link không invalidate.
7. Render lại `cloudflared/config.yml` chỉ còn admin ingress.
8. `ufw allow 443/tcp` (idempotent — kiểm tra trước khi add rule).
9. Restart `cfvpn-xray.service`, reload `cfvpn-cloudflared.service`.
10. Xoá CNAME proxied cũ trên CF DNS.
11. Update env: `DOMAIN=<new direct host>`, thêm `MODE=direct`, lưu `PUBLIC_IP=<detected IP>`.
12. Self-test: `curl https://<new_host>/vless` từ chính VPS, expect HTTP 400/426 (TLS handshake OK, WS upgrade thiếu auth).
13. Print: hướng dẫn admin sync host về panel (xem Migration Runbook bên dưới).

Sau upgrade, app pull sub link → host trong sub đã là direct domain → app reconnect → ping thấp. **Không cần user bấm rotate thêm**, miễn là panel đã sync `nodes.vpn_host` mới.

### Rollback

Nếu bất kỳ bước 4-12 fail, lệnh tự động:

1. Khôi phục `/etc/cfvpn/` từ backup `/etc/cfvpn.backup-<timestamp>/`.
2. Xoá A record direct vừa tạo (nếu đã tạo).
3. Khôi phục CNAME proxied cũ (nếu đã xoá).
4. Restart cfvpn-xray và cfvpn-cloudflared với config cũ.
5. Exit non-zero với log lỗi rõ ràng.

Sau rollback, VPS quay về tunnel mode hoạt động bình thường — admin có thể fix nguyên nhân lỗi rồi chạy lại `--upgrade`.

### Sync host về panel sau upgrade

Sau khi VPS đã ở direct mode, panel vẫn lưu `nodes.vpn_host = <old proxied domain>`. Cần sync. Có 2 cách:

**Cách A — Auto sync khi click Status (panel sửa nhẹ):**

`routes/nodes.ts` `nodeStatus` được mở rộng: nếu `agent_response.vpn_host !== row.vpn_host`, tự `UPDATE nodes SET vpn_host=?` và log event `node.host_synced`. Admin chỉ cần click nút "Status" trên Node card sau upgrade, panel tự cập nhật.

**Cách B — Manual edit qua panel:**

Admin vào Nodes tab → edit node → set `vpn_host` thủ công sang domain direct mới. Backup nếu Cách A không hoạt động.

Spec này chọn **Cách A** — minor change, không cần thêm endpoint.

## Migration Runbook (Operational Guide)

Hướng dẫn step-by-step cho admin migrate từ tunnel mode → direct mode trên VPS đang chạy.

### Trước khi bắt đầu

**Yêu cầu:**

- SSH access vào VPS với quyền root/sudo.
- Panel hoạt động bình thường, biết `node.id` của VPS này trong panel.
- Mobile clients đã import `https://<panel>/sub/<token>` qua Shadowrocket/v2rayNG (không phải import URI thẳng).
- Có 5-10 phút downtime chấp nhận được (data plane sẽ ngắt ngắn khi restart Xray).

**Kiểm tra version:**

```bash
sudo cfvpnctl version
```

Nếu binary cũ chưa có flag `--upgrade`, build lại trên dev machine và copy:

```bash
# trên dev machine
cd /opt/cf-vpn
make build

# copy lên VPS
scp bin/cfvpnctl root@<vps>:/usr/local/bin/cfvpnctl.new
ssh root@<vps> 'install -m 0755 /usr/local/bin/cfvpnctl.new /usr/local/bin/cfvpnctl && rm /usr/local/bin/cfvpnctl.new'
```

### Step 1 — Add zone vào panel (nếu chưa có)

Vào panel → Settings/Zones tab → verify zone của `DOMAIN` hiện tại có trong DB và `enabled=1`. Nếu chưa, thêm:

- Name: ví dụ `example.com`
- CF Zone ID: lấy từ Cloudflare dashboard → Zone → Overview → Zone ID
- Enabled: ✓

### Step 2 — Pre-flight check trên VPS

```bash
sudo cfvpnctl install --upgrade --check
```

Lệnh này dry-run: validate env, validate CF token scope, resolve zone, detect IP. KHÔNG đụng vào file/service. In ra plan execution và exit. Nếu thấy lỗi (ví dụ thiếu scope, hoặc DOMAIN không thuộc zone nào CF), fix trước khi chạy thật.

### Step 3 — Run upgrade

```bash
sudo cfvpnctl install --upgrade 2>&1 | tee /tmp/cfvpn-upgrade.log
```

Quan sát log. Lệnh sẽ in:

```
[1/12] backing up /etc/cfvpn/ → /etc/cfvpn.backup-20260426-143022/
[2/12] installing acme.sh ...
[3/12] issuing wildcard cert *.example.com (DNS-01, ~30-60s) ... done
[4/12] detected public IP: 203.0.113.42
[5/12] creating A record 7f3a.example.com → 203.0.113.42 (proxied:false)
[6/12] rendering xray/config.json (TLS, port 443) ...
[7/12] rendering cloudflared/config.yml (admin only) ...
[8/12] ufw: opening 443/tcp ...
[9/12] restarting cfvpn-xray.service ...
[10/12] reloading cfvpn-cloudflared.service ...
[11/12] deleting old CNAME proxied.example.com ...
[12/12] self-test https://7f3a.example.com/vless → 400 OK

UPGRADE COMPLETE.
  old domain (deleted): proxied.example.com
  new domain (direct):  7f3a.example.com
  public IP:            203.0.113.42

Next: open panel → click "Status" on this node to sync vpn_host into DB.
```

### Step 4 — Sync panel

Panel → Nodes tab → tìm node vừa upgrade → click "Status".

Panel sẽ:
- Gọi agent qua admin tunnel (vẫn hoạt động sau upgrade).
- Phát hiện `vpn_host` mới khác DB.
- Tự update `nodes.vpn_host = "7f3a.example.com"`, log event `node.host_synced`.
- Hiển thị badge "Direct" + IP `203.0.113.42` trên Node card.

### Step 5 — Verify từ client

Trên 1 thiết bị mobile:

1. Mở Shadowrocket/v2rayNG, vào subscription, bấm "Update" thủ công (thay vì chờ 24h).
2. Verify config mới hiện hostname là `7f3a.example.com:443`.
3. Connect → ping VPS → expect <100ms ở các vùng không bị CF edge xa.

### Step 6 — Optional: rotate ngay để verify rotate flow

Panel → Nodes → click "Rotate" → confirm. Expect ~5s, nodes.vpn_host đổi sang subdomain khác. Apps lần pull tiếp theo sẽ pick up.

### Trouble-shooting

**Pre-flight fail "CF token thiếu scope Zone:DNS:Edit":**

Recreate token trên Cloudflare → My Profile → API Tokens. Scope cần: `Zone:DNS:Edit` (cho zone target) + `Account:Cloudflare Tunnel:Edit` (cho admin tunnel hiện tại).

**Step 3 fail tại "issuing wildcard cert":**

- Check `journalctl -u cfvpn-agent --since "5 min ago"` (nếu agent đã chạy).
- Hoặc chạy thủ công `sudo /root/.acme.sh/acme.sh --issue --dns dns_cf -d example.com -d '*.example.com' --debug` để xem lỗi DNS-01.
- Common: token thiếu scope, hoặc zone chưa active trên CF.

**Step 9 fail "restart cfvpn-xray":**

Auto-rollback đã trigger. VPS quay về tunnel mode. Check `journalctl -u cfvpn-xray` để xem lỗi config (thường là cert path sai hoặc port 443 đang bị process khác chiếm).

**Step 12 self-test fail:**

- Verify ufw rule: `sudo ufw status | grep 443`.
- Verify Xray listening: `sudo ss -tlnp | grep 443`.
- Verify DNS propagate: `dig 7f3a.example.com @1.1.1.1` (TTL 60, nên propagate <2 phút).

**Sau upgrade, panel "Status" trả lỗi "agent_unreachable":**

Admin tunnel có thể đang reload. Đợi 30s, retry. Nếu vẫn lỗi: `sudo systemctl status cfvpn-cloudflared` + `journalctl -u cfvpn-cloudflared`.

**App vẫn ping cao sau migration:**

- Verify DNS resolve về IP VPS thật, không qua CF: `dig <new_domain>` từ máy có VPN/proxy giống user. Nếu trả IP CF range (104.x, 172.67.x), tức A record vẫn proxied. Vào CF dashboard → DNS → tìm record → tắt orange cloud (gray).
- Verify A record TTL ≤ 300 và đã propagate.

### Backup & cleanup sau khi ổn định

Sau 1-2 tuần direct mode hoạt động ổn:

```bash
sudo rm -rf /etc/cfvpn.backup-*
```

Backup chiếm vài MB, có thể giữ lâu hơn nếu thích.

### Áp dụng cho VPS mới (fresh install)

Lệnh `cfvpnctl install` (không có `--upgrade`) đã được rewrite theo direct mode, áp dụng cho VPS mới:

```bash
sudo cfvpnctl install
```

Flow tương tự `--upgrade` nhưng:

- Skip backup (chưa có gì để back).
- Skip xoá CNAME cũ.
- Tạo cả admin tunnel mới (`cfvpn-admin-<random>`) và domain direct.
- Print sub link cho `user1` mặc định khi xong.

## Security Considerations

- **VPS IP lộ:** trade-off chấp nhận để có ping thấp. Mất CF anti-DDoS — chấp nhận vì chỉ 1-5 user cá nhân.
- **Port 443 inbound:** cần `ufw allow 443/tcp`. README hiện tại nói "default deny incoming, only SSH". Section Security trong README sẽ được cập nhật.
- **Cert privkey:** chmod 600, owner root, chỉ Xray (chạy as root) đọc.
- **CF API token scope:** đã có `Zone:DNS:Edit` (cho A record). Không cần scope mới.
- **Admin tunnel:** giữ nguyên CF Access policy `/api/*` require, `/sub/*` bypass.
- **acme.sh on VPS:** chỉ root chạy. Cron job chạy as root.

## Testing Strategy

### Unit tests

- `internal/cert/acme.go`: mock exec, test idempotency (skip nếu cert valid >30 ngày).
- `internal/netinfo/publicip.go`: mock HTTP, test cache TTL.
- `internal/cloudflare/client.go`: thêm test cho `UpsertARecord` và `DeleteARecordByName` (extend test suite hiện có).
- `internal/commands/rotate.go`: rewrite tests cho direct flow, mock CF + cert + xray reload + IP detect.
- Worker `routes/nodes.ts` + `routes/zones.ts`: thêm test cho payload mới và endpoint issue-cert.

### Integration tests

- Bootstrap fresh VPS với `cfvpnctl install` mới → check `curl https://<domain>/vless` returns 400/426 (Xray TLS handshake OK, WS upgrade thiếu auth).
- Rotate on panel → verify A record mới + xray config update + sub link đổi host.
- Multi-zone: add zone thứ 2, issue cert, rotate sang zone đó → verify Xray pick đúng cert theo SNI.

### Manual smoke

- China VPN test: trước/sau migration đo ping bằng app.
- Cert renew: chạy `acme.sh --renew --force` → verify Xray reload, không drop client đang kết nối.

## Open Questions / Future Work

- **IPv6 / AAAA record:** spec này chỉ A (IPv4). Có thể bổ sung sau nếu VPS có IPv6 và client cần.
- **CDN-style fronting nâng cao:** nếu IP VPS bị block, fallback automatic không có trong scope này — vẫn rotate manual.
- **Cert pre-issue cho zone chưa active:** spec hiện tại issue lazy (lần rotate đầu tiên vào zone mới). Có thể eager issue khi enable zone, nhưng tốn API quota acme/CF khi user bật/tắt zone nhiều.
