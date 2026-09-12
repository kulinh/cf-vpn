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

## Bổ sung 6 — điều khiển china-mode bằng Telegram (@rwl_vpn_bot)

Bot `cfvpn-tgbot` (unit `cfvpn-tgbot.service`, **chỉ trên VNM-01**) long-poll
`@rwl_vpn_bot` và nhận đúng các lệnh sau trong group `-1003806233980`:

```
/china status    (hoặc /derp)   — xem region + china-mode on/off
/china on                        — chỉ dùng relay riêng (trước khi bay TQ)
/china off                       — relay công cộng + riêng (bình thường)
```

Bot gọi thẳng `commands.RunDerpChinaMode` / `RunDerpShow` trong process nên
snapshot policy, validate, `If-Match` và `tailscale netcheck` giống hệt khi gõ
CLI; output được trả về ngay trong group. Lý do đặt bot trên VNM-01 thay vì
trong Worker: lệnh cần OAuth client ở `/etc/cfvpn/tailscale-oauth.env`, để
secret không phải rời máy. Bot này **khác** bot của Worker nên không tranh
update stream (Telegram chỉ cho mỗi bot một trong hai: webhook hoặc getUpdates).

Rào an toàn: chỉ phục vụ đúng chat id cấu hình; chỉ trả lời 2 lệnh trên và im
lặng với mọi lệnh khác (`/status`, `/nodes`, `/sub`… là của bot panel, cùng
group); bỏ qua lệnh gắn `@bot_khác`; chạy tuần tự một lệnh một lúc (hai lần
ghi ACL song song sẽ đụng nhau); khi restart thì bỏ backlog, không replay lệnh
đã xếp hàng lúc bot chết. Privacy mode đang bật nên bot chỉ nhận slash command.

Verify thật: `cfvpn-tgbot --simulate "/derp"` và `"/china on"` → `"/china off"`
chạy qua đúng bot và group, on 14 s / off 12 s, netcheck in ra trong group,
`/status` bị bỏ qua, trạng thái cuối **china-mode off**. Menu lệnh đã đăng ký
bằng `setMyCommands` chỉ cho chat này. Token lấy từ
`/etc/cfvpn/fleet-probe.env` (mode 600, gitignore).

## Bổ sung 7 — dọn secret tồn, quyết định giữ CF token, và một drift tìm ra

**CF token: giữ nguyên theo quyết định của anh.** Em dò thật bằng các endpoint
token cần dùng (D1 query, zones, DNS read, tunnels, workers scripts) — tất cả
đều `success`, nên token đang dùng trên fleet vẫn sống và không có gì đứt.
`GET /user/tokens/verify` trả "Invalid API Token" (code 1000) và `GET /user`
trả 9109 chỉ vì đây là token **account-scoped**, hai endpoint đó là user-scoped
— **không dùng chúng để kết luận token chết**. Nếu sau này muốn xoay thật thì
phải cập nhật `CF_API_TOKEN` trong `/etc/cfvpn/cfvpn.env` của cả 9 node (lego,
Caddy, derper đều đọc từ đây) cộng secret `CF_API_TOKEN` của Worker, và phân
phối trước khi xoá token cũ.

**`.claude/settings.local.json`:** xoá 27 rule có nhúng token Cloudflare hoặc
mật khẩu SSH (`SSHPASS='…'`), còn 325 rule, JSON hợp lệ, không còn match nào.
Các rule đó dư vì đã có `Bash(npx wrangler *)`, `Bash(npm *)`, `Bash(sshpass *)`
không mang secret. File này được gitignore toàn cục nên không nằm trong repo.

**Xoá file env backup tồn đọng:** 13 file `/etc/cfvpn/cfvpn.env.bak*` trên 7
node (VNM-01 3 file từ tháng 4 còn khoá `TROJAN_PASS_USER1` thời trước HY2,
JPY-02 5 file, còn lại 1 file mỗi node). Mỗi file chứa một bản copy của
`CF_API_TOKEN`. Chỉ xoá sau khi kiểm tra env sống của node đó đủ khoá và xray
đang active. USA-01 và HAN-01 vốn đã sạch. Kết quả: 0 file còn lại trên cả 9 node.

**Drift phát hiện nhân lúc đó:** HKG-01, SIN-01 và JPY-02 **thiếu khoá
`NODE_ID`** trong `/etc/cfvpn/cfvpn.env` (node cài từ trước khi có khoá này).
Hệ quả: tag trong file subscription phía node là `kulinh-Reality` thay vì
`kulinh@HKG-01-Reality`, lệch với tag panel sinh ra; và `cfvpnctl install` trên
node đó sẽ hỏng ở bước `tunnelNameForNode`. Subscription anh import từ panel
không bị ảnh hưởng vì Worker lấy id từ D1. Đã thêm `NODE_ID` và sinh lại file
subscription phía node; tag giờ là `kulinh%40HKG-01-Reality`,
`kulinh%40SIN-01-Reality`, `kulinh%40JPY-02-Reality`. Toàn fleet hiện đủ cả 5
khoá `CF_API_TOKEN`, `CF_ACCOUNT_ID`, `NODE_ID`, `MODE`, `DOMAIN`.

Probe lại sau thay đổi: 19/19 đường 204, drift 9/9 khớp.

## Bổ sung 8 — audit sr_proxy_list_CN.module và ghép với RWL8899.conf

**Cách ghép (đã làm, không cần chỉnh tay):** module của repo
`kulinh/shadowrocket-vietnamese` trỏ mọi rule tới policy `PROXY`; file
`RWL8899.conf` sinh từ panel có đúng group tên `PROXY` (select, mặc định =
`AUTO` url-test 5 đường, có thể chuyển sang `HY2-BACKUP` hoặc node lẻ). Vì
vậy chỉ cần: (1) Cài đặt > Module thêm `sr_proxy_list_CN.module` (đặt dưới
module reject nếu có); (2) import `RWL8899.conf`; (3) màn hình chính chọn
group `PROXY`. Đuôi `[Rule]` của conf giờ do Worker sinh:

```
DOMAIN,cp.rwl265.com,PROXY            # panel + subscription luôn đi proxy
DOMAIN-SUFFIX,cloudflareaccess.com,PROXY  # trang login Access
FINAL,DIRECT                          # blacklist: module quyết định cái gì đi proxy
```

Thêm `&final=proxy` vào URL subscription thì đuôi thành `FINAL,PROXY` (full
tunnel, không cần module); giá trị khác trả 400 `invalid_final`.

**Tối ưu tiếp (câu hỏi "có nên đưa RWL8899.conf vào module không"):** không —
conf chứa UUID/mật khẩu node của từng user (repo module là public), và module
Shadowrocket vốn không chứa được `[Proxy]`/`[Proxy Group]`. Làm chiều ngược
lại: Worker tải `sr_proxy_list_CN.module` từ GitHub **tại edge Cloudflare**
(cache 1 giờ; điện thoại ở TQ không cần tới GitHub), đổi policy mọi rule về
`PROXY` rồi chép thẳng vào `[Rule]` trước `FINAL,DIRECT`. Kết quả: không cần
cài module bằng tay, sửa module trên GitHub → lần refresh config sau tự có.
Conf live giờ 333 dòng, 314 rule nhúng (etag `fbadbd21`), fetch lần 2 qua
edge cache 0,16 s. Tuỳ chọn URL: `&rules=none` (đuôi trần như cũ, tự nạp
module), `&final=proxy` (không nhúng), giá trị lạ → 400 `invalid_rules`. Nếu
edge không tải được GitHub, conf có dòng
`RULE-SET,…/sr_proxy_list_CN.list,PROXY` thay cho phần nhúng (file `.list`
mới, sinh tự động cùng JSON v2rayNG; commit `c818493` trên repo module).
Worker version `75552b0f`, 159 test pass, `final/RWL8899.conf` trong backup
đã tải lại từ bản live.

**Audit module (commit `416de1c` trên master của shadowrocket-vietnamese):**
đối chiếu gfwlist, `gstatic.com/ipranges/goog.json`, RIPEstat (AS32934 Meta;
AS62014/62041/59930/44907/211157 Telegram) và test HTTP thật từ ~297 node
đại lục qua itdog.cn ngày 12/09/2026.

| Domain | itdog: node lỗi / tổng | Kết luận |
|---|---|---|
| google.com (chuẩn "bị chặn") | 251 / 297 | — |
| apple.com (chuẩn "mở") | 2 / 297 | — |
| go.dev | 142 / 298 | chặn nửa số node → giữ |
| voatiengviet.com | 257 / 298 | chặn → thêm (mục BÁO CHÍ) |
| proton.me | 256 / 298 | chặn → thêm |
| waze.com | 257 / 299 | chặn → giữ |
| notion.so | ~69 / 112 đã trả lời | chặn không đều → thêm notion.so/.site/.com |
| pages.dev | 9 / 298 | mở, nhưng giữ vì Pages/Workers hay bị chặn lẻ |
| figma.com | 10 / 298 | mở → không thêm |
| paypal.com | 2 / 298 | mở → không thêm |
| edition.cnn.com | 16 / 298 | mở → không thêm |
| bitbucket.org | 1 / 36 đã trả lời | mở → không thêm |
| kubernetes.io | 4 / 299 | mở → không thêm |
| npmjs.com | 25 / 299 (156 là 403 bot-check) | mở → giữ vì tốc độ |
| rwl265.cloudflareaccess.com | 3 / 299 | mở, vẫn ép proxy cho ổn định login |

Thay đổi trong module: Google +5 dải IPv4 lớn +4 dải IPv6 theo goog.json;
Meta +9 dải AS32934 (57.141/57.144, 163.70, 185.89.216, 2620:0:1c00::/40);
Telegram bỏ 149.154.164.0/22 (trùng), thêm 91.105.192.0/23, 185.76.151.0/24,
2a0a:f280::/32; thêm Kakao/Naver, Poe/Character/Mistral, Docker/Go/Vercel/
Netlify/Heroku/pages.dev/workers.dev, Disney+/Max/Prime, SoundCloud/
Dailymotion, Tumblr/Flickr/WordPress/Substack/Patreon/Canva, DuckDuckGo/
Startpage/Yahoo, archive.org, Mega/Box/Proton, Notion; hai mục mới BÁO CHÍ và
DNS CÔNG CỘNG (8.8.8.8, 1.1.1.1, cloudflare-dns.com). Không xoá domain nào
đang có. Tổng: 244 DOMAIN-SUFFIX, 1 DOMAIN, 8 DOMAIN-KEYWORD, 61 IP-CIDR,
không trùng, mọi dòng đúng cú pháp. `docs/v2rayng_rulesets_CN.json` sinh lại
bằng script mới `docs/module2v2rayng.py` (253 domain, 61 IP; `--check` để so
khớp), README có mục "Dùng với cf-vpn".

## Bổ sung 9 — module UAE viết lại theo kiểm chứng thật, conf có `&rules=uae`

**Cách đo:** UAE không có itdog; em dùng globalping.io, chọn đúng 2 probe
nằm trong mạng dân dụng **du (AS15802)** và **Etisalat (AS5384)**, GET
HTTPS `/` cho ~70 domain. Dấu hiệu chặn rất rõ: du = TLS handshake timeout,
Etisalat = ECONNRESET hoặc DNS timeout (Etisalat chặn cả ở tầng DNS); site
mở trả 200/30x. Chuẩn: pornhub chặn cả hai, google mở cả hai.

| Nhóm | Kết quả từ du + Etisalat | Quyết định |
|---|---|---|
| Web WhatsApp/Viber/Zalo/Telegram/Signal/Discord/Snapchat/LINE/Kakao/WeChat | mở hết | TDRA chỉ chặn kênh thoại → giữ domain + IP media |
| Google/YouTube/Netflix/X/TikTok/Threads | mở | bỏ khỏi module (proxy chỉ làm chậm) |
| Teams/Meet/Zoom | mở (đã cấp phép) | bỏ; Skype tiêu dùng đóng 05/2025 → bỏ |
| Wikipedia/Reddit/Twitch/BBC/Al Jazeera/RFA/VOA/archive.org/binance | mở | không thêm |
| ynet.co.il | mở | Israel đã bỏ chặn → không thêm |
| pornhub/xvideos/xhamster/youporn/redtube/chaturbate/stripchat/livejasmin/onlyfans | chặn cả hai | giữ/thêm |
| bet365/pokerstars/1xbet/betway/m88/dafabet | chặn cả hai (888 chỉ du; fun88, w88 mở) | thêm; fun88/w88 không |
| grindr/badoo/bumble/okcupid | chặn cả hai (tinder chỉ Etisalat; hinge mở) | thêm |
| nordvpn/protonvpn/expressvpn/surfshark/windscribe/mullvad | chặn cả hai | thêm |
| middleeasteye.net | chặn cả hai | thêm |
| imo.im | chặn trên du | thêm |
| botim.me | mở | đúng, app cấp phép, không proxy |

**Module mới (commit `688cd7c` + `e54e8c7` sửa dải trùng trên master repo module):**
96 DOMAIN-SUFFIX, 1 DOMAIN, 3 KEYWORD, 55 IP-CIDR (cũ: 103 domain, 29 IP).
Bỏ hẳn nhóm CDN (akamai/cloudfront/amazonaws/cloudflare/fastly) vì nó đẩy
nửa Internet, kể cả site UAE, qua proxy; bỏ apple.com/icloud/mzstatic
(FaceTime chỉ cần facetime/ess/ids/push.apple.com + 17.0.0.0/8). IP media
theo BGP 09/2026: Meta AS32934 đủ 23 dải, Telegram thêm 3 dải mới, và **mới
VNG/AS38244 (Zalo)** 9 dải v4 + 3 v6 collapse từ 128 prefix để cuộc gọi Zalo
không rớt. Thêm imo, Snapchat, KakaoTalk, Instagram call, nhóm DNS công
cộng. Cảnh báo trong module + README: **không nạp `zalo_zalopay` ở UAE** (nó
ép Zalo đi thẳng). Tiện thể phát hiện dải Meta 31.13.96.0/19 em thêm ở bản
CN hôm nay nằm trong 31.13.64.0/18 → bỏ ở cả hai module.

**Worker:** `?format=shadowrocket&rules=uae` nhúng module UAE thay vì CN
(`rules=cn` mặc định, `none` để trần, giá trị lạ 400); nguồn là map key →
file dưới `RULES_BASE_URL`. Version `6f241700`, 161 test pass. Generator
`docs/module2v2rayng.py` chạy cho cả CN và UAE (bản UAE không có ruleset
"Direct - China"), sinh `sr_proxy_list_UAE.list` cho fallback RULE-SET.

## Bổ sung 10 — `/mode china|uae|home`: link sub giữ nguyên, tự đổi list

Anh hỏi có on/off bằng Telegram được không: được, và mặc định luôn là China.

**Cách chạy.** D1 có bảng `settings` mới (migration 0022, đã apply prod) với
khoá `rules_mode` ∈ {cn, uae, none}. Worker: link `?format=shadowrocket` mà
KHÔNG có `&rules=` sẽ đọc khoá này để chọn list nhúng (chưa set hoặc giá trị
lạ → cn; `&rules=` ghi rõ trên link vẫn thắng). Ghi khoá này từ VNM-01 bằng
CF token có sẵn trong `cfvpn.env` (cùng đường `scripts/d1-set-node.sh`), nên
không phải mở thêm gì qua Cloudflare Access:

- CLI: `cfvpnctl rules-mode show | set cn|uae|none` (thêm `D1Query` vào client
  Cloudflare Go).
- Telegram `@rwl_vpn_bot`: `/mode status` (in rules_mode + DERP), `/mode china`
  = list CN + china-mode **on**, `/mode uae` = list UAE + china-mode off,
  `/mode home` = list CN + china-mode off. Một chuyến đi = một lệnh; nếu ghi
  D1 lỗi thì không đụng DERP. `/china on|off` vẫn giữ để chỉnh DERP riêng.

**Đã kiểm chứng live:** `rules-mode set uae` → conf tải từ link cũ nhúng
`sr_proxy_list_UAE`; `set cn` → quay về CN; `set mars` bị từ chối; bot đã
cài lại (`--setup` menu có `/mode`), `--simulate "/mode status"` trả lời vào
group đúng. Go test toàn bộ pass, Worker 162 test pass, version `504b6afc`.

**Anh nhớ:** sau `/mode …`, vào Shadowrocket kéo cập nhật config RWL8899 một
lần (link không đổi). Ở UAE thêm bước bỏ module `zalo_zalopay`.

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

1. **Import `final/RWL8899.conf`** trên Shadowrocket (tải lại từ subscription,
   đuôi `[Rule]` mới), xác nhận AUTO (5 đường), HY2-BACKUP (6 đường) và node
   lẻ `JPY-01-XHTTP-Direct`. Không cần thêm module `sr_proxy_list_CN` nữa
   (conf đã nhúng sẵn); chọn group `PROXY` trên màn hình chính.
2. **Đi UAE:** gõ `/mode uae` trong group Telegram, kéo cập nhật config
   RWL8899 trong Shadowrocket, bỏ module `zalo_zalopay`. Về nhà: `/mode home`.
3. **Trước khi bay Trung Quốc:** gõ `/mode china` (list CN + china-mode on),
   kéo cập nhật config; khi về gõ `/mode home`. Nhớ SIN-01 chỉ relay được qua
   JPY-01 khi china-mode on.
4. **Merge PR #9.**

Đã xong trong phiên, không cần làm gì thêm: Telegram alert cho fleet-probe
(@rwl_vpn_bot trong group "RWL Hub"), bot điều khiển `/china`, gỡ forward
5373→443 trên TierHive, dọn secret khỏi `.claude/settings.local.json` và xoá
13 file env backup tồn đọng. CF token giữ nguyên theo quyết định của anh.

## Cập nhật sau cùng (probe 19/19, 00:5xZ)

Sau khi thêm `NODE_ID` cho HKG-01/SIN-01/JPY-02 và xoá hết env backup: 19/19
đường trả 204, drift 9/9 khớp, toàn fleet đủ 5 khoá env bắt buộc, không còn
file `cfvpn.env.bak*` nào, `cfvpn-tgbot` và cron probe đang chạy.
