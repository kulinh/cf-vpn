package tgbot

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Everything the bot posts is Telegram HTML: a headline, one line per fact,
// one line telling the operator what to do next. The raw cfvpnctl output is
// parsed for the few facts that matter; when a parser does not recognise the
// output it falls back to the raw text (trimmed) in a <pre> block rather than
// hiding it.

const usage = "🤖 <b>cf-vpn bot</b>\n" +
	"/mode status — chế độ hiện tại (list nhúng trong config RWL8899 + DERP)\n" +
	"/mode china — list CN + china-mode on (chỉ dùng relay riêng)\n" +
	"/mode uae — list UAE (gọi OTT) + china-mode off\n" +
	"/mode home — về mặc định: list CN, china-mode off\n" +
	"/china on | off | status — chỉ chỉnh DERP china-mode\n" +
	"/derp — như /china status\n\n" +
	"Sau /mode: kéo cập nhật config RWL8899 trong Shadowrocket (link không đổi).\n" +
	"Tin nhắn trong nhóm này tự xoá sau 24 giờ."

// Vietnam time for the timestamps shown in the chat.
var vnZone = time.FixedZone("ICT", 7*3600)

func esc(s string) string { return html.EscapeString(s) }

func fmtTook(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s < 1 {
		return "<1 s"
	}
	return fmt.Sprintf("%d s", s)
}

// rawBlock returns the output as a <pre> block, minus noise lines, capped.
func rawBlock(out string) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(l, "portmap:") || strings.HasPrefix(strings.TrimSpace(l), "---") {
			continue
		}
		keep = append(keep, l)
	}
	if len(keep) > 12 {
		keep = keep[len(keep)-12:]
	}
	s := strings.TrimSpace(strings.Join(keep, "\n"))
	if s == "" {
		return ""
	}
	if len(s) > 1200 {
		s = s[len(s)-1200:]
	}
	return "<pre>" + esc(s) + "</pre>"
}

// ---- rules mode -----------------------------------------------------------

var reRulesMode = regexp.MustCompile(`rules_mode = "?([a-z]+)"?(?: \(([^)]*)\))?`)

// rulesLine renders the rules_mode output of cfvpnctl.
func rulesLine(out string) (string, bool) {
	m := reRulesMode.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	mode, note := m[1], m[2]
	var desc string
	switch mode {
	case "cn":
		desc = "list CN (vượt GFW)"
	case "uae":
		desc = "list UAE (gọi OTT + site TDRA chặn)"
	case "none":
		desc = "không nhúng list (tự nạp module)"
	default:
		desc = "giá trị lạ " + esc(mode) + " → Worker dùng list CN"
	}
	line := "📄 Config RWL8899: " + desc
	switch {
	case strings.HasPrefix(note, "default"):
		line += " · mặc định"
	case strings.HasPrefix(note, "set "):
		if t, err := time.Parse(time.RFC3339, strings.TrimPrefix(note, "set ")); err == nil {
			line += " · đặt " + t.In(vnZone).Format("02/01 15:04")
		}
	}
	return line, true
}

// ---- DERP / china-mode ----------------------------------------------------

var (
	reChinaLine = regexp.MustCompile(`china-mode (on|off): (policy updated|already in the requested state)`)
	reOmit      = regexp.MustCompile(`OmitDefaultRegions: (true|false)`)
	reRegion    = regexp.MustCompile(`region (\d+) [a-z0-9]+ \(([^)]+)\): (\S+) derp=(\d+) stun=(\d+)`)
	reNearest   = regexp.MustCompile(`\* Nearest DERP: (.+)`)
	reUDP       = regexp.MustCompile(`\* UDP: (true|false)`)
	reLatency   = regexp.MustCompile(`(?m)^\s*-\s*([a-z0-9]+):\s*(?:([\d.]+)ms)?\s*\((.+)\)\s*$`)
	reCustom    = regexp.MustCompile(`^[A-Z]{3}-\d{2}$`)
)

func chinaDesc(on bool) string {
	if on {
		return "chỉ dùng relay riêng HKG-01/JPY-01 (SIN-01 chỉ qua JPY-01)"
	}
	return "relay công cộng + relay riêng"
}

// chinaLine renders the "china-mode …:" line of `cfvpnctl derp china-mode`.
func chinaLine(out string) (string, bool) {
	m := reChinaLine.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	on := m[1] == "on"
	if m[2] == "policy updated" {
		return "🌐 China-mode → " + m[1] + " · policy đã đổi: " + chinaDesc(on), true
	}
	return "🌐 China-mode → " + m[1] + " · đã " + m[1] + " sẵn, policy giữ nguyên", true
}

type relay struct {
	name string
	ms   string
}

// relayLine condenses `tailscale netcheck`: the nearest public relay plus our
// own regions (names like HKG-01), with a warning when UDP is blocked.
func relayLine(out string) (string, bool) {
	nearest := ""
	if m := reNearest.FindStringSubmatch(out); m != nil {
		nearest = strings.TrimSpace(m[1])
	}
	var picks []relay
	seen := map[string]bool{}
	for _, m := range reLatency.FindAllStringSubmatch(out, -1) {
		name, ms := strings.TrimSpace(m[3]), m[2]
		if seen[name] {
			continue
		}
		if name == nearest || reCustom.MatchString(name) {
			seen[name] = true
			if ms == "" {
				ms = "—"
			} else if f, err := strconv.ParseFloat(ms, 64); err == nil {
				ms = fmt.Sprintf("%.0f ms", f)
			}
			picks = append(picks, relay{name, ms})
		}
	}
	if len(picks) == 0 && nearest == "" {
		return "", false
	}
	var parts []string
	for _, p := range picks {
		parts = append(parts, esc(p.name)+" "+p.ms)
	}
	line := "📶 Relay: " + strings.Join(parts, " · ")
	if m := reUDP.FindStringSubmatch(out); m != nil && m[1] == "false" {
		line += "\n⚠️ Netcheck: UDP bị chặn trên VNM-01"
	}
	return line, true
}

// derpStatusLines renders `cfvpnctl derp show`.
func derpStatusLines(out string) (string, bool) {
	m := reOmit.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	on := m[1] == "true"
	state := "off"
	if on {
		state = "on"
	}
	lines := []string{"🌐 China-mode: " + state + " · " + chinaDesc(on)}
	var regions []string
	for _, r := range reRegion.FindAllStringSubmatch(out, -1) {
		regions = append(regions, fmt.Sprintf("%s (%s)", esc(r[2]), r[1]))
	}
	if len(regions) > 0 {
		lines = append(lines, "🛰 Relay riêng: "+strings.Join(regions, " · "))
	} else {
		lines = append(lines, "🛰 Relay riêng: không có")
	}
	return strings.Join(lines, "\n"), true
}

// ---- messages -------------------------------------------------------------

func modeTitle(sub string) string {
	switch sub {
	case "china":
		return "Chế độ China"
	case "uae":
		return "Chế độ UAE"
	default:
		return "Về mặc định (China)"
	}
}

func modeHint(sub string) string {
	switch sub {
	case "uae":
		return "👉 Kéo cập nhật config RWL8899 trong Shadowrocket. Ở UAE bỏ module zalo_zalopay."
	case "home":
		return "👉 Kéo cập nhật config RWL8899 trong Shadowrocket; nạp lại module zalo_zalopay nếu đã bỏ."
	default:
		return "👉 Kéo cập nhật config RWL8899 trong Shadowrocket."
	}
}

func fmtAck(kind, sub string) string {
	switch kind {
	case "mode":
		return "⏳ Đang chuyển sang <b>" + modeTitle(sub) + "</b>… (ghi D1 → chỉnh DERP → đo netcheck, khoảng 10–60 s)"
	default:
		return "⏳ Đang chuyển china-mode " + sub + "… (chỉnh policy, chờ DERP map, đo netcheck)"
	}
}

// fmtMode renders a completed /mode china|uae|home.
func fmtMode(sub string, took time.Duration, rulesOut, chinaOut string) string {
	lines := []string{"✅ <b>" + modeTitle(sub) + "</b> · " + fmtTook(took)}
	if l, ok := rulesLine(rulesOut); ok {
		lines = append(lines, strings.Replace(l, "📄 Config RWL8899: ", "📄 Config RWL8899 → ", 1))
	} else if r := rawBlock(rulesOut); r != "" {
		lines = append(lines, r)
	}
	if l, ok := chinaLine(chinaOut); ok {
		lines = append(lines, l)
	}
	if l, ok := relayLine(chinaOut); ok {
		lines = append(lines, l)
	}
	if _, ok := chinaLine(chinaOut); !ok {
		if r := rawBlock(chinaOut); r != "" {
			lines = append(lines, r)
		}
	}
	lines = append(lines, modeHint(sub))
	return strings.Join(lines, "\n")
}

// fmtStatus renders /mode status (and /derp, /china status).
func fmtStatus(rulesOut, derpOut string) string {
	lines := []string{"ℹ️ <b>Chế độ hiện tại</b>"}
	if rulesOut != "" {
		if l, ok := rulesLine(rulesOut); ok {
			lines = append(lines, l)
		} else if r := rawBlock(rulesOut); r != "" {
			lines = append(lines, r)
		}
	}
	if l, ok := derpStatusLines(derpOut); ok {
		lines = append(lines, l)
	} else if r := rawBlock(derpOut); r != "" {
		lines = append(lines, r)
	}
	return strings.Join(lines, "\n")
}

// fmtChina renders a completed /china on|off.
func fmtChina(sub string, took time.Duration, out string) string {
	lines := []string{"✅ <b>China-mode " + sub + "</b> · " + fmtTook(took)}
	if l, ok := chinaLine(out); ok {
		lines = append(lines, l)
	} else if r := rawBlock(out); r != "" {
		lines = append(lines, r)
	}
	if l, ok := relayLine(out); ok {
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// fmtFailure renders an error with the tail of whatever was produced.
func fmtFailure(title string, took time.Duration, err error, outputs ...string) string {
	lines := []string{"✖ <b>" + title + " thất bại</b> · " + fmtTook(took), "<code>" + esc(err.Error()) + "</code>"}
	if r := rawBlock(strings.Join(outputs, "\n")); r != "" {
		lines = append(lines, r)
	}
	return strings.Join(lines, "\n")
}

var reTags = regexp.MustCompile(`<[^>]+>`)

// plainText strips the HTML for the length-limited or parse-failed fallback.
func plainText(s string) string {
	return html.UnescapeString(reTags.ReplaceAllString(s, ""))
}
