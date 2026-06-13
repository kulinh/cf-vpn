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

// withUnitDir points systemdUnitDir at a temp dir for the test's duration.
func withUnitDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := systemdUnitDir
	systemdUnitDir = dir
	t.Cleanup(func() { systemdUnitDir = old })
	return dir
}

func seedCanonicalUnits(t *testing.T, dir string) {
	t.Helper()
	for name, content := range canonicalUnits() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func recorderCalls(rec *installRecorder) string {
	var b strings.Builder
	for _, c := range rec.calls {
		b.WriteString(strings.Join(c, " "))
		b.WriteString("\n")
	}
	return b.String()
}

func TestReconcileUnitsRewritesDriftedUnit(t *testing.T) {
	dir := withUnitDir(t)
	seedCanonicalUnits(t, dir)
	// Reproduce the real-world drift: cert-renew pointing at the old binary name.
	stale := "[Service]\nExecStart=/usr/local/bin/cfvpn cert-renew\n"
	if err := os.WriteFile(filepath.Join(dir, "cfvpn-cert-renew.service"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := reconcileUnits()
	if err != nil {
		t.Fatalf("reconcileUnits: %v", err)
	}
	if len(changed) != 1 || changed[0] != "cfvpn-cert-renew.service" {
		t.Fatalf("changed = %v, want [cfvpn-cert-renew.service]", changed)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "cfvpn-cert-renew.service"))
	if !strings.Contains(string(got), "/usr/local/bin/cfvpnctl cert-renew") {
		t.Fatalf("cert-renew not fixed: %s", got)
	}
}

func TestReconcileUnitsIdempotentWhenInSync(t *testing.T) {
	dir := withUnitDir(t)
	seedCanonicalUnits(t, dir)
	changed, err := reconcileUnits()
	if err != nil {
		t.Fatalf("reconcileUnits: %v", err)
	}
	if len(changed) != 0 {
		t.Fatalf("changed = %v, want none (already in sync)", changed)
	}
}

func TestRunReconcileUnitsRestartsLongRunningNotOneshot(t *testing.T) {
	dir := withUnitDir(t)
	seedCanonicalUnits(t, dir)
	// Drift one long-running service, one oneshot service, one timer.
	mustWrite(t, filepath.Join(dir, "cfvpn-xray.service"), "stale\n")
	mustWrite(t, filepath.Join(dir, "cfvpn-cert-renew.service"), "stale\n")
	mustWrite(t, filepath.Join(dir, "cfvpn-cert-renew.timer"), "stale\n")

	rec := &installRecorder{}
	var out bytes.Buffer
	if err := RunReconcileUnits(context.Background(), rec, &out); err != nil {
		t.Fatalf("RunReconcileUnits: %v", err)
	}
	calls := recorderCalls(rec)
	if !strings.Contains(calls, "systemctl daemon-reload") {
		t.Fatalf("expected daemon-reload, calls:\n%s", calls)
	}
	if !strings.Contains(calls, "systemctl restart cfvpn-xray.service") {
		t.Fatalf("expected xray restart, calls:\n%s", calls)
	}
	if strings.Contains(calls, "restart cfvpn-cert-renew.service") {
		t.Fatalf("oneshot cert-renew must NOT be restarted, calls:\n%s", calls)
	}
	if !strings.Contains(calls, "systemctl enable --now cfvpn-cert-renew.timer") {
		t.Fatalf("expected timer enable --now, calls:\n%s", calls)
	}
}

func TestRunReconcileUnitsNoDriftSkipsReload(t *testing.T) {
	withUnitDirSeeded(t)
	rec := &installRecorder{}
	var out bytes.Buffer
	if err := RunReconcileUnits(context.Background(), rec, &out); err != nil {
		t.Fatalf("RunReconcileUnits: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("expected no systemctl calls when in sync, got:\n%s", recorderCalls(rec))
	}
}

// Feature A: an in-place re-deploy (same mode) must self-heal drifted units.
func TestReRenderInPlaceReconcilesDriftedUnit(t *testing.T) {
	withUpgradeSeams(t)
	seedUpgradeConfig(t)
	env, err := state.Load(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	env["MODE"] = "direct"
	env[state.KeyRealityPriv] = "test-priv-x25519"
	env[state.KeyRealityPub] = "test-pub"
	env[state.KeyRealityShortID] = "abcd1234"
	env[state.KeyRealityDest] = "www.microsoft.com:443"
	env[state.KeyRealitySNI] = "www.microsoft.com"
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(systemdUnitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(systemdUnitDir, "cfvpn-cert-renew.service")
	mustWrite(t, stalePath, "[Service]\nExecStart=/usr/local/bin/cfvpn cert-renew\n")

	deps := InstallDeps{SystemdRunner: &installRecorder{}}
	var out, errBuf bytes.Buffer
	if _, err := reRenderInPlace(context.Background(), UpgradeInputs{Mode: "direct"}, deps, env, &out, &errBuf); err != nil {
		t.Fatalf("reRenderInPlace: %v", err)
	}
	got, _ := os.ReadFile(stalePath)
	if !strings.Contains(string(got), "/usr/local/bin/cfvpnctl cert-renew") {
		t.Fatalf("reRenderInPlace did not reconcile drifted unit:\n%s", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func withUnitDirSeeded(t *testing.T) string {
	dir := withUnitDir(t)
	seedCanonicalUnits(t, dir)
	return dir
}
