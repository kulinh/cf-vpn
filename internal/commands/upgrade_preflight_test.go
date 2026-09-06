package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kulinh/cf-vpn/internal/state"
)

// setUpgradeEnv rewrites the seeded env with the given overrides.
func setUpgradeEnv(t *testing.T, overrides map[string]string) {
	t.Helper()
	env, err := state.Load(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range overrides {
		if v == "" {
			delete(env, k)
			continue
		}
		env[k] = v
	}
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidateUpgradeIdentity(t *testing.T) {
	base := map[string]string{
		"DOMAIN":            "cdn-a1b2.rwl.one",
		"ADMIN_TUNNEL_UUID": "2f8a1c3e-1111-4222-8333-abcdefabcdef",
		"NODE_ID":           "JPY-04",
	}
	if err := validateUpgradeIdentity(base); err != nil {
		t.Fatalf("valid env rejected: %v", err)
	}

	// Legacy nodes carry TUNNEL_UUID instead of ADMIN_TUNNEL_UUID.
	legacy := map[string]string{
		"DOMAIN":      "cdn-a1b2.rwl.one",
		"TUNNEL_UUID": "2f8a1c3e-1111-4222-8333-abcdefabcdef",
		"ADMIN_HOST":  "jpy-04.rwl247.dev",
	}
	if err := validateUpgradeIdentity(legacy); err != nil {
		t.Fatalf("legacy env rejected: %v", err)
	}

	bad := map[string]struct {
		env  map[string]string
		want string
	}{
		"non-canonical tunnel uuid": {
			env:  map[string]string{"DOMAIN": "cdn-a1b2.rwl.one", "ADMIN_TUNNEL_UUID": "old-tun", "NODE_ID": "JPY-04"},
			want: "ADMIN_TUNNEL_UUID",
		},
		"missing tunnel uuid": {
			env:  map[string]string{"DOMAIN": "cdn-a1b2.rwl.one", "NODE_ID": "JPY-04"},
			want: "ADMIN_TUNNEL_UUID",
		},
		"bad domain": {
			env:  map[string]string{"DOMAIN": "not a hostname", "ADMIN_TUNNEL_UUID": "2f8a1c3e-1111-4222-8333-abcdefabcdef", "NODE_ID": "JPY-04"},
			want: "DOMAIN",
		},
		"odd admin host": {
			env: map[string]string{
				"DOMAIN": "cdn-a1b2.rwl.one", "ADMIN_TUNNEL_UUID": "2f8a1c3e-1111-4222-8333-abcdefabcdef",
				"ADMIN_HOST": "jpy 04.rwl247.dev",
			},
			want: "admin host",
		},
		"no node id and no admin host": {
			env:  map[string]string{"DOMAIN": "cdn-a1b2.rwl.one", "ADMIN_TUNNEL_UUID": "2f8a1c3e-1111-4222-8333-abcdefabcdef"},
			want: "ADMIN_HOST",
		},
	}
	for name, c := range bad {
		err := validateUpgradeIdentity(c.env)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, c.want)
		}
	}
}

// An unusable identity must abort the upgrade BEFORE any side effect: no
// config backup, no binary (re)install, no sysctl tuning, no config write.
// Previously it failed inside RenderCloudflared*, i.e. after all of those.
func TestRunUpgradeAbortsBeforeSideEffectsOnBadTunnelUUID(t *testing.T) {
	dir := withUpgradeSeams(t)
	seedUpgradeConfig(t)
	setUpgradeEnv(t, map[string]string{"TUNNEL_UUID": "", "ADMIN_TUNNEL_UUID": "not-a-uuid"})

	binRunner := &installRecorder{}
	sysRunner := &installRecorder{}
	tuned := false
	oldTune := runTuneCommand
	runTuneCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		tuned = true
		return oldTune(ctx, name, args...)
	}
	t.Cleanup(func() { runTuneCommand = oldTune })

	xrayBefore, _ := os.ReadFile(xrayConfigPath)
	cfdBefore, _ := os.ReadFile(cloudflaredConfig)

	deps := InstallDeps{
		CF: &fakeInstallCF{zones: map[string]string{"example.com": "zone-1", adminHostZone: "admin-zone"}},
		IP: fakeInstallIP{ip: "203.0.113.42"}, Cert: &fakeInstallCert{}, UFW: &fakeUFW{},
		BinaryRunner: binRunner, SystemdRunner: sysRunner,
	}
	var out, errBuf bytes.Buffer
	_, err := RunUpgrade(context.Background(), UpgradeInputs{BackupRoot: dir, Now: func() time.Time { return time.Unix(4242, 0) }}, deps, &out, &errBuf)
	if err == nil {
		t.Fatal("expected the upgrade to abort")
	}
	if !strings.Contains(err.Error(), "ADMIN_TUNNEL_UUID") {
		t.Errorf("err = %v, want it to name ADMIN_TUNNEL_UUID", err)
	}

	if len(binRunner.calls) != 0 {
		t.Errorf("binaries were installed before the pre-flight failed: %#v", binRunner.calls)
	}
	if len(sysRunner.calls) != 0 {
		t.Errorf("services were touched before the pre-flight failed: %#v", sysRunner.calls)
	}
	if tuned {
		t.Error("sysctl tuning ran before the pre-flight failed")
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "cfvpn.backup-*")); len(entries) != 0 {
		t.Errorf("config backup was taken before the pre-flight failed: %v", entries)
	}
	xrayAfter, _ := os.ReadFile(xrayConfigPath)
	cfdAfter, _ := os.ReadFile(cloudflaredConfig)
	if !bytes.Equal(xrayBefore, xrayAfter) || !bytes.Equal(cfdBefore, cfdAfter) {
		t.Error("configs were rewritten before the pre-flight failed")
	}
}

func TestRunUpgradeCheckRejectsBadIdentity(t *testing.T) {
	dir := withUpgradeSeams(t)
	seedUpgradeConfig(t)
	setUpgradeEnv(t, map[string]string{"TUNNEL_UUID": "", "ADMIN_TUNNEL_UUID": "not-a-uuid"})

	deps := InstallDeps{
		CF: &fakeInstallCF{zones: map[string]string{"example.com": "zone-1", adminHostZone: "admin-zone"}},
		IP: fakeInstallIP{ip: "203.0.113.42"},
	}
	var out, errBuf bytes.Buffer
	err := RunUpgradeCheck(context.Background(), UpgradeInputs{BackupRoot: dir}, deps, &out, &errBuf)
	if err == nil {
		t.Fatal("pre-flight check accepted an unusable ADMIN_TUNNEL_UUID")
	}
	if !strings.Contains(err.Error(), "ADMIN_TUNNEL_UUID") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(out.String(), "pre-flight OK") {
		t.Fatalf("check reported OK: %q", out.String())
	}
}
