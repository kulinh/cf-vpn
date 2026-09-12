package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/templates"
)

// certWriter stands in for lego: Issue overwrites the fixed HY2 cert paths,
// which is exactly the side effect a failed rotation has to undo.
type certWriter struct {
	cert, key string
	err       error
}

func (c *certWriter) Issue(_ context.Context, _, certPath, keyPath, _ string) error {
	if err := os.WriteFile(certPath, []byte(c.cert), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, []byte(c.key), 0o600); err != nil {
		return err
	}
	return c.err
}

func (c *certWriter) Renew(_ context.Context, _, _, _, _ string, _ int) error { return nil }

func seedHy2CertPair(t *testing.T, cert, key string) (certPath, keyPath string) {
	t.Helper()
	old := hysteriaCertDir
	hysteriaCertDir = t.TempDir()
	t.Cleanup(func() { hysteriaCertDir = old })
	certPath, keyPath = HysteriaCertPaths()
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, []byte(cert), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// A rotation that fails after the HY2 certificate was reissued must put the old
// pair back: the paths are fixed and hysteria would otherwise serve a cert for
// a host that never went live at its next restart.
func TestRunRotateDirectRestoresHy2CertWhenDNSFails(t *testing.T) {
	withRotateDirectTempPaths(t)
	saveEnv(t, map[string]string{"HY2_PORT": "45321", "HY2_OBFS_PW": "obfs"})
	writeTestHysteriaConfig(t)
	certPath, keyPath := seedHy2CertPair(t, "old-cert", "old-key")

	cf := &fakeRotateDirectCF{upsertErr: errors.New("cloudflare 502")}
	_, err := RunRotateDirect(context.Background(), RotateDirectInputs{
		NewHost:       "vpn.example.com",
		NewZone:       "example.com",
		NewZoneID:     "vpn-zone",
		NewHy2Host:    "hy2-new.example.com",
		NewHy2Zone:    "example.com",
		NewHy2ZoneID:  "hy2-zone",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}},
	}, RotateDirectDeps{
		CF:     cf,
		IP:     fakeIPDetector{ip: "203.0.113.10"},
		Cert:   &certWriter{cert: "new-cert", key: "new-key"},
		Runner: eventRunner{},
	}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected the DNS failure to abort the rotation")
	}
	if got := readTestFile(t, certPath); got != "old-cert" {
		t.Errorf("HY2 cert = %q, want the previous one restored", got)
	}
	if got := readTestFile(t, keyPath); got != "old-key" {
		t.Errorf("HY2 key = %q, want the previous one restored", got)
	}
}

// Same guarantee when the certificate issuance itself fails halfway (lego wrote
// the cert, then died before the key).
func TestRunRotateDirectRestoresHy2CertWhenIssueFails(t *testing.T) {
	withRotateDirectTempPaths(t)
	saveEnv(t, map[string]string{"HY2_PORT": "45321", "HY2_OBFS_PW": "obfs"})
	writeTestHysteriaConfig(t)
	certPath, keyPath := seedHy2CertPair(t, "old-cert", "old-key")

	_, err := RunRotateDirect(context.Background(), RotateDirectInputs{
		NewHost:       "vpn.example.com",
		NewZone:       "example.com",
		NewZoneID:     "vpn-zone",
		NewHy2Host:    "hy2-new.example.com",
		NewHy2Zone:    "example.com",
		NewHy2ZoneID:  "hy2-zone",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}},
	}, RotateDirectDeps{
		CF:     &fakeRotateDirectCF{},
		IP:     fakeIPDetector{ip: "203.0.113.10"},
		Cert:   &certWriter{cert: "half-written", key: "half-written", err: errors.New("lego exit 1")},
		Runner: eventRunner{},
	}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected the cert failure to abort the rotation")
	}
	if got := readTestFile(t, certPath); got != "old-cert" {
		t.Errorf("HY2 cert = %q, want the previous one restored", got)
	}
	if got := readTestFile(t, keyPath); got != "old-key" {
		t.Errorf("HY2 key = %q, want the previous one restored", got)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// failingIP makes install abort right after the tunnel step.
type failingIP struct{}

func (failingIP) Detect(context.Context) (string, error) { return "", errors.New("no route to metadata") }

// The cleanup hint deletes a tunnel and its credentials. When the tunnel was
// REUSED (an existing node), printing that hint invites the operator to destroy
// a live node.
func TestRunInstallNeverSuggestsCleanupForAReusedTunnel(t *testing.T) {
	withInstallSeams(t)
	const existing = "56dae1fa-99f1-41e2-a429-19072f6abb69"
	if err := os.MkdirAll(cloudflaredCredDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloudflaredCredDir, existing+".json"), []byte(`{"kept":"creds"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-id", adminHostZone: "admin-zone"}}
	deps := baseInstallDeps(cf)
	deps.IP = failingIP{}
	var stdout bytes.Buffer
	err := RunInstall(context.Background(), InstallInputs{
		CFAPIToken: "cf-token", CFAccountID: "cf-acct", Domain: "vpn.example.com",
		NodeID: "OR-001", User1Name: "alice", Mode: "cloudflare", AdminTunnelUUID: existing,
	}, deps, &stdout, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected the install to fail at IP detection")
	}
	out := stdout.String()
	if strings.Contains(out, "--cleanup") {
		t.Errorf("a reused tunnel must never be offered for cleanup:\n%s", out)
	}
	if !strings.Contains(out, "REUSED") {
		t.Errorf("output should say the tunnel was reused:\n%s", out)
	}
}

// xhttp enable writes cfvpn.env only after the restarts succeed: the env is what
// gen-sub reads, and advertising an XHTTP route that xray is not serving hands
// clients a dead link.
func TestRunXHTTPSetKeepsEnvAndConfigWhenRestartFails(t *testing.T) {
	withRotateCloudflareTempPaths(t)
	env := map[string]string{
		state.KeyMode: "cloudflare", state.KeyDomain: "edge.example.com",
		state.KeyAdminHost: "node.rwl247.dev", state.KeyAdminTunnelUUID: "11111111-2222-3333-4444-555555555555",
	}
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		t.Fatal(err)
	}
	rendered, err := templates.RenderXrayCloudflareOpts(
		[]templates.XrayUser{{Name: "kulinh", UUID: "11111111-1111-1111-1111-111111111111"}},
		"edge.example.com", nil, templates.XrayCloudflareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xrayConfigPath, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cloudflaredConfig, []byte("tunnel: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := &installRecorder{fail: "restart cfvpn-xray.service"}
	if err := RunXHTTPSet(context.Background(), true, rec, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected the restart failure to surface")
	}
	after, err := state.Load(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if after[state.KeyXHTTPEnabled] == "1" {
		t.Error("XHTTP_ENABLED must not be persisted when the restart failed")
	}
	if got := readTestFile(t, xrayConfigPath); got != rendered {
		t.Error("the previous xray config must be restored")
	}
}
