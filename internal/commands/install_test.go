package commands

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeInstallCF struct {
	zones          map[string]string
	tunnelID       string
	creds          []byte
	createCalls    int
	lastTunnelName string
	upserts        [][3]string
	getZoneCalls   int
}

func (f *fakeInstallCF) GetZoneID(_ context.Context, domain string) (string, error) {
	f.getZoneCalls++
	if z, ok := f.zones[domain]; ok {
		return z, nil
	}
	return "", errors.New("zone not found")
}

func (f *fakeInstallCF) CreateTunnel(_ context.Context, name string) (string, []byte, error) {
	f.createCalls++
	f.lastTunnelName = name
	return f.tunnelID, f.creds, nil
}

func (f *fakeInstallCF) UpsertCNAME(_ context.Context, z, n, target string) error {
	f.upserts = append(f.upserts, [3]string{z, n, target})
	return nil
}

// installRecorder records every Run call made against both the binary
// runner and the systemd runner.
type installRecorder struct {
	calls [][]string
}

func (r *installRecorder) Run(_ context.Context, name string, args ...string) error {
	inv := append([]string{name}, args...)
	r.calls = append(r.calls, inv)
	return nil
}

func TestInstallRequiresDomain(t *testing.T) {
	var out, errBuf bytes.Buffer
	cfg := InstallInputs{CFAPIToken: "t", CFAccountID: "a", Domain: ""}
	if err := RunInstall(context.Background(), cfg, InstallDeps{CF: &fakeInstallCF{}}, &out, &errBuf); err == nil {
		t.Fatalf("expected error when domain empty")
	}
}

func TestInstallRequiresToken(t *testing.T) {
	var out, errBuf bytes.Buffer
	cfg := InstallInputs{CFAPIToken: "", CFAccountID: "a", Domain: "vpn.example.com"}
	if err := RunInstall(context.Background(), cfg, InstallDeps{CF: &fakeInstallCF{}}, &out, &errBuf); err == nil {
		t.Fatalf("expected error when token empty")
	}
}

func TestRunInstall(t *testing.T) {
	dir := t.TempDir()
	oldEnv, oldCred, oldCfg, oldXray, oldSub, oldSysd := envFilePath, cloudflaredCredDir, cloudflaredConfig, xrayConfigPath, subscriptionDir, systemdUnitDir
	envFilePath = filepath.Join(dir, "cfvpn.env")
	cloudflaredCredDir = filepath.Join(dir, "creds")
	cloudflaredConfig = filepath.Join(dir, "cloudflared.yml")
	xrayConfigPath = filepath.Join(dir, "xray.json")
	subscriptionDir = filepath.Join(dir, "subs")
	systemdUnitDir = filepath.Join(dir, "units")
	t.Cleanup(func() {
		envFilePath, cloudflaredCredDir, cloudflaredConfig, xrayConfigPath, subscriptionDir, systemdUnitDir = oldEnv, oldCred, oldCfg, oldXray, oldSub, oldSysd
	})

	cf := &fakeInstallCF{
		zones:    map[string]string{"vpn.example.com": "zone-1"},
		tunnelID: "tun-abc",
		creds:    []byte(`{"k":"v"}`),
	}
	binRunner := &installRecorder{}
	sysRunner := &installRecorder{}

	in := InstallInputs{
		CFAPIToken:  "cf-token",
		CFAccountID: "cf-acct",
		Domain:      "vpn.example.com",
		// User1Name empty on purpose — should default to "user1".
	}
	deps := InstallDeps{
		CF:            cf,
		BinaryRunner:  binRunner,
		SystemdRunner: sysRunner,
	}

	var out, errBuf bytes.Buffer
	if err := RunInstall(context.Background(), in, deps, &out, &errBuf); err != nil {
		t.Fatalf("RunInstall: %v", err)
	}

	// Fake CF: CreateTunnel + GetZoneID + UpsertCNAME all called once.
	if cf.createCalls != 1 {
		t.Fatalf("expected 1 CreateTunnel call, got %d", cf.createCalls)
	}
	if cf.lastTunnelName != "cfvpn-vpn.example.com" {
		t.Fatalf("tunnel name = %q, want cfvpn-vpn.example.com", cf.lastTunnelName)
	}
	if cf.getZoneCalls != 1 {
		t.Fatalf("expected 1 GetZoneID call, got %d", cf.getZoneCalls)
	}
	if len(cf.upserts) != 1 {
		t.Fatalf("expected 1 UpsertCNAME call, got %d", len(cf.upserts))
	}
	if cf.upserts[0] != [3]string{"zone-1", "vpn.example.com", "tun-abc.cfargotunnel.com"} {
		t.Fatalf("upsert payload = %#v, want {zone-1, vpn.example.com, tun-abc.cfargotunnel.com}", cf.upserts[0])
	}

	// Files on disk.
	credPath := filepath.Join(cloudflaredCredDir, "tun-abc.json")
	if _, err := os.Stat(credPath); err != nil {
		t.Fatalf("creds file missing at %s: %v", credPath, err)
	}
	if _, err := os.Stat(xrayConfigPath); err != nil {
		t.Fatalf("xray config missing: %v", err)
	}
	if _, err := os.Stat(cloudflaredConfig); err != nil {
		t.Fatalf("cloudflared config missing: %v", err)
	}
	for _, unit := range []string{"cfvpn-xray.service", "cfvpn-cloudflared.service"} {
		p := filepath.Join(systemdUnitDir, unit)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("systemd unit %s missing: %v", unit, err)
		}
	}

	// Env content.
	envRaw, err := os.ReadFile(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	envS := string(envRaw)
	for _, want := range []string{
		"DOMAIN=vpn.example.com",
		"TUNNEL_UUID=tun-abc",
		"UUID_USER1=",
		"TROJAN_PASS_USER1=",
		"USER1_NAME=user1",
		"CF_API_TOKEN=cf-token",
		"CF_ACCOUNT_ID=cf-acct",
	} {
		if !strings.Contains(envS, want) {
			t.Fatalf("env file missing %q; full file:\n%s", want, envS)
		}
	}

	// Systemd runner: daemon-reload, enable --now xray, enable --now cloudflared.
	if len(sysRunner.calls) != 3 {
		t.Fatalf("expected 3 systemd calls, got %d: %#v", len(sysRunner.calls), sysRunner.calls)
	}
	expect := [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "--now", "cfvpn-xray.service"},
		{"systemctl", "enable", "--now", "cfvpn-cloudflared.service"},
	}
	for i, want := range expect {
		got := strings.Join(sysRunner.calls[i], " ")
		wantS := strings.Join(want, " ")
		if got != wantS {
			t.Fatalf("sysRunner call %d = %q, want %q", i, got, wantS)
		}
	}

	// Stdout contains the subscription payload (base64). The probe will
	// fail because vpn.example.com does not resolve in the test env; we
	// ensure RunInstall still returned nil and the subscription was
	// printed.
	stdoutStr := out.String()
	if !strings.Contains(stdoutStr, "ensuring binaries...") {
		t.Fatalf("stdout missing 'ensuring binaries...': %q", stdoutStr)
	}
	if !strings.Contains(stdoutStr, "creating tunnel...") {
		t.Fatalf("stdout missing 'creating tunnel...': %q", stdoutStr)
	}
	// The final line is the base64 subscription; decode it to be sure.
	lines := strings.Split(strings.TrimRight(stdoutStr, "\n"), "\n")
	subLine := lines[len(lines)-1]
	dec, err := base64.StdEncoding.DecodeString(subLine)
	if err != nil {
		t.Fatalf("final stdout line is not base64 (%q): %v", subLine, err)
	}
	if !strings.Contains(string(dec), "vless://") || !strings.Contains(string(dec), "trojan://") {
		t.Fatalf("decoded subscription lacks vless+trojan URIs: %q", dec)
	}

	// Binary runner: EnsureXray/EnsureCloudflared may have been called 0..2
	// times depending on whether `xray` / `cloudflared` exist on the test
	// host. Just assert the recorder was handed off (no panic on nil).
	_ = binRunner.calls
}
