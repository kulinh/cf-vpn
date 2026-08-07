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
	want := "xray run -c /etc/cfvpn/xray/config.json"
	if !strings.Contains(u, want) {
		t.Fatalf("missing ExecStart: %s", want)
	}
}

func TestXrayUnitHasDirectModeServiceSettings(t *testing.T) {
	u := XrayService("/etc/cfvpn/xray/config.json")
	for _, want := range []string{
		"User=root",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("missing %s in unit: %s", want, u)
		}
	}
}

// SIGHUP terminates xray and systemd counts it as a clean exit, so an
// ExecReload that HUPs the process silently kills the service (fleet outage
// 2026-07-26 via acme.sh's `systemctl reload` hook). The unit must not
// advertise reload support.
func TestXrayUnitHasNoExecReload(t *testing.T) {
	u := XrayService("/etc/cfvpn/xray/config.json")
	if strings.Contains(u, "ExecReload") {
		t.Fatalf("unit must not define ExecReload (xray dies on SIGHUP): %s", u)
	}
}

func TestAgentServiceContainsExpectedExecStart(t *testing.T) {
	u := AgentService()
	want := "ExecStart=/usr/local/bin/cfvpn-agent"
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

func TestHysteriaServiceContainsExpectedExecStart(t *testing.T) {
	u := HysteriaService("/etc/cfvpn/hysteria/config.yaml")
	want := "ExecStart=/usr/local/bin/hysteria server -c /etc/cfvpn/hysteria/config.yaml"
	if !strings.Contains(u, want) {
		t.Fatalf("missing ExecStart: %s", want)
	}
}

func TestHysteriaServiceRestartsOnFailure(t *testing.T) {
	u := HysteriaService("/etc/cfvpn/hysteria/config.yaml")
	if !strings.Contains(u, "Restart=on-failure") {
		t.Fatalf("missing Restart=on-failure in unit: %s", u)
	}
}

func TestCertRenewTimerRunsDaily(t *testing.T) {
	u := CertRenewTimer()
	if !strings.Contains(u, "OnCalendar=daily") {
		t.Fatalf("missing OnCalendar=daily in timer: %s", u)
	}
}
