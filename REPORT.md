# REPORT — Fleet refresh 12/9/2026

Branch `feat/fleet-refresh-2026-09` (PR #9), một commit cho mỗi việc. Spec:
`docs/superpowers/specs/2026-09-12-fleet-refresh-design.md`, plan:
`docs/superpowers/plans/2026-09-12-fleet-refresh.md`. Giờ trong báo cáo là UTC
(giờ VN = UTC+7). Phần đầu là 6 việc gốc; phần "Bổ sung" là các yêu cầu anh
đưa thêm trong cùng phiên (deploy việc 6, bật lại HY2, group HY2-BACKUP).

## Backup

`/root/cfvpn-backups/20260911T195306Z/` trên VNM-01 (mode 700):

- `nodes/<NODE>/etc-cfvpn.tgz` — toàn bộ `/etc/cfvpn` của 9 node + `system.txt`.
- `d1/{nodes,users,user_nodes}.json` — dump D1 trước khi sửa.
- `sub/` — subscription cũ (base64, decoded, clash, shadowrocket).
- `after/`, `after2/`, `after3/` — subscription sau việc 4, sau khi bật lại HY2,
  và **bản cuối cùng** (`after3/`: base64, decoded, `RWL8899.conf`, clash,
  `naive.txt`).

Khôi phục một node: giải nén tgz vào `/etc/cfvpn`, `systemctl restart
cfvpn-xray cfvpn-cloudflared`, rồi `bash scripts/d1-set-node.sh <NODE> reality`
(và `hy2-on`/`xhttp-on` nếu cần) để D1 khớp lại.

## Trạng thái cuối cùng theo node

| Node | IP | Mode | Reality dest / SNI | HY2 | XHTTP | Khác |
|---|---|---|---|---|---|---|
| HAN-01 | 103.199.17.69 | direct | vtv.vn | có (:44283) | — | |
| HKG-01 | 96.9.228.81 | direct | www.cathaypacific.com | có (:31300) | — | DERP relay :8443 + STUN :3478 |
| JPY-01 | 45.143.131.36 | cloudflare | — | có (:56028) | có | cloudflared `protocol: http2`; NaiveProxy :443 |
| JPY-02 | 185.200.65.215 | direct | www.amazon.co.jp | **không** | — | Reality chính |
| OR-001 | 51.81.245.144 (NAT) | cloudflare | — | có (:5331) | có | NaiveProxy :443 trong VM, ngoài là :5373 |
| SIN-01 | 96.9.231.74 | direct | www.singaporeair.com | **không** | — | Reality chính |
| USA-01 | 64.44.157.135 | direct | www.tesla.com | có (:53572) | — | |
| VNM-01 | 14.248.99.79 | cloudflare | — | có (không có trong D1) | có | máy dev; thêm cron probe + env probe |
| VNM-02 | 103.161.171.20 | direct | www.samsung.com | có (:41718) | — | |

Mọi node direct: `security=reality`, `flow=xtls-rprx-vision`, port 443,
Xray-core 26.3.27 (stable mới nhất; 26.9.9 chỉ là pre-release nên không nâng).
Không node nào từng chạy `security=tls` với cert riêng.

## Việc 1 — hai node hỏng

**JPY-01.** Xray và ingress cloudflared giống hệt OR-001. Nguyên nhân là
cloudflared rớt QUIC liên tục tới edge (`failed to dial to edge with quic:
timeout: no recent network activity`, reconnect mỗi vài phút; OR-001 không
rớt). Sửa: khoá env mới `CLOUDFLARED_PROTOCOL=http2`, template cloudflared
render thêm dòng `protocol:`; áp dụng bằng `cfvpnctl upgrade --mode
cloudflare` lúc 19:55Z. 4 kết nối `protocol=http2` đăng ký ngay và từ đó tới
cuối phiên không có lần "Connection terminated" nào.

**HKG-01.** Server không có lỗi: pubkey/shortId/SNI trên node khớp D1 và khớp
subscription; log xray cho thấy client China Mobile (223.104.x.x) kết nối
thành công các ngày 9–11/9. Timeout ở Bắc Kinh được quy cho việc client phân
giải domain `media-f6af97a5.dongnat247.com` bị chặn/nhiễu, không phải TCP 443.
Xử lý ở việc 3 (dest mới) và việc 4 (URI dùng IP). Không sửa gì trên server.

## Việc 2 — Hysteria2 (đã sửa theo yêu cầu bổ sung)

Code: khoá env `HY2_ENABLED` + lệnh `cfvpnctl hy2 enable|disable`. Khi tắt:
unit `cfvpn-hysteria` bị stop + disable + xoá, cert-renew bỏ qua cert HY2,
agent không reload hysteria và báo trống các trường HY2, subscription phía
node không sinh dòng HY2. Config, cert và các khoá `HY2_*` giữ nguyên nên
`hy2 enable` là đảo ngược hoàn toàn; `cfvpnctl upgrade` không bật lại được vì
unit set suy ra từ `HY2_ENABLED`.

Lần 1 (19:59Z–20:03Z) tắt trên 6 node. Lần 2 theo yêu cầu mới (20:38Z–20:40Z)
**bật lại trên HAN-01, USA-01, VNM-02, OR-001** bằng `cfvpnctl hy2 enable`,
D1 khôi phục 3 cột hy2 (`d1-set-node.sh <NODE> hy2-on`), ufw mở lại port UDP,
dòng HY2 trở lại subscription. Khi bật lại phát hiện và sửa một lỗi: reconcile
chỉ `restart` unit mới tạo lại nên hysteria sẽ không tự lên sau reboot; nay
unit mới được `enable --now`. **JPY-02 và SIN-01 không có HY2.**

## Việc 3 — Reality mới trên 6 node direct

Code: `cfvpnctl rotate-reality --dest <host>:443 [--sni <host>]` sinh x25519 +
shortId mới, render lại xray, restart (tự khôi phục config cũ nếu restart
lỗi), ghi `REALITY_*` vào env. Install/upgrade nay đọc `REALITY_DEST/SNI` từ
env khi sinh key nên dest theo node không bị reset về mặc định (trước đây dest
là hằng `www.apple.com` trong code).

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
node) và các cột `reality_*` trong D1. Verify mỗi node: xray active trên
:443, `openssl s_client` vào 127.0.0.1:443 với SNI = dest trả về đúng cert
thật của dest, probe 204, drift check khớp.

## Việc 4 — client

- URI Reality dùng **IP** (`vless://…@96.9.231.74:443?…&sni=www.singaporeair.com`)
  và URI HY2 cũng dùng **IP** với `sni=<hy2 hostname>` (yêu cầu bổ sung), ở cả
  builder Go và Worker (golden test hai phía). Đường cloudflare (HTTPUpgrade,
  XHTTP) giữ hostname vì đi qua Cloudflare edge.
- Subscription base64 giữ nguyên format, cuối cùng có **18 đường**: 6 Reality,
  3 HTTPUpgrade, 3 XHTTP, 6 HY2. File subscription phía node cũng đã sinh lại.
- `https://cp.rwl265.com/sub/<token>?format=shadowrocket` trả file
  `RWL8899.conf`:

  ```
  AUTO = url-test, kulinh@JPY-02-Reality, kulinh@SIN-01-Reality, kulinh@JPY-01-HY2, kulinh@HKG-01-HY2, kulinh@OR-001-HTTPUpgrade, url = http://cp.cloudflare.com/generate_204, interval = 600, tolerance = 500, timeout = 8
  HY2-BACKUP = select, kulinh@HAN-01-HY2, kulinh@HKG-01-HY2, kulinh@JPY-01-HY2, kulinh@OR-001-HY2, kulinh@USA-01-HY2, kulinh@VNM-02-HY2
  PROXY = select, AUTO, HY2-BACKUP, <tất cả 18 đường>
  FINAL,AUTO
  ```

  Cách dùng: import subscription base64 như cũ (để có node), rồi import file
  `.conf` này. Group tham chiếu node theo đúng tên trong subscription. Bản cuối
  ở `after3/RWL8899.conf`. Em chưa kiểm tra được trên điện thoại.
- Worker version cuối: `37f83411-344f-47e1-9e64-c048b158150a`.

## Việc 5 — health check từ VNM-01

`scripts/fleet-probe.py`: xray client (một SOCKS inbound cho mỗi đường VLESS,
hiểu cả reality/httpupgrade/xhttp) + hysteria client cho mỗi đường HY2, curl
`generate_204` qua từng đường, ghi `/var/log/cfvpn-fleet-probe.log`, đếm fail
liên tiếp trong `/var/lib/cfvpn/fleet-probe.state`, fail 2 lần liên tiếp thì
gửi Telegram một lần (và một lần khi hồi phục). 8 unit test.

Đã cài trên VNM-01: `/etc/cron.d/cfvpn-fleet-probe` (mỗi 10 phút),
`/etc/cfvpn/fleet-probe.env` (mode 600, đã điền `SUB_URL`). Cron đã tự chạy từ
20:20Z; tới 20:47Z có 105 dòng log, không có FAIL nào từ cron. Hai dòng FAIL
duy nhất trong log là `OR-001-XHTTP-stream-one/stream-up` từ lần em thử tay
XHTTP (ghi vào cùng file log), không phải sự cố.

**Cần anh:** dán `TELEGRAM_BOT_TOKEN=<token>` vào `/etc/cfvpn/fleet-probe.env`.
Token chỉ tồn tại trong secret của Worker nên em không đọc được. Chat id
`-1003806233980` đã điền sẵn.

## Việc 6 — đã deploy theo yêu cầu bổ sung

**NaiveProxy** (`docs/prep/naiveproxy-jpy-01-or-001.md`): Caddy + forwardproxy
(fork naive) + plugin DNS Cloudflare, cert Let's Encrypt DNS-01, unit
`naive-caddy`, decoy page.

| Node | Hostname | Port client | Kết quả từ VNM-01 |
|---|---|---|---|
| JPY-01 | naive-62abc468.duylinh.net | 443 | 204 qua proxy; sai mật khẩu bị từ chối; GET thường ra decoy 200 |
| OR-001 | naive-74df6c7b.duylinh.net | **5373** (anh forward 5373→443 trên NAT TierHive) | 204 qua proxy, egress IP 51.81.245.144 |

Client line (có mật khẩu) ở `after3/naive.txt`, không đưa vào subscription hay
AUTO. Bài học: site key trong Caddyfile phải là `:443, <host>:443`, nếu chỉ
`<host>:443` thì CONNECT không khớp host matcher và Caddy trả 200 rỗng, client
tưởng tunnel mở nhưng không có dữ liệu.

**XHTTP** (`docs/prep/xhttp-cloudflare-nodes.md`): thử tay trên OR-001 trước:
`packet-up` chạy được qua cloudflared (611 ms), `auto` được, `stream-up` và
`stream-one` fail như phát hiện tháng 5. Sau đó đưa vào code đúng chuẩn: khoá
`XHTTP_ENABLED`, `cfvpnctl xhttp enable|disable`, inbound thứ hai
`127.0.0.1:10002` path `/api/v2/stream`, ingress cloudflared tương ứng, dòng
`<node>-XHTTP` trong subscription (hai builder), cột D1 `xhttp_enabled`
(migration 0020, đã apply prod). Bật trên OR-001, JPY-01, VNM-01; HTTPUpgrade
giữ nguyên song song. XHTTP có trong group PROXY, không có trong AUTO.

**DERP** (`docs/prep/tailscale-derp.md`): `derper` chạy trên HKG-01, hostname
`derp-f2a4f360.duylinh.net`, DERP TCP 8443 (443 là Reality), STUN UDP 3478,
cert lego DNS-01, `--verify-clients`. Verify: `/derp/probe` 200, STUN trả lời.
**ACL chưa apply** vì phải làm trong admin console; đoạn `derpMap` và thứ tự
an toàn (thêm region trước, `OmitDefaultRegions: true` sau khi verify) nằm
trong tài liệu. Cert DERP hết hạn ~11/12/2026, dòng cron renew ghi sẵn trong
tài liệu, chưa cài.

## Kết quả probe cuối (ms, từ VNM-01, tất cả trả 204, 20:47Z)

| Đường | Trước (19:38Z) | Cuối |
|---|---|---|
| HAN-01 Reality / HY2 | 173 / 98 | 91 / 76 |
| HKG-01 Reality / HY2 | 204 / 102 | 173 / 124 |
| JPY-01 HTTPUpgrade / XHTTP / HY2 | 344 / — / 265 | 361 / 172 / 272 |
| JPY-02 Reality (HY2 gỡ) | 595 / 246 | 359 |
| OR-001 HTTPUpgrade / XHTTP / HY2 | 788 / — / 405 | 814 / 348 / 397 |
| SIN-01 Reality (HY2 gỡ) | 214 / 143 | 174 |
| USA-01 Reality / HY2 | 695 / 430 | 668 / 433 |
| VNM-01 HTTPUpgrade / XHTTP | 223 / — | 218 / 166 |
| VNM-02 Reality / HY2 | 419 / 198 | 355 / 293 |
| NaiveProxy JPY-01 / OR-001 | — | 204 / 204 |

Drift check cuối: 9/9 khớp. Latency tuyệt đối phụ thuộc đường VNPT ↔ nhà
mạng node; điểm cần đọc là mọi đường đều 204 sau khi đổi key, dest, IP và thêm
đường mới. Test thật từ Trung Quốc vẫn là bước kiểm chứng cuối.

## Việc chưa làm hoặc cần anh quyết

1. **Telegram token** cho fleet-probe (việc 5).
2. **Import `RWL8899.conf` trên Shadowrocket**, xác nhận AUTO (5 đường) và
   HY2-BACKUP (6 đường) nhận đủ node; nếu cần cú pháp khác em sửa builder.
3. **DERP ACL**: dán `derpMap` trong `docs/prep/tailscale-derp.md` vào admin
   console theo 2 bước; sau đó cài cron renew cert trên HKG-01.
4. **Tailscale key expiry** vẫn bật trên jpy-01, jpy-02, vnm-01 (hết hạn tối
   13/9 giờ VN) và các node còn lại.
5. **Merge PR #9** khi anh review xong.
6. Trên VNM-01 ngoài repo chỉ thay đổi: cron probe, env probe, và XHTTP bật
   thêm inbound 10002 theo yêu cầu deploy việc 6.
