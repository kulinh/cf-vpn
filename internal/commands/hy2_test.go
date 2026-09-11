package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/state"
)

func TestHy2EnabledDefaultsOn(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want bool
	}{{"", true}, {"1", true}, {"true", true}, {"0", false}, {"false", false}, {"no", false}, {" off ", false}} {
		if got := Hy2Enabled(map[string]string{"HY2_ENABLED": tc.v}); got != tc.want {
			t.Fatalf("HY2_ENABLED=%q: got %v, want %v", tc.v, got, tc.want)
		}
	}
	if !Hy2Enabled(map[string]string{}) {
		t.Fatal("missing key must mean enabled")
	}
}

func TestCanonicalUnitsDropHysteriaWhenDisabled(t *testing.T) {
	if _, ok := canonicalUnitsFor(false)["cfvpn-hysteria.service"]; ok {
		t.Fatal("hysteria unit must be absent when HY2 is disabled")
	}
	if _, ok := canonicalUnitsFor(true)["cfvpn-hysteria.service"]; !ok {
		t.Fatal("hysteria unit must be present when HY2 is enabled")
	}
	if len(canonicalUnitsFor(true)) != len(canonicalUnitsFor(false))+1 {
		t.Fatal("only the hysteria unit may differ between the two sets")
	}
}

func TestBuildUserURIsNoHy2WhenDisabled(t *testing.T) {
	env := map[string]string{
		"MODE": "cloudflare", "NODE_ID": "OR-001", "PUBLIC_IP": "1.2.3.4",
		"HY2_HOST": "h.example", "HY2_PORT": "5331", "HY2_OBFS_PW": "o", "HY2_ENABLED": "0",
	}
	lines := buildUserURIs("kulinh", "uuid", "d.example", "pw", env, nil)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "vless://") {
		t.Fatalf("expected only the VLESS line, got %v", lines)
	}
	env["HY2_ENABLED"] = "1"
	if lines := buildUserURIs("kulinh", "uuid", "d.example", "pw", env, nil); len(lines) != 2 {
		t.Fatalf("expected VLESS + HY2 lines when enabled, got %v", lines)
	}
}

func TestRunReconcileUnitsRetiresHysteriaWhenDisabled(t *testing.T) {
	withTempPaths(t)
	dir := withUnitDirSeeded(t) // seeded while HY2 is (by default) enabled: hysteria unit present
	if err := state.SaveAtomic(envFilePath, map[string]string{"HY2_ENABLED": "0"}, 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &installRecorder{}
	var out bytes.Buffer
	if err := RunReconcileUnits(context.Background(), rec, &out); err != nil {
		t.Fatalf("RunReconcileUnits: %v", err)
	}
	calls := recorderCalls(rec)
	if !strings.Contains(calls, "systemctl disable --now cfvpn-hysteria.service") {
		t.Fatalf("expected disable --now, got:\n%s", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "cfvpn-hysteria.service")); !os.IsNotExist(err) {
		t.Fatalf("unit file must be removed, stat err=%v", err)
	}
	// Second run: nothing left to do, no systemctl calls.
	rec2 := &installRecorder{}
	if err := RunReconcileUnits(context.Background(), rec2, &out); err != nil {
		t.Fatal(err)
	}
	if len(rec2.calls) != 0 {
		t.Fatalf("second run must be a no-op, got:\n%s", recorderCalls(rec2))
	}
}

func TestDirectURIUsesPublicIP(t *testing.T) {
	env := map[string]string{
		"MODE": "direct", "NODE_ID": "SIN-01", "PUBLIC_IP": "96.9.231.74", "HY2_ENABLED": "0",
		"REALITY_PUBLIC_KEY": "pbk", "REALITY_SHORT_ID": "sid", "REALITY_SNI": "www.singaporeair.com",
	}
	lines := buildUserURIs("kulinh", "uuid", "assets-b7e69185.rwl.one", "", env, nil)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "vless://uuid@96.9.231.74:443?") {
		t.Fatalf("got %v", lines)
	}
	delete(env, "PUBLIC_IP")
	lines = buildUserURIs("kulinh", "uuid", "assets-b7e69185.rwl.one", "", env, nil)
	if !strings.HasPrefix(lines[0], "vless://uuid@assets-b7e69185.rwl.one:443?") {
		t.Fatalf("without PUBLIC_IP the domain must be used, got %v", lines)
	}
}
