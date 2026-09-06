package commands

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/xray"
)

func TestRunStatus(t *testing.T) {
	dir := t.TempDir()
	oldEnv, oldXray := envFilePath, xrayConfigPath
	envFilePath = filepath.Join(dir, "cfvpn.env")
	xrayConfigPath = filepath.Join(dir, "xray.json")
	t.Cleanup(func() {
		envFilePath, xrayConfigPath = oldEnv, oldXray
	})

	if err := state.SaveAtomic(envFilePath, map[string]string{
		"DOMAIN":      "127.0.0.1:1",
		"TUNNEL_UUID": "64f1b549-fe6c-4a02-8493-0ad58f0b2f3b",
	}, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := xray.NewBaseConfig("user1", "uuid-1", "pw-1")
	if err := xray.SaveAtomic(xrayConfigPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	r := &recordingRunner{}
	var out bytes.Buffer
	if err := RunStatus(context.Background(), r, &out); err != nil {
		t.Fatalf("RunStatus: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"cfvpn-xray.service:",
		"cfvpn-cloudflared.service:",
		"DOMAIN:",
		"TUNNEL_UUID:",
		"probe:",
		"users:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}

	if len(r.calls) != 2 {
		t.Fatalf("expected 2 is-active calls, got %d: %#v", len(r.calls), r.calls)
	}
	for i, unit := range []string{"cfvpn-xray.service", "cfvpn-cloudflared.service"} {
		inv := strings.Join(r.calls[i], " ")
		if inv != "systemctl is-active --quiet "+unit {
			t.Fatalf("call %d = %q, want is-active for %s", i, inv, unit)
		}
	}
}
