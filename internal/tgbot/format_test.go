package tgbot

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Real outputs, as cfvpnctl prints them on VNM-01 (2026-09-12).
const (
	sampleRulesUAE     = "rules_mode = uae — Shadowrocket .conf inlines sr_proxy_list_UAE (OTT calls + sites TDRA blocks)\nSubscription links without ?rules= follow it from the next refresh (pull the RWL8899 config in Shadowrocket).\n"
	sampleRulesDefault = "rules_mode = cn (default, never set) — Shadowrocket .conf inlines sr_proxy_list_CN (GFW list)\n"
	sampleRulesSet     = "rules_mode = cn (set 2026-09-12T05:57:41Z) — Shadowrocket .conf inlines sr_proxy_list_CN (GFW list)\n"
	sampleChinaNoop    = "china-mode off: already in the requested state; policy untouched (snapshot /root/cfvpn-backups/acl/20260912T055950Z.before.json)\n" +
		"--- tailscale netcheck ---\n" +
		"2026/09/12 13:00:00 portmap: monitor: gateway and self IP changed: gw=10.0.0.1 self=10.0.0.199\n\n" +
		"Report:\n * Time: 2026-09-12 13:00:00.951238164+07:00\n * UDP: true\n * IPv4: yes, 222.252.55.33:56848\n" +
		" * Nearest DERP: Hong Kong\n * DERP latency:\n  - hkg: 25.6ms  (Hong Kong)\n  - sin: 46.8ms  (Singapore)\n  - hkg: 51.8ms  (HKG-01)\n" +
		"  - tok: 65.9ms  (Tokyo)\n  - jpy: 125.5ms (JPY-01)\n  - sao:         (São Paulo)\n  - dbi:         (Dubai)\n"
	sampleChinaOn  = "china-mode on: policy updated (before: /root/a.json, after: /root/b.json)\n--- tailscale netcheck ---\nReport:\n * UDP: false\n * Nearest DERP: HKG-01\n * DERP latency:\n  - hkg: 51.8ms  (HKG-01)\n  - jpy: 125.5ms (JPY-01)\n"
	sampleDerpShow = "OmitDefaultRegions: false (china-mode off)\nregion 900 hkg (HKG-01): derp-f2a4f360.duylinh.net derp=8443 stun=3478\nregion 901 jpy (JPY-01): derp-da32d5af.duylinh.net derp=8443 stun=3478\n"
)

func TestFmtModeCondensesTheRealOutput(t *testing.T) {
	got := fmtMode("uae", 11*time.Second, sampleRulesUAE, sampleChinaNoop)
	want := "✅ <b>Chế độ UAE</b> · 11 s\n" +
		"📄 Config RWL8899 → list UAE (gọi OTT + site TDRA chặn)\n" +
		"🌐 China-mode → off · đã off sẵn, policy giữ nguyên\n" +
		"📶 Relay: Hong Kong 26 ms · HKG-01 52 ms · JPY-01 126 ms\n" +
		"👉 Kéo cập nhật config RWL8899 trong Shadowrocket. Ở UAE bỏ module zalo_zalopay."
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	for _, noise := range []string{"portmap", "snapshot", "/root/", "--- tailscale", "São Paulo", "Dubai", "Singapore"} {
		if strings.Contains(got, noise) {
			t.Fatalf("noise %q leaked into the message", noise)
		}
	}
}

func TestFmtModeChinaAndHome(t *testing.T) {
	got := fmtMode("china", 25*time.Second, sampleRulesSet, sampleChinaOn)
	for _, want := range []string{
		"✅ <b>Chế độ China</b> · 25 s",
		"📄 Config RWL8899 → list CN (vượt GFW) · đặt 12/09 12:57",
		"🌐 China-mode → on · policy đã đổi: chỉ dùng relay riêng HKG-01/JPY-01 (SIN-01 chỉ qua JPY-01)",
		"📶 Relay: HKG-01 52 ms · JPY-01 126 ms",
		"⚠️ Netcheck: UDP bị chặn trên VNM-01",
		"👉 Kéo cập nhật config RWL8899 trong Shadowrocket.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	home := fmtMode("home", time.Second, sampleRulesDefault, sampleChinaNoop)
	if !strings.HasPrefix(home, "✅ <b>Về mặc định (China)</b> · 1 s\n📄 Config RWL8899 → list CN (vượt GFW) · mặc định") || !strings.Contains(home, "nạp lại module zalo_zalopay") {
		t.Fatalf("home:\n%s", home)
	}
}

func TestFmtStatus(t *testing.T) {
	got := fmtStatus(sampleRulesDefault, sampleDerpShow)
	want := "ℹ️ <b>Chế độ hiện tại</b>\n" +
		"📄 Config RWL8899: list CN (vượt GFW) · mặc định\n" +
		"🌐 China-mode: off · relay công cộng + relay riêng\n" +
		"🛰 Relay riêng: HKG-01 (900) · JPY-01 (901)"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	derpOnly := fmtStatus("", "OmitDefaultRegions: true (china-mode on)\nregions: none\n")
	if strings.Contains(derpOnly, "Config") || !strings.Contains(derpOnly, "China-mode: on · chỉ dùng relay riêng") || !strings.Contains(derpOnly, "Relay riêng: không có") {
		t.Fatalf("derp-only:\n%s", derpOnly)
	}
}

func TestFmtChinaAndFailure(t *testing.T) {
	got := fmtChina("on", 30*time.Second, sampleChinaOn)
	if !strings.HasPrefix(got, "✅ <b>China-mode on</b> · 30 s\n🌐 China-mode → on · policy đã đổi") || !strings.Contains(got, "📶 Relay: HKG-01 52 ms · JPY-01 126 ms") {
		t.Fatalf("china:\n%s", got)
	}
	f := fmtFailure("Chế độ UAE", 3*time.Second, errors.New("write rules_mode: cf api error <1000>"), "rules_mode = uae\n", "")
	if !strings.HasPrefix(f, "✖ <b>Chế độ UAE thất bại</b> · 3 s\n<code>write rules_mode: cf api error &lt;1000&gt;</code>\n<pre>rules_mode = uae</pre>") {
		t.Fatalf("failure:\n%s", f)
	}
}

func TestUnparseableOutputFallsBackToRawText(t *testing.T) {
	got := fmtMode("uae", time.Second, "something new\n", "weird output <x>\n2026/09/12 portmap: noise\n")
	if !strings.Contains(got, "<pre>something new</pre>") || !strings.Contains(got, "<pre>weird output &lt;x&gt;</pre>") || strings.Contains(got, "portmap") {
		t.Fatalf("fallback:\n%s", got)
	}
	if plainText("✅ <b>x</b> &lt;y&gt;") != "✅ x <y>" {
		t.Fatal("plainText must strip tags and unescape")
	}
}
