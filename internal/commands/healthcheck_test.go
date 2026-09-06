package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsHealthyCode(t *testing.T) {
	if !IsHealthyCode(101) {
		t.Fatalf("101 (WebSocket Upgrade accepted) must be healthy")
	}
	// M-G4: 426 is the synthetic code cfvpn-agent returns after a successful
	// TCP open against a Reality node — most of the fleet. Rejecting it made a
	// successful probe report ok:false.
	if !IsHealthyCode(426) {
		t.Fatalf("426 (synthetic Reality TCP-open success) must be healthy")
	}
	// xray 26.x's HTTPUpgrade transport closes plain GETs without writing a
	// status line, so a real 400 from the wire is still unhealthy.
	for _, code := range []int{0, 200, 400, 502} {
		if IsHealthyCode(code) {
			t.Fatalf("code %d must be unhealthy", code)
		}
	}
}

type recordingRunner struct {
	calls [][]string
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	inv := append([]string{name}, args...)
	r.calls = append(r.calls, inv)
	return nil
}

func TestRunHealthcheckInstall(t *testing.T) {
	dir := t.TempDir()
	oldDir := systemdUnitDir
	systemdUnitDir = dir
	t.Cleanup(func() { systemdUnitDir = oldDir })

	r := &recordingRunner{}
	var out bytes.Buffer
	if err := RunHealthcheckInstall(context.Background(), r, &out); err != nil {
		t.Fatalf("RunHealthcheckInstall: %v", err)
	}

	svcPath := filepath.Join(dir, "cfvpn-healthcheck.service")
	timerPath := filepath.Join(dir, "cfvpn-healthcheck.timer")

	svc, err := os.ReadFile(svcPath)
	if err != nil {
		t.Fatalf("service file not written: %v", err)
	}
	if !strings.Contains(string(svc), "cfvpn periodic healthcheck") {
		t.Fatalf("service file missing expected description: %s", svc)
	}

	timer, err := os.ReadFile(timerPath)
	if err != nil {
		t.Fatalf("timer file not written: %v", err)
	}
	if !strings.Contains(string(timer), "cfvpn healthcheck timer") {
		t.Fatalf("timer file missing expected description: %s", timer)
	}

	if len(r.calls) != 2 {
		t.Fatalf("expected 2 runner calls, got %d: %#v", len(r.calls), r.calls)
	}
	if got := strings.Join(r.calls[0], " "); got != "systemctl daemon-reload" {
		t.Fatalf("first call = %q, want systemctl daemon-reload", got)
	}
	if got := strings.Join(r.calls[1], " "); got != "systemctl enable --now cfvpn-healthcheck.timer" {
		t.Fatalf("second call = %q, want systemctl enable --now cfvpn-healthcheck.timer", got)
	}

	if !strings.Contains(out.String(), "installed") {
		t.Fatalf("stdout missing 'installed': %q", out.String())
	}
}
