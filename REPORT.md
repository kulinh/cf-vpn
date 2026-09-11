# REPORT — Fleet refresh 12/9/2026

Branch `feat/fleet-refresh-2026-09`, một commit cho mỗi việc. Spec:
`docs/superpowers/specs/2026-09-12-fleet-refresh-design.md`, plan:
`docs/superpowers/plans/2026-09-12-fleet-refresh.md`. Giờ trong báo cáo là UTC
(giờ VN = UTC+7).

## Backup

`/root/cfvpn-backups/20260911T195306Z/` trên VNM-01 (mode 700):

- `nodes/<NODE>/etc-cfvpn.tgz` — toàn bộ `/etc/cfvpn` của 9 node (env, xray,
  hysteria, cloudflared, cert) + `system.txt` (ufw, unit systemd).
- `d1/{nodes,users,user_nodes}.json` — dump D1 trước khi sửa.
- `sub/{base64.txt,decoded.txt,clash.yaml,shadowrocket.txt}` — subscription cũ.
- `after/{base64.txt,decoded.txt,RWL8899.conf,clash.yaml}` — subscription mới.

Khôi phục một node: giải nén tgz vào `/etc/cfvpn`, `systemctl restart
cfvpn-xray cfvpn-cloudflared`, rồi `bash scripts/d1-set-node.sh <NODE> reality`
để D1 khớp lại.

## Tổng kết theo node

| Node | IP | Mode | Reality dest / SNI mới | HY2 | Khác |
|---|---|---|---|---|---|
| HAN-01 | 103.199.17.69 | direct | vtv.vn | **tắt** | |
| HKG-01 | 96.9.228.81 | direct | www.cathaypacific.com | giữ (:31300) | |
| JPY-01 | 45.143.131.36 | cloudflare | — | giữ (:56028) | cloudflared → `protocol: http2` |
| JPY-02 | 185.200.65.215 | direct | www.amazon.co.jp | **tắt** | |
| OR-001 | 51.81.245.144 | cloudflare | — | **tắt** | |
| SIN-01 | 96.9.231.74 | direct | www.singaporeair.com | **tắt** | |
| USA-01 | 64.44.157.135 | direct | www.tesla.com | **tắt** | |
| VNM-01 | 14.248.99.79 | cloudflare | — | không đổi | chỉ thêm cron + env của probe |
| VNM-02 | 103.161.171.20 | direct | www.samsung.com | **tắt** | |

Mọi node direct: `security=reality`, `flow=xtls-rprx-vision`, port 443,
Xray-core 26.3.27 (đang là bản stable mới nhất; 26.9.9 chỉ là pre-release nên
không nâng). Không node nào từng chạy `security=tls` với cert riêng.

## Việc 1 — hai node hỏng

**JPY-01.** Xray và ingress cloudflared giống hệt OR-001. Nguyên nhân là
cloudflared rớt QUIC liên tục tới edge (`failed to dial to edge with quic:
timeout: no recent network activity`, reconnect mỗi vài phút; OR-001 không
rớt). Sửa: khoá env mới `CLOUDFLARED_PROTOCOL=http2`, template cloudflared
render thêm dòng `protocol:`; áp dụng bằng `cfvpnctl upgrade --mode
cloudflare` lúc 19:55Z. 4 kết nối `protocol=http2` đăng ký ngay, và **từ đó
tới cuối phiên (hơn 20 phút) không có lần "Connection terminated" nào**.
Probe từ VNM-01 sau sửa: HTTPUpgrade 355 ms, HY2 227 ms.

**HKG-01.** Server không có lỗi: pubkey/shortId/SNI trên node khớp D1 và khớp
subscription; log xray cho thấy client China Mobile (223.104.x.x) kết nối
thành công các ngày 9–11/9. Timeout anh gặp ở Bắc Kinh được quy cho việc
client phân giải domain `media-f6af97a5.dongnat247.com` bị chặn/nhiễu, không
phải TCP 443. Xử lý nằm ở việc 3 (dest mới) và việc 4 (URI dùng IP thay
domain). Không sửa gì trên server ở việc này.

## Việc 2 — Hysteria2 chỉ còn trên JPY-01 và HKG-01

Code: khoá env `HY2_ENABLED` + lệnh `cfvpnctl hy2 enable|disable`. Khi tắt:
`reconcile-units` stop + disable + xoá unit `cfvpn-hysteria`, cert-renew bỏ
qua cert HY2, agent không reload hysteria khi sync user và báo trống các trường
HY2, subscription phía node không sinh dòng HY2. Config, cert và các khoá
`HY2_*` giữ nguyên nên `hy2 enable` là đảo ngược hoàn toàn. `cfvpnctl upgrade`
sau này không bật lại được vì unit set được suy ra từ `HY2_ENABLED`.

Rollout 19:59Z–20:03Z, từng node: OR-001 → HAN-01 → USA-01 → VNM-02 → JPY-02
→ SIN-01. Mỗi node: cài binary mới, `cfvpnctl hy2 disable`, kiểm tra unit
inactive/không còn, không còn listener UDP của hysteria, ufw đã xoá rule UDP,
D1 `hy2_host/hy2_port/hy2_obfs_pw = NULL` (`scripts/d1-set-node.sh <NODE>
hy2-off`), subscription không còn dòng HY2 của node, probe đường VLESS trả 204.
Drift check sau cùng: 9/9 khớp.

## Việc 3 — Reality mới trên 6 node direct

Code: lệnh `cfvpnctl rotate-reality --dest <host>:443 [--sni <host>]` sinh
x25519 + shortId mới, render lại xray, restart (tự khôi phục config cũ nếu
restart lỗi), ghi `REALITY_*` vào `cfvpn.env`. Install/upgrade nay đọc
`REALITY_DEST/REALITY_SNI` từ env khi sinh key nên dest theo node không bị
reset về mặc định. Trước đây dest là hằng `www.apple.com` trong code.

Rollout ~20:04Z–20:07Z theo thứ tự HAN-01, VNM-02, USA-01, HKG-01, JPY-02,
SIN-01. Key cũ không tái dùng.

| Node | Dest / SNI | shortId mới | Public key (12 ký tự đầu) |
|---|---|---|---|
| HAN-01 | vtv.vn:443 | 49a5413ddff74d56 | urWW92G1NbBJ… |
| VNM-02 | www.samsung.com:443 | 46ccd9d68a6134d1 | MTkAgPy1lRLx… |
| USA-01 | www.tesla.com:443 | 6ea35c34bb0dfb56 | 0P5Pt-np2QGl… |
| HKG-01 | www.cathaypacific.com:443 | a55ad8dc1935ae6b | VCj0Y2Q1Hh0-… |
| JPY-02 | www.amazon.co.jp:443 | 47f88688c4d9fdce | TEL6BN7YVAwz… |
| SIN-01 | www.singaporeair.com:443 | 4a2739d7c27cf56d | r2pctdBDeA5i… |

Key đầy đủ nằm ở `/etc/cfvpn/cfvpn.env` trên từng node (private key không rời
node) và cột `reality_pubkey/reality_sid/reality_sni/reality_dest` trong D1.
Verify mỗi node: xray active trên :443, `openssl s_client` vào 127.0.0.1:443
với SNI = dest trả về đúng cert thật của dest (Reality forward handshake
thành công), probe 204, drift check khớp.

## Việc 4 — client

- URI Reality dùng **IP** (`vless://…@96.9.231.74:443?…&sni=www.singaporeair.com`),
  ở cả builder Go và Worker (golden test cập nhật hai phía). Đường cloudflare
  giữ hostname. Worker version `742f5964-5318-4d85-a36b-d9333da860ee`, deploy
  ~20:10Z.
- Subscription base64 giữ nguyên format, còn 11 đường: 6 Reality, 3
  HTTPUpgrade, 2 HY2 (HKG-01, JPY-01). File subscription phía node
  (`/var/lib/cfvpn/subscriptions/kulinh.txt`) cũng đã sinh lại với IP.
- Mới: `https://cp.rwl265.com/sub/<token>?format=shadowrocket` trả file
  `RWL8899.conf` gồm `[General]`, `[Proxy Group]`, `[Rule]`:

  ```
  AUTO = url-test, kulinh@JPY-02-Reality, kulinh@SIN-01-Reality, kulinh@JPY-01-HY2, kulinh@HKG-01-HY2, kulinh@OR-001-HTTPUpgrade, url = http://cp.cloudflare.com/generate_204, interval = 600, tolerance = 500, timeout = 8
  PROXY = select, AUTO, <tất cả 11 đường>
  FINAL,AUTO
  ```

  Cách dùng trên Shadowrocket: import subscription base64 như cũ (để có node),
  rồi import file `.conf` này (Config → thêm từ URL). Group tham chiếu node
  theo đúng tên trong subscription. Bản đã sinh nằm ở
  `/root/cfvpn-backups/20260911T195306Z/after/RWL8899.conf`. Em chưa kiểm tra
  được trên điện thoại; anh import thử và báo nếu Shadowrocket không nhận group.

## Việc 5 — health check từ VNM-01

`scripts/fleet-probe.py`: dựng xray client (một SOCKS inbound cho mỗi đường
VLESS) + hysteria client cho mỗi đường HY2, curl `generate_204` qua từng
đường, ghi `<ts> <đường> OK|FAIL <ms>` vào `/var/log/cfvpn-fleet-probe.log`,
đếm fail liên tiếp trong `/var/lib/cfvpn/fleet-probe.state`, fail 2 lần liên
tiếp thì gửi Telegram một lần (và một lần khi hồi phục). 8 unit test
(`python3 -m pytest scripts/tests`).

Đã cài trên VNM-01: `/etc/cron.d/cfvpn-fleet-probe` (mỗi 10 phút),
`/etc/cfvpn/fleet-probe.env` (mode 600, đã điền `SUB_URL`). Chạy thật lần đầu
20:15Z: 11/11 OK.

**Cần anh:** dán `TELEGRAM_BOT_TOKEN=<token>` vào `/etc/cfvpn/fleet-probe.env`.
Token chỉ tồn tại trong secret của Worker nên em không đọc được. Chat id
`-1003806233980` đã điền sẵn. Trước khi có token, cảnh báo chỉ in ra
`/var/log/cfvpn-fleet-probe.err`.

## Việc 6 — chuẩn bị, chưa deploy

- `docs/prep/naiveproxy-jpy-01-or-001.md` — Caddy + forwardproxy trên
  **JPY-01 và OR-001**, port **443** (hai node cloudflare-mode nên 443 trống),
  subdomain `naive-<rand>.duylinh.net`, cert Let's Encrypt qua DNS-01 với
  `CF_API_TOKEN`. Client line xuất riêng, không vào subscription/AUTO. Có bước
  bật, verify, rollback.
- `docs/prep/xhttp-cloudflare-nodes.md` — inbound XHTTP thứ hai (10002, path
  `/api/v2/stream`, mode `packet-up`) + ingress cloudflared cho OR-001, VNM-01,
  JPY-01; URI client; bước bật/rollback. Lưu ý quan trọng: config xray và
  cloudflared được render từ template, sync user từ panel sẽ xoá sửa tay, nên
  bật lâu dài phải đưa vào template (cùng kiểu `HY2_ENABLED`).
- `docs/prep/tailscale-derp.md` — derper trên HKG-01 port 8443 (443 đang là
  Reality) + STUN 3478, cert qua lego, unit systemd, đoạn `derpMap` với
  `OmitDefaultRegions: true`, thứ tự apply an toàn và rollback.

## Kết quả probe (ms, từ VNM-01, tất cả trả 204)

| Đường | Trước (19:38Z) | Sau cùng (20:15Z) |
|---|---|---|
| HAN-01-Reality | 173 | 84 |
| HKG-01-Reality | 204 | 175 |
| HKG-01-HY2 | 102 | 113 |
| JPY-01-HTTPUpgrade | 344 | 371 |
| JPY-01-HY2 | 265 | 244 |
| JPY-02-Reality | 595 | 390 |
| OR-001-HTTPUpgrade | 788 | 807 |
| SIN-01-Reality | 214 | 160 |
| USA-01-Reality | 695 | 655 |
| VNM-01-HTTPUpgrade | 223 | 251 |
| VNM-02-Reality | 419 | 496 |
| HAN-01/JPY-02/OR-001/SIN-01/USA-01/VNM-02 HY2 | 98/246/405/143/430/198 | đã gỡ |

Latency tuyệt đối phụ thuộc đường VNPT ↔ nhà mạng node, không so sánh chéo
được; điểm cần đọc là mọi đường đều 204 sau khi đổi key, đổi dest và đổi sang
IP. Test thật từ Trung Quốc vẫn là bước kiểm chứng cuối cùng cho HKG-01.

## Việc chưa làm hoặc cần anh quyết

1. **Telegram token** cho fleet-probe (xem việc 5).
2. **Import `RWL8899.conf` trên Shadowrocket** và xác nhận group AUTO nhận đủ 5
   node; nếu Shadowrocket cần cú pháp khác em sửa builder.
3. **NaiveProxy / XHTTP / DERP**: chỉ có tài liệu, chờ anh chọn thời điểm; XHTTP
   nên thử trên OR-001 trước và test từ Trung Quốc.
4. **Tailscale key expiry** còn bật trên jpy-01, jpy-02 và vnm-01 (hết hạn tối
   13/9 giờ VN), cùng sin-01, vnm-02, usa-01, han-01, or-001. Tắt trong admin
   console để không lặp lại sự cố 9/9.
5. **Merge branch**: em push `feat/fleet-refresh-2026-09` và mở PR, không tự
   merge vào `main`.
6. Không có gì thay đổi trên VNM-01 ngoài repo, cron probe và file env của
   probe; HY2 trên VNM-01 vẫn chạy như trước (không nằm trong D1 nên không có
   trong subscription).
