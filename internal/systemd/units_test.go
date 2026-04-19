package systemd

import (
	"strings"
	"testing"
)

func TestCloudflaredUnitContainsExpectedExecStart(t *testing.T) {
	u := CloudflaredService("/etc/cfvpn/cloudflared/config.yml")
	want := "cloudflared tunnel --config /etc/cfvpn/cloudflared/config.yml run"
	if !strings.Contains(u, want) {
		t.Fatalf("missing ExecStart: %s", want)
	}
}

func TestXrayUnitContainsExpectedExecStart(t *testing.T) {
	u := XrayService("/etc/cfvpn/xray/config.json")
	want := "xray -config /etc/cfvpn/xray/config.json"
	if !strings.Contains(u, want) {
		t.Fatalf("missing ExecStart: %s", want)
	}
}

func TestHealthcheckServiceContainsExecStart(t *testing.T) {
	u := HealthcheckService()
	want := "cfvpnctl healthcheck run"
	if !strings.Contains(u, want) {
		t.Fatalf("missing ExecStart: %s", want)
	}
}

func TestHealthcheckTimerHasUnitTarget(t *testing.T) {
	u := HealthcheckTimer()
	if !strings.Contains(u, "Unit=cfvpn-healthcheck.service") {
		t.Fatalf("missing Unit=cfvpn-healthcheck.service in timer: %s", u)
	}
	if !strings.Contains(u, "OnUnitActiveSec=5m") {
		t.Fatalf("missing OnUnitActiveSec=5m in timer: %s", u)
	}
}
