# REPORT — Fleet refresh 12/9/2026

Branch `feat/fleet-refresh-2026-09` (PR #9), một commit cho mỗi việc. Spec:
`docs/superpowers/specs/2026-09-12-fleet-refresh-design.md`, plan:
`docs/superpowers/plans/2026-09-12-fleet-refresh.md`. Giờ trong báo cáo là UTC
(giờ VN = UTC+7). Phần đầu là 6 việc gốc; phần "Bổ sung" là các yêu cầu anh
đưa thêm trong cùng phiên, theo thứ tự thời gian.

## Backup

`/root/cfvpn-backups/20260911T195306Z/` trên VNM-01 (mode 700):

- `nodes/<NODE>/etc-cfvpn.tgz` — toàn bộ `/etc/cfvpn` của 9 node + `system.txt`.
- `d1/{nodes,users,user_nodes}.json` — dump D1 trước khi sửa.
- `sub/` — subscription cũ. `after/`…`after3/` — các mốc trung gian.
- **`final/`** — bản cuối cùng: `base64.txt`, `decoded.txt`, `RWL8899.conf`, `clash.yaml`.
- `/root/cfvpn-backups/acl/<ts>.{before,after}.json` — mọi lần sửa policy Tailscale.

Khôi phục một node: giải nén tgz vào `/etc/cfvpn`, `systemctl restart
cfvpn-xray cfvpn-cloudflared`, rồi `bash scripts/d1-set-node.sh <NODE> reality`
(và `hy2-on` / `xhttp-on` / `xhttp-direct` nếu cần) để D1 khớp lại.

## Trạng thái cuối cùng theo node

| Node | IP | Mode | Reality dest / SNI | HY2 | XHTTP (qua CF) | Khác |
|---|---|---|---|---|---|---|
| HAN-01 | 103.199.17.69 | direct | vtv.vn | có (:44283) | — | |
| HKG-01 | 96.9.228.81 | direct | www.cathaypacific.com | có (:31300) | — | DERP region 900 :8443 + STUN :3478 |
| JPY-01 | 45.143.131.36 | cloudflare | — | có (:56028) | có | cloudflared `http2`; **XHTTP-Direct** qua Caddy :443 (`cdn-82169439.duylinh.net`); DERP region 901 :8443 + STUN :3478 |
| JPY-02 | 185.200.65.215 | direct | www.amazon.co.jp | **không** | — | Reality chính |
| OR-001 | 51.81.245.144 (NAT) | cloudflare | — | có (:5331) | có | |
| SIN-01 | 96.9.231.74 | direct | www.singaporeair.com | **không** | — | Reality chính |
| USA-01 | 64.44.157.135 | direct | www.tesla.com | có (:53572) | — | |
| VNM-01 | 14.248.99.79 | cloudflare | — | có (không có trong D1) | có | máy dev; cron probe + env probe |
| VNM-02 | 103.161.171.20 | direct | www.samsung.com | có (:41718) | — | |

Mọi node direct: `security=reality`, `flow=xtls-rprx-vision`, port 443,
Xray-core 26.3.27 (stable mới nhất; 26.9.9 là pre-release). NaiveProxy đã gỡ
hoàn toàn (xem Bổ sung).

## Việc 1 — hai node hỏng

**JPY-01.** cloudflared rớt QUIC liên tục tới edge. Sửa: khoá env
`CLOUDFLARED_PROTOCOL=http2`, template render dòng `protocol:`; áp dụng lúc
19:55Z. 4 kết nối http2 đăng ký ngay và không rớt lần nào tới cuối phiên.

**HKG-01.** Server không lỗi (config khớp D1, log cho thấy China Mobile kết nối
được 9–11/9). Timeout ở Bắc Kinh quy cho client phân giải domain bị nhiễu →
xử lý bằng dest mới (việc 3) và URI dùng IP (việc 4).

## Việc 2 — Hysteria2

Khoá `HY2_ENABLED` + `cfvpnctl hy2 enable|disable`; tắt = unit bị stop,
disable, xoá; cert-renew và agent bỏ qua HY2; subscription không sinh dòng HY2.
Config/cert/env giữ nguyên nên enable là đảo ngược hoàn toàn. Lần 1 tắt trên
6 node; lần 2 theo yêu cầu bổ sung bật lại HAN-01, USA-01, VNM-02, OR-001.
**JPY-02 và SIN-01 không có HY2.** Khi bật lại phát hiện reconcile chỉ restart
unit mới tạo nên hysteria không tự lên sau reboot; đã sửa (enable --now) kèm test.

## Việc 3 — Reality mới trên 6 node direct

`cfvpnctl rotate-reality --dest <host>:443` sinh x25519 + shortId mới, render
lại xray, restart (tự khôi phục nếu lỗi), ghi env; install/upgrade nay đọc
`REALITY_DEST/SNI` từ env. Rollout ~20:04Z–20:07Z, key cũ không tái dùng.

| Node | Dest / SNI | shortId mới | Public key (12 ký tự đầu) |
|---|---|---|---|
| HAN-01 | vtv.vn:443 | 49a5413ddff74d56 | urWW92G1NbBJ… |
| VNM-02 | www.samsung.com:443 | 46ccd9d68a6134d1 | MTkAgPy1lRLx… |
| USA-01 | www.tesla.com:443 | 6ea35c34bb0dfb56 | 0P5Pt-np2QGl… |
| HKG-01 | www.cathaypacific.com:443 | a55ad8dc1935ae6b | VCj0Y2Q1Hh0-… |
| JPY-02 | www.amazon.co.jp:443 | 47f88688c4d9fdce | TEL6BN7YVAwz… |
| SIN-01 | www.singaporeair.com:443 | 4a2739d7c27cf56d | r2pctdBDeA5i… |

Key đầy đủ ở `/etc/cfvpn/cfvpn.env` từng node và cột `reality_*` trong D1.
Verify: xray :443 active, `openssl s_client` với SNI = dest trả cert thật của
dest, probe 204, drift khớp.

## Việc 4 — client

- URI Reality và HY2 dùng **IP**, hostname chỉ còn ở `sni` (Go + Worker,
  golden test). Đường qua Cloudflare (HTTPUpgrade, XHTTP) giữ hostname.
  Đường XHTTP-Direct dùng **hostname** vì TLS thật.
- Subscription base64 cuối cùng **19 đường**: 6 Reality, 3 HTTPUpgrade,
  3 XHTTP, 1 XHTTP-Direct, 6 HY2.
- `?format=shadowrocket` → `RWL8899.conf`:

  ```
  AUTO = url-test, kulinh@JPY-02-Reality, kulinh@SIN-01-Reality, kulinh@JPY-01-HY2, kulinh@HKG-01-HY2, kulinh@OR-001-HTTPUpgrade, url = http://cp.cloudflare.com/generate_204, interval = 600, tolerance = 500, timeout = 8
  HY2-BACKUP = select, kulinh@HAN-01-HY2, kulinh@HKG-01-HY2, kulinh@JPY-01-HY2, kulinh@OR-001-HY2, kulinh@USA-01-HY2, kulinh@VNM-02-HY2
  PROXY = select, AUTO, HY2-BACKUP, <tất cả 19 đường, gồm kulinh@JPY-01-XHTTP-Direct>
  FINAL,AUTO
  ```

  Import subscription base64 để có node, rồi import file `.conf`. Bản cuối ở
  `final/RWL8899.conf`. Worker version cuối `cc4db87d-feda-48c9-a945-cc4467070be7`.

## Việc 5 — health check từ VNM-01

`scripts/fleet-probe.py` (hiểu reality/httpupgrade/xhttp mọi mode + HY2), cron
10 phút, log `/var/log/cfvpn-fleet-probe.log`, state
`/var/lib/cfvpn/fleet-probe.state`, Telegram sau 2 lần fail liên tiếp (và một
lần khi hồi phục). Cron tự chạy từ 20:20Z, không có FAIL nào từ cron.

**Telegram đã hoạt động (21:5xZ):** token `@rwl_vpn_bot` nằm trong
`/etc/cfvpn/fleet-probe.env` (mode 600, gitignore), chat id `-1003806233980`
= group "RWL Hub". Ban đầu Telegram trả "chat not found" vì bot chưa ở trong
group — khác bot của Worker; anh thêm `@rwl_vpn_bot` vào group là gửi được.
Đã gửi một tin nhắn test thật qua đúng hàm `send_telegram` của probe và nhận
`ok=true`.

## Việc 6 — đã deploy

**XHTTP qua Cloudflare** (`docs/prep/xhttp-cloudflare-nodes.md`): thử tay trên
OR-001: `packet-up` chạy (611 ms), `auto` chạy, `stream-up`/`stream-one` fail.
Đưa vào code: `XHTTP_ENABLED`, `cfvpnctl xhttp enable|disable`, inbound
`127.0.0.1:10002` path `/api/v2/stream`, ingress cloudflared, dòng
`<node>-XHTTP`, cột D1 `xhttp_enabled` (migration 0020). Bật trên OR-001,
JPY-01, VNM-01. Có trong PROXY, không trong AUTO.

**DERP** (`docs/prep/tailscale-derp.md`): xem "Bổ sung 3" — trạng thái cuối
là 2 region riêng (900 HKG-01, 901 JPY-01), `OmitDefaultRegions: false`, quản
lý bằng `cfvpnctl derp china-mode on|off`.

**NaiveProxy**: đã deploy rồi gỡ theo yêu cầu (xem Bổ sung).

## Bổ sung 1 — XHTTP-Direct trên JPY-01 (không qua Cloudflare)

- Hostname `cdn-82169439.duylinh.net` → 45.143.131.36, cert Let's Encrypt qua
  Caddy DNS-01. Caddy trên :443 phục vụ site thật (`/var/www/site`) cho mọi
  path, chỉ reverse_proxy đúng một path ngẫu nhiên dài (`XHTTP_DIRECT_PATH`
  trong env) tới xray, upstream h2c, `flush_interval -1`.
- Xray inbound `vless-xhttp-direct` `127.0.0.1:10003`, xhttp, mode
  **stream-one** (server ghim; client packet-up/auto bị từ chối, đã verify),
  không TLS ở xray. Render từ env nên sync panel không xoá.
- Code: `cfvpnctl xhttp-direct enable --host --path | disable`, dòng
  `kulinh@JPY-01-XHTTP-Direct` (address = domain, `security=tls`, `sni=domain`)
  ở cả hai builder, cột D1 `xhttp_direct_host/path` (migration 0021),
  `d1-set-node.sh JPY-01 xhttp-direct`. Trong PROXY, **không** trong AUTO.
- Verify từ VNM-01: qua node 350–393 ms (204, nhiều lần); `https://<domain>/`
  và path sai → site thật 200; GET thường đúng path → 404 rỗng của xray (chỉ
  ai biết path mới thấy).

## Bổ sung 2 — gỡ NaiveProxy hoàn toàn

- JPY-01: bỏ block forward_proxy và site naive khỏi Caddyfile (backup ở
  `/root/Caddyfile.pre-naive-removal`), giữ Caddy, site thật và handle
  XHTTP-Direct; restart; site 200, XHTTP-Direct 204, HTTPUpgrade/XHTTP/HY2 OK.
  Tên unit `naive-caddy`, đường dẫn `/etc/naive`, binary `caddy-naive` giữ
  nguyên (Caddy vẫn cần cho XHTTP-Direct); chỉ là tên cũ.
- OR-001: disable + xoá unit, `/etc/naive`, `/var/lib/naive`, binary, decoy;
  ufw đóng 443/tcp; xray/cloudflared/hysteria không ảnh hưởng.
- Xoá 2 bản ghi DNS `naive-62abc468` và `naive-74df6c7b`; xoá mọi `naive.txt`
  trong backup và file mật khẩu tạm; xoá `docs/prep/naiveproxy-jpy-01-or-001.md`.
  Naive chưa từng vào cfvpnctl/Worker/D1 nên không có code hay migration phải gỡ.
- **Anh tự gỡ:** rule forward 5373→443 (TCP+UDP) trên NAT TierHive do anh tạo
  tay; hiện port 5373 không còn gì trả lời.

## Bổ sung 3 — Tailscale DERP: API key thu hồi, OAuth client, china-mode, region 901

- **Việc A.** API key cũ đã thu hồi. OAuth client (scope duy nhất Policy File:
  Write) nằm ở `/etc/cfvpn/tailscale-oauth.env` (root, 600, gitignore).
  `OmitDefaultRegions` về **false**, giữ region 900. Verify: VNM-01 netcheck
  thấy Hong Kong/Singapore/Tokyo + HKG-01, `debug derp 900` OK, SIN-01 có
  relay trở lại (sin 0.4 ms), SSH qua Tailscale tới SIN-01/JPY-02/USA-01 OK.
- **Việc B.** `cfvpnctl derp china-mode on|off`, `derp show`, `derp region
  add|remove` (README có hướng dẫn: gõ `on` trước khi bay Trung Quốc, `off`
  khi về). Đọc policy qua OAuth, patch đúng khoá bằng RFC 6902 qua thư viện
  `tailscale/hujson` nên comment và định dạng giữ nguyên, validate bằng API,
  ghi với `If-Match`, snapshot trước/sau vào `/root/cfvpn-backups/acl/`, rồi
  chạy `tailscale netcheck` và in ra. Test: 11 unit test (hujson patch, HTTP
  client với server giả, lệnh); test thật on → off: hai snapshot chỉ khác đúng
  khoá `OmitDefaultRegions`, kết thúc ở off. Lưu ý: `go.mod` nâng `go 1.22 →
  1.26` do thư viện hujson (toolchain trên VNM-01 là 1.26.2).
- **Việc C.** derper trên JPY-01 (`derp-da32d5af.duylinh.net`, TCP 8443, STUN
  3478, cert lego DNS-01 tới 10/12/2026, cron renew hàng tháng, `--verify-clients`).
  Region 901 thêm bằng `cfvpnctl derp region add`. Verify: probe 200, STUN
  trả lời, `debug derp 901` OK từ VNM-01 **và từ SIN-01** (SIN-01 tới JPY-01
  68 ms, khác với HKG-01 không tới được). china-mode on: VNM-01 chỉ thấy
  HKG-01 58 ms + JPY-01 121 ms, SIN-01 thấy JPY-01 70 ms; SSH qua Tailscale
  vẫn chạy; rồi off: relay công cộng quay lại kèm cả 2 region.

## Bổ sung 4 — Đo lại XHTTP-Direct (Việc D)

10 request xen kẽ mỗi đường trong cùng cửa sổ thời gian, từ VNM-01, ms:

| Đường | median | p90 | min | max |
|---|---|---|---|---|
| JPY-01-HTTPUpgrade | 353 | 368 | 340 | 385 |
| JPY-01-XHTTP (qua CF) | 94 | 178 | 90 | 277 |
| JPY-01-XHTTP-Direct | 128 | 366 | 120 | 390 |
| JPY-02-Reality | 366 | 395 | 328 | 400 |

XHTTP-Direct **nhanh hơn** HTTPUpgrade 225 ms median, không cần chỉnh Caddy
(đã có `flush_interval -1`, upstream h2c, stream-one ở cả server lẫn
subscription). p90 của XHTTP-Direct dao động (366) do vài request lẻ chậm,
không phải buffering.

## Bổ sung 5 — Dọn secret (Việc E)

- Quét toàn bộ lịch sử nhánh (`git log -p main..HEAD`) và working tree: không
  có API key Tailscale, OAuth secret, Cloudflare token, Telegram token, mật
  khẩu hay private key Reality trong bất kỳ commit nào. Không cần rewrite lịch sử.
- `.gitignore` nay phủ `*.env` (trừ `*.env.example`), `/etc/cfvpn/*.env`,
  `cfvpn-backups/`, `/root/cfvpn-backups/`; kiểm tra bằng `git check-ignore`.
- Ngoài repo: `.claude/settings.local.json` (gitignore toàn cục, không track)
  vẫn chứa Cloudflare API token dạng plaintext trong các rule allow; anh nên
  xoay token đó hoặc dọn các rule.
- Cron probe: 245 dòng log, 4 dòng FAIL đều là test tay của em (XHTTP
  stream-up/stream-one qua CF và packet-up/auto vào đường direct), không phải
  sự cố.

## Kết quả probe cuối (ms, từ VNM-01, tất cả 204)

| Đường | Trước (19:38Z) | Cuối (21:32Z) |
|---|---|---|
| HAN-01 Reality / HY2 | 173 / 98 | 132 / 76 |
| HKG-01 Reality / HY2 | 204 / 102 | 165 / 113 |
| JPY-01 HTTPUpgrade / XHTTP / XHTTP-Direct / HY2 | 344 / — / — / 265 | 369 / 164 / 390 / 232 |
| JPY-02 Reality (HY2 gỡ) | 595 / 246 | 347 |
| OR-001 HTTPUpgrade / XHTTP / HY2 | 788 / — / 405 | 850 / 332 / 383 |
| SIN-01 Reality (HY2 gỡ) | 214 / 143 | 150 |
| USA-01 Reality / HY2 | 695 / 430 | 659 / 431 |
| VNM-01 HTTPUpgrade / XHTTP | 223 / — | 233 / 169 |
| VNM-02 Reality / HY2 | 419 / 198 | 551 / 183 |

**19 đường**: 6 Reality, 3 HTTPUpgrade, 3 XHTTP qua Cloudflare, 1 XHTTP-Direct,
6 HY2. DERP: 2 region riêng (900 HKG-01, 901 JPY-01), china-mode **off**.
Drift check cuối 9/9 khớp. Go 0 fail (17 package), Worker 141 pass, pytest 8
pass, shellcheck sạch. Test thật từ Trung Quốc vẫn là bước kiểm chứng cuối.

## Việc anh còn phải làm tay

1. **Import `final/RWL8899.conf`** trên Shadowrocket, xác nhận AUTO (5),
   HY2-BACKUP (6) và node lẻ `JPY-01-XHTTP-Direct`.
3. **Gỡ forward 5373→443** trên NAT TierHive (do anh tạo tay, không còn gì trả lời sau nó).
4. **Cloudflare API token** trong `.claude/settings.local.json`: xoay hoặc dọn rule.
5. **Trước khi bay Trung Quốc:** `cfvpnctl derp china-mode on` trên VNM-01;
   về nhà: `cfvpnctl derp china-mode off`. Nhớ SIN-01 chỉ relay được qua JPY-01.
6. **Merge PR #9.**
