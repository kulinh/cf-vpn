package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/xray"
)

type stubRunner struct{ calls int }

func (s *stubRunner) Run(_ context.Context, _ string, _ ...string) error {
	s.calls++
	return nil
}

func withTempPaths(t *testing.T) (cfgPath, subDir string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "config.json")
	subDir = filepath.Join(dir, "subs")
	oldC, oldS, oldH, oldE := xrayConfigPath, subscriptionDir, hysteriaConfigPath, envFilePath
	xrayConfigPath = cfgPath
	subscriptionDir = subDir
	hysteriaConfigPath = filepath.Join(dir, "hy-cfg.yaml")
	// Redirect the env file too: without this the tests read the real
	// /etc/cfvpn/cfvpn.env of whatever machine runs them, so their result
	// depends on that node's MODE and Reality params.
	envFilePath = filepath.Join(dir, "cfvpn.env")
	// Write a minimal hysteria config with alice so ListUsers/SetUsers work.
	if err := os.WriteFile(hysteriaConfigPath, []byte("listen: :20515\ntls:\n  cert: cert.pem\n  key: key.pem\nobfs:\n  type: salamander\n  salamander:\n    password: pw\nbandwidth:\n  up: 100mbps\n  down: 100mbps\nauth:\n  type: userpass\n  userpass:\n    alice: alice-hy2pw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		xrayConfigPath = oldC
		subscriptionDir = oldS
		hysteriaConfigPath = oldH
		envFilePath = oldE
	})
	return
}

// writeTestEnv writes the node env file the commands under test read.
func writeTestEnv(t *testing.T, values map[string]string) {
	t.Helper()
	if err := state.SaveAtomic(envFilePath, values, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAddUserRejectsInvalidName(t *testing.T) {
	if err := ValidateAddUserInput("bad name"); err == nil {
		t.Fatalf("expected invalid name error")
	}
	if err := ValidateAddUserInput("alice"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunAddUserHappyPath(t *testing.T) {
	cfgPath, subDir := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	r := &stubRunner{}
	var out, errBuf bytes.Buffer
	err := RunAddUser(context.Background(), UserInputs{Name: "alice", Domain: "vpn.example.com"}, r, &out, &errBuf)
	if err != nil {
		t.Fatal(err)
	}

	loaded, _ := xray.Load(cfgPath)
	names := xray.ListUserNames(loaded)
	if len(names) != 2 {
		t.Fatalf("expected 2 users, got %d", len(names))
	}

	if _, err := os.Stat(filepath.Join(subDir, "alice.txt")); err != nil {
		t.Fatalf("subscription file missing: %v", err)
	}
	if r.calls != 1 {
		t.Fatalf("expected 1 restart, got %d", r.calls)
	}
	if out.Len() == 0 {
		t.Fatalf("expected subscription printed on stdout")
	}
}

func TestRunAddUserRejectsAtMax(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	for _, n := range []string{"b", "c", "d", "e"} {
		if err := xray.AddUser(&cfg, n, "u-"+n, "p-"+n); err != nil {
			t.Fatal(err)
		}
	}
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	err := RunAddUser(context.Background(), UserInputs{Name: "f", Domain: "d.com"}, &stubRunner{}, &out, &errBuf)
	if err == nil {
		t.Fatalf("expected max-users error")
	}
}

func TestRunAddUserRejectsDuplicate(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	cfg := xray.NewBaseConfig("alice", "u-a", "p-a")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	err := RunAddUser(context.Background(), UserInputs{Name: "alice", Domain: "d.com"}, &stubRunner{}, &out, &errBuf)
	if err == nil {
		t.Fatalf("expected duplicate error")
	}
}

func TestRunRemoveUserDeletesSubscription(t *testing.T) {
	cfgPath, subDir := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	_ = xray.AddUser(&cfg, "alice", "u-a", "p-a")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "alice.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &stubRunner{}
	var out, errBuf bytes.Buffer
	err := RunRemoveUser(context.Background(), UserInputs{Name: "alice"}, r, &out, &errBuf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(subDir, "alice.txt")); !os.IsNotExist(err) {
		t.Fatalf("subscription file should be deleted")
	}
	if r.calls != 2 {
		t.Fatalf("expected 2 restarts (xray + hysteria), got %d", r.calls)
	}
}

func TestRunRemoveUserMissingSubscriptionFileOK(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	_ = xray.AddUser(&cfg, "alice", "u-a", "p-a")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	if err := RunRemoveUser(context.Background(), UserInputs{Name: "alice"}, &stubRunner{}, &out, &errBuf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRemoveUserUnknown(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	err := RunRemoveUser(context.Background(), UserInputs{Name: "nope"}, &stubRunner{}, &out, &errBuf)
	if err == nil {
		t.Fatalf("expected error for unknown user")
	}
}

func TestRunGenSubSingleUser(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	err := RunGenSub(context.Background(), UserInputs{Name: "user1", Domain: "vpn.example.com"}, &out, &errBuf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("expected subscription output")
	}
	got := out.String()
	if !strings.Contains(got, "vless://") {
		t.Fatalf("expected decoded vless output, got %q", got)
	}
	if strings.Contains(got, "trojan://") {
		t.Fatalf("expected no trojan:// in output, got %q", got)
	}
}

func TestRunGenSubAllUsers(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	_ = xray.AddUser(&cfg, "alice", "u-a", "p-a")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	err := RunGenSub(context.Background(), UserInputs{Name: "", Domain: "vpn.example.com"}, &out, &errBuf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("expected subscription output")
	}
	got := out.String()
	if strings.Contains(got, "dmxlc3M6Ly8") {
		t.Fatalf("expected decoded output, got base64-like text %q", got)
	}
	if !strings.Contains(got, "# user1") || !strings.Contains(got, "# alice") {
		t.Fatalf("expected named decoded sections, got %q", got)
	}
	if !strings.Contains(got, "vless://") {
		t.Fatalf("expected decoded vless output, got %q", got)
	}
	if strings.Contains(got, "trojan://") {
		t.Fatalf("expected no trojan:// in output, got %q", got)
	}
}

func TestRunGenSubRequiresDomain(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	err := RunGenSub(context.Background(), UserInputs{Name: "user1", Domain: ""}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected domain-required error")
	}
}

func TestRunGenSubUnknownUser(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	err := RunGenSub(context.Background(), UserInputs{Name: "nope", Domain: "d.com"}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected unknown-user error")
	}
}
