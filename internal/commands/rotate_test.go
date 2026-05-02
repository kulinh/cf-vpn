package commands

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/xray"
)

type fakeCF struct {
	createCalled int
	lastName     string
	newID        string
	creds        []byte
	zones        map[string]string
	upserts      [][3]string
	deleted      []string
	failCreate   bool
}

func (f *fakeCF) GetZoneID(_ context.Context, d string) (string, error) {
	if z, ok := f.zones[d]; ok {
		return z, nil
	}
	return "", errors.New("zone not found")
}

func (f *fakeCF) CreateTunnel(_ context.Context, name string) (string, []byte, error) {
	f.createCalled++
	f.lastName = name
	if f.failCreate {
		return "", nil, errors.New("boom")
	}
	return f.newID, f.creds, nil
}

func (f *fakeCF) UpsertCNAME(_ context.Context, z, n, t string) error {
	f.upserts = append(f.upserts, [3]string{z, n, t})
	return nil
}

func (f *fakeCF) DeleteTunnel(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func TestRunRotateCleanup(t *testing.T) {
	dir := t.TempDir()
	oldCred := cloudflaredCredDir
	cloudflaredCredDir = dir
	t.Cleanup(func() { cloudflaredCredDir = oldCred })

	credFile := filepath.Join(dir, "old-id.json")
	if err := os.WriteFile(credFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cf := &fakeCF{}
	var out, errBuf bytes.Buffer
	if err := RunRotateCleanup(context.Background(), "old-id", RotateDeps{CF: cf, Runner: &stubRunner{}}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if len(cf.deleted) != 1 || cf.deleted[0] != "old-id" {
		t.Fatalf("expected 1 delete, got %#v", cf.deleted)
	}
	if _, err := os.Stat(credFile); !os.IsNotExist(err) {
		t.Fatal("creds file should be removed")
	}
}

type fakeRotateDirectCF struct {
	upsertA   []struct{ zoneID, name, ip string }
	deleteA   []struct{ zoneID, name string }
	deleteErr map[string]error
	events    *[]string
}

func (f *fakeRotateDirectCF) UpsertARecord(_ context.Context, zoneID, name, ip string) error {
	f.upsertA = append(f.upsertA, struct{ zoneID, name, ip string }{zoneID, name, ip})
	if f.events != nil {
		*f.events = append(*f.events, "upsert:"+zoneID+"/"+name)
	}
	return nil
}

func (f *fakeRotateDirectCF) DeleteARecordByName(_ context.Context, zoneID, name string) error {
	f.deleteA = append(f.deleteA, struct{ zoneID, name string }{zoneID, name})
	if f.events != nil {
		*f.events = append(*f.events, "delete:"+zoneID+"/"+name)
	}
	if f.deleteErr != nil {
		return f.deleteErr[zoneID+"/"+name]
	}
	return nil
}

type fakeIPDetector struct{ ip string }

func (f fakeIPDetector) Detect(context.Context) (string, error) { return f.ip, nil }

type failingRunner struct{}

func (f failingRunner) Run(context.Context, string, ...string) error {
	return errors.New("reload failed")
}

type eventRunner struct{ events *[]string }

func (r eventRunner) Run(_ context.Context, name string, args ...string) error {
	if r.events != nil {
		*r.events = append(*r.events, "run:"+strings.Join(append([]string{name}, args...), " "))
	}
	return nil
}

type fakeCertManager struct {
	cert, key        string
	ensureCalled     int
	populateOnEnsure bool
}

func (f *fakeCertManager) Issue(_ context.Context, host, certPath, keyPath, token string) error {
	f.ensureCalled++
	if f.populateOnEnsure {
		f.cert = certPath
		f.key = keyPath
	}
	return nil
}
func (f *fakeCertManager) Renew(_ context.Context, host, certPath, keyPath, token string, days int) error {
	f.ensureCalled++
	return nil
}

func withRotateDirectTempPaths(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldEnv, oldXray, oldSub, oldHy := envFilePath, xrayConfigPath, subscriptionDir, hysteriaConfigPath
	envFilePath = filepath.Join(dir, "cfvpn.env")
	xrayConfigPath = filepath.Join(dir, "xray.json")
	subscriptionDir = filepath.Join(dir, "subs")
	hysteriaConfigPath = filepath.Join(dir, "hysteria.yaml")
	t.Cleanup(func() {
		envFilePath, xrayConfigPath, subscriptionDir, hysteriaConfigPath = oldEnv, oldXray, oldSub, oldHy
	})
	if err := state.SaveAtomic(envFilePath, realityRotateEnv(), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// realityRotateEnv returns a base env with Reality params populated. Required
// since RunRotateDirect refuses to run without Reality (legacy WS+TLS removed).
func realityRotateEnv() map[string]string {
	return map[string]string{
		state.KeyRealityPriv:    "test-priv-x25519",
		state.KeyRealityPub:     "test-pub-x25519",
		state.KeyRealityShortID: "abcd1234",
		state.KeyRealityDest:    "www.microsoft.com:443",
		state.KeyRealitySNI:     "www.microsoft.com",
	}
}

// saveEnv merges extra keys into a Reality-seeded env and writes it.
func saveEnv(t *testing.T, extra map[string]string) {
	t.Helper()
	env := realityRotateEnv()
	for k, v := range extra {
		env[k] = v
	}
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeTestHysteriaConfig writes a minimal but valid hysteria config to hysteriaConfigPath.
func writeTestHysteriaConfig(t *testing.T) {
	t.Helper()
	content := `listen: ":45321"
tls:
  cert: "/etc/cfvpn/hysteria/cert.pem"
  key: "/etc/cfvpn/hysteria/key.pem"
obfs:
  type: salamander
  salamander:
    password: "obfs-pw-test"
bandwidth:
  up: 100mbps
  down: 100mbps
auth:
  type: userpass
  userpass:
    "alice": "pass-alice"
    "bob": "pass-bob"
`
	if err := os.WriteFile(hysteriaConfigPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

type trackingCertManager struct {
	mu    sync.Mutex
	hosts []string
	errs  map[string]error
}

func (m *trackingCertManager) Issue(_ context.Context, host, _, _, _ string) error {
	m.mu.Lock()
	m.hosts = append(m.hosts, host)
	m.mu.Unlock()
	if m.errs != nil {
		if e, ok := m.errs[host]; ok {
			return e
		}
	}
	return nil
}

func (m *trackingCertManager) Renew(_ context.Context, _, _, _, _ string, _ int) error { return nil }

func TestRunRotateDirectHappyPath(t *testing.T) {
	withRotateDirectTempPaths(t)
	saveEnv(t, map[string]string{"DOMAIN": "old.example.com"})
	cf := &fakeRotateDirectCF{}
	runner := &recordingRunner{}
	var out, errBuf bytes.Buffer
	res, err := RunRotateDirect(context.Background(), RotateDirectInputs{
		NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "new-zone", OldHost: "old.example.com", OldZoneID: "old-zone", CFAPIToken: "tok",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}},
	}, RotateDirectDeps{CF: cf, IP: fakeIPDetector{ip: "203.0.113.10"}, Cert: &fakeCertManager{cert: "/c", key: "/k"}, Runner: runner}, &out, &errBuf)
	if err != nil {
		t.Fatal(err)
	}
	if res.VpnHost != "vpn.example.com" || res.PublicIP != "203.0.113.10" {
		t.Fatalf("unexpected result: %#v", res)
	}
	if len(cf.upsertA) != 1 || cf.upsertA[0].zoneID != "new-zone" || cf.upsertA[0].name != "vpn.example.com" || cf.upsertA[0].ip != "203.0.113.10" {
		t.Fatalf("unexpected upsert: %#v", cf.upsertA)
	}
	if len(cf.deleteA) != 1 || cf.deleteA[0].zoneID != "old-zone" || cf.deleteA[0].name != "old.example.com" {
		t.Fatalf("unexpected deletes: %#v", cf.deleteA)
	}
	if len(runner.calls) != 1 || runner.calls[0][0] != "systemctl" || strings.Join(runner.calls[0][1:], " ") != "restart cfvpn-xray.service" {
		t.Fatalf("unexpected runner calls: %#v", runner.calls)
	}
	raw, err := os.ReadFile(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DOMAIN=vpn.example.com", "PUBLIC_IP=203.0.113.10", "MODE=direct"} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatalf("env missing %s: %s", want, raw)
		}
	}
}

// TestRunRotateDirectSkipsVpnHostCertOnReality verifies Reality nodes do not
// issue an LE cert for the VPN host on rotate (Reality camouflages TLS via
// dest=, no public cert needed). Only HY2 gets a real cert when present.
func TestRunRotateDirectSkipsVpnHostCertOnReality(t *testing.T) {
	withRotateDirectTempPaths(t)
	cert := &fakeCertManager{populateOnEnsure: true}
	_, err := RunRotateDirect(context.Background(), RotateDirectInputs{NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "z", ExistingUsers: []ExistingUser{{Name: "alice", UUID: "u"}}}, RotateDirectDeps{CF: &fakeRotateDirectCF{}, IP: fakeIPDetector{ip: "203.0.113.10"}, Cert: cert, Runner: &recordingRunner{}}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cert.ensureCalled != 0 {
		t.Fatalf("expected no cert.Issue calls (Reality + no HY2 host), got %d", cert.ensureCalled)
	}
}

func TestRunRotateDirectXrayBackupReadFailureDoesNotCreateARecord(t *testing.T) {
	dir := withRotateDirectTempPaths(t)
	blocker := filepath.Join(dir, "not-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	xrayConfigPath = filepath.Join(blocker, "xray.json")
	cf := &fakeRotateDirectCF{}
	_, err := RunRotateDirect(context.Background(), RotateDirectInputs{NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "z", ExistingUsers: []ExistingUser{{Name: "alice", UUID: "u"}}}, RotateDirectDeps{CF: cf, IP: fakeIPDetector{ip: "203.0.113.10"}, Cert: &fakeCertManager{cert: "/c", key: "/k"}, Runner: &recordingRunner{}}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected backup read failure")
	}
	if len(cf.upsertA) != 0 || len(cf.deleteA) != 0 {
		t.Fatalf("expected no DNS mutation before backup read succeeds, got upserts %#v deletes %#v", cf.upsertA, cf.deleteA)
	}
}

func TestRunRotateDirectIgnoresOldADeleteFailure(t *testing.T) {
	withRotateDirectTempPaths(t)
	cf := &fakeRotateDirectCF{deleteErr: map[string]error{"old-zone/old.example.com": errors.New("delete failed")}}
	var errBuf bytes.Buffer
	_, err := RunRotateDirect(context.Background(), RotateDirectInputs{NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "z", OldHost: "old.example.com", OldZoneID: "old-zone", ExistingUsers: []ExistingUser{{Name: "alice", UUID: "u"}}}, RotateDirectDeps{CF: cf, IP: fakeIPDetector{ip: "203.0.113.10"}, Cert: &fakeCertManager{cert: "/c", key: "/k"}, Runner: &recordingRunner{}}, &bytes.Buffer{}, &errBuf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "warning") {
		t.Fatalf("expected warning, got %q", errBuf.String())
	}
}

func TestRunRotateDirectSubscriptionFailureKeepsNewAAndOldA(t *testing.T) {
	dir := withRotateDirectTempPaths(t)
	blocker := filepath.Join(dir, "subs-parent-is-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	subscriptionDir = filepath.Join(blocker, "subs")
	cfg := xray.NewBaseConfig("alice", "uuid-a", "pass-a")
	if err := xray.SaveAtomic(xrayConfigPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	cf := &fakeRotateDirectCF{}
	_, err := RunRotateDirect(context.Background(), RotateDirectInputs{NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "new-zone", OldHost: "old.example.com", OldZoneID: "old-zone", ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}}}, RotateDirectDeps{CF: cf, IP: fakeIPDetector{ip: "203.0.113.10"}, Cert: &fakeCertManager{cert: "/c", key: "/k"}, Runner: &recordingRunner{}}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected subscription regeneration failure")
	}
	if len(cf.deleteA) != 0 {
		t.Fatalf("expected no DNS deletes after reload/env save subscription failure, got %#v", cf.deleteA)
	}
}

func TestRunRotateDirectEventOrderDeletesOldAfterReload(t *testing.T) {
	withRotateDirectTempPaths(t)
	var events []string
	cf := &fakeRotateDirectCF{events: &events}
	_, err := RunRotateDirect(context.Background(), RotateDirectInputs{NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "new-zone", OldHost: "old.example.com", OldZoneID: "old-zone", ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}}}, RotateDirectDeps{CF: cf, IP: fakeIPDetector{ip: "203.0.113.10"}, Cert: &fakeCertManager{cert: "/c", key: "/k"}, Runner: eventRunner{events: &events}}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"upsert:new-zone/vpn.example.com", "run:systemctl restart cfvpn-xray.service", "delete:old-zone/old.example.com"}
	if strings.Join(events, "|") != strings.Join(want, "|") {
		t.Fatalf("unexpected event order: got %#v want %#v", events, want)
	}
}

func TestRunRotateDirectRestartFailureRestoresXrayAndRollsBackARecord(t *testing.T) {
	withRotateDirectTempPaths(t)
	old := []byte(`{"old":true}`)
	if err := os.WriteFile(xrayConfigPath, old, 0o600); err != nil {
		t.Fatal(err)
	}
	cf := &fakeRotateDirectCF{}
	_, err := RunRotateDirect(context.Background(), RotateDirectInputs{NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "z", ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}}}, RotateDirectDeps{CF: cf, IP: fakeIPDetector{ip: " 203.0.113.10 "}, Cert: &fakeCertManager{cert: "/c", key: "/k"}, Runner: failingRunner{}}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected reload failure")
	}
	got, readErr := os.ReadFile(xrayConfigPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, old) {
		t.Fatalf("xray config not restored: got %s want %s", got, old)
	}
	if len(cf.deleteA) != 1 || cf.deleteA[0].zoneID != "z" || cf.deleteA[0].name != "vpn.example.com" {
		t.Fatalf("expected rollback delete of new A record, got %#v", cf.deleteA)
	}
}

func TestRunRotateDirectRejectsNonIPv4(t *testing.T) {
	withRotateDirectTempPaths(t)
	for _, ip := range []string{"not-an-ip", "2001:db8::1"} {
		_, err := RunRotateDirect(context.Background(), RotateDirectInputs{NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "z", ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}}}, RotateDirectDeps{CF: &fakeRotateDirectCF{}, IP: fakeIPDetector{ip: ip}, Cert: &fakeCertManager{cert: "/c", key: "/k"}, Runner: &recordingRunner{}}, nil, &bytes.Buffer{})
		if err == nil {
			t.Fatalf("expected error for %q", ip)
		}
	}
}

// ---------------------------------------------------------------------------
// HY2 rotation tests
// ---------------------------------------------------------------------------

func TestRunRotateDirectHy2RotatesHostCertDNSService(t *testing.T) {
	withRotateDirectTempPaths(t)
	saveEnv(t, map[string]string{
		"HY2_PORT":    "45321",
		"HY2_OBFS_PW": "obfs-test-pw",
	})
	writeTestHysteriaConfig(t)

	cf := &fakeRotateDirectCF{}
	cm := &trackingCertManager{}
	var runnerEvents []string
	runner := eventRunner{events: &runnerEvents}

	res, err := RunRotateDirect(context.Background(), RotateDirectInputs{
		NewHost:       "vpn.example.com",
		NewZone:       "example.com",
		NewZoneID:     "vpn-zone",
		NewHy2Host:    "hy2.example.com",
		NewHy2Zone:    "example.com",
		NewHy2ZoneID:  "hy2-zone",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}},
	}, RotateDirectDeps{
		CF:     cf,
		IP:     fakeIPDetector{ip: "203.0.113.10"},
		Cert:   cm,
		Runner: runner,
	}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	// On Reality direct nodes, only HY2 needs a real LE cert; the VPN host
	// uses Reality's TLS camouflage (no cert).
	if len(cm.hosts) != 1 {
		t.Fatalf("expected 1 cert.Issue call (HY2 only), got %d: %v", len(cm.hosts), cm.hosts)
	}
	if cm.hosts[0] != "hy2.example.com" {
		t.Errorf("Issue host: got %q want %q", cm.hosts[0], "hy2.example.com")
	}

	// A records upserted: VPN + HY2
	wantUpserts := map[string]bool{"vpn-zone/vpn.example.com": false, "hy2-zone/hy2.example.com": false}
	for _, u := range cf.upsertA {
		key := u.zoneID + "/" + u.name
		if _, ok := wantUpserts[key]; ok {
			wantUpserts[key] = true
		}
		if u.ip != "203.0.113.10" {
			t.Errorf("upsert %q ip: got %q want 203.0.113.10", key, u.ip)
		}
	}
	for k, found := range wantUpserts {
		if !found {
			t.Errorf("A record upsert not called for %q", k)
		}
	}

	// hysteria service restarted
	restartedHy := false
	for _, ev := range runnerEvents {
		if strings.Contains(ev, "cfvpn-hysteria.service") {
			restartedHy = true
		}
	}
	if !restartedHy {
		t.Errorf("cfvpn-hysteria.service not restarted; events: %v", runnerEvents)
	}

	// env updated with HY2_HOST
	envBytes, _ := os.ReadFile(envFilePath)
	if !bytes.Contains(envBytes, []byte("HY2_HOST=hy2.example.com")) {
		t.Errorf("env missing HY2_HOST=hy2.example.com: %s", envBytes)
	}

	// result
	if res.Hy2Host != "hy2.example.com" {
		t.Errorf("result Hy2Host: got %q want hy2.example.com", res.Hy2Host)
	}
	if res.Hy2Port != 45321 {
		t.Errorf("result Hy2Port: got %d want 45321", res.Hy2Port)
	}
	if res.Hy2ObfsPW != "obfs-test-pw" {
		t.Errorf("result Hy2ObfsPW: got %q want obfs-test-pw", res.Hy2ObfsPW)
	}

	// hysteria config rewritten: must contain the fixed HysteriaCertPaths cert path
	// and must preserve the existing users (alice and bob from writeTestHysteriaConfig).
	hyBody, _ := os.ReadFile(hysteriaConfigPath)
	if !bytes.Contains(hyBody, []byte("/etc/cfvpn/hysteria/cert.pem")) {
		t.Errorf("hysteria config missing cert path; got: %s", hyBody)
	}
	if !bytes.Contains(hyBody, []byte("alice")) || !bytes.Contains(hyBody, []byte("bob")) {
		t.Errorf("hysteria config missing preserved users; got: %s", hyBody)
	}
}

func TestRunRotateDirectHy2DeletesOldHy2Record(t *testing.T) {
	withRotateDirectTempPaths(t)
	saveEnv(t, map[string]string{"HY2_PORT": "45321", "HY2_OBFS_PW": "pw"})
	writeTestHysteriaConfig(t)

	cf := &fakeRotateDirectCF{}
	_, err := RunRotateDirect(context.Background(), RotateDirectInputs{
		NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "vpn-zone",
		NewHy2Host: "hy2-new.example.com", NewHy2Zone: "example.com", NewHy2ZoneID: "hy2-zone",
		OldHy2Host: "hy2-old.example.com", OldHy2ZoneID: "hy2-old-zone",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}},
	}, RotateDirectDeps{
		CF: cf, IP: fakeIPDetector{ip: "203.0.113.10"}, Cert: &trackingCertManager{}, Runner: &recordingRunner{},
	}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	// old HY2 A record deleted
	foundOldHy2Delete := false
	for _, d := range cf.deleteA {
		if d.zoneID == "hy2-old-zone" && d.name == "hy2-old.example.com" {
			foundOldHy2Delete = true
		}
	}
	if !foundOldHy2Delete {
		t.Errorf("old HY2 A record not deleted; deletes: %#v", cf.deleteA)
	}
}

func TestRunRotateDirectNoHy2WhenNewHy2HostEmpty(t *testing.T) {
	withRotateDirectTempPaths(t)
	saveEnv(t, map[string]string{"HY2_PORT": "45321", "HY2_OBFS_PW": "pw"})
	cm := &trackingCertManager{}
	cf := &fakeRotateDirectCF{}
	res, err := RunRotateDirect(context.Background(), RotateDirectInputs{
		NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "z",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}},
	}, RotateDirectDeps{
		CF: cf, IP: fakeIPDetector{ip: "203.0.113.10"}, Cert: cm, Runner: &recordingRunner{},
	}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	// On Reality + no HY2 host, no certs are issued (Reality has no public cert).
	if len(cm.hosts) != 0 {
		t.Errorf("unexpected cert.Issue calls: %v", cm.hosts)
	}
	// Only VPN A record upserted (no HY2)
	if len(cf.upsertA) != 1 {
		t.Fatalf("expected 1 A upsert (VPN only, no HY2), got %d: %v", len(cf.upsertA), cf.upsertA)
	}
	// Hy2Host is empty in result
	if res.Hy2Host != "" {
		t.Errorf("expected Hy2Host empty, got %q", res.Hy2Host)
	}
}

func TestRunRotateDirectHy2IgnoresOldHy2DeleteFailure(t *testing.T) {
	withRotateDirectTempPaths(t)
	saveEnv(t, map[string]string{"HY2_PORT": "45321", "HY2_OBFS_PW": "pw"})
	writeTestHysteriaConfig(t)

	cf := &fakeRotateDirectCF{deleteErr: map[string]error{"hy2-old-zone/hy2-old.example.com": errors.New("dns delete failed")}}
	var errBuf bytes.Buffer
	_, err := RunRotateDirect(context.Background(), RotateDirectInputs{
		NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "vpn-zone",
		NewHy2Host: "hy2-new.example.com", NewHy2Zone: "example.com", NewHy2ZoneID: "hy2-zone",
		OldHy2Host: "hy2-old.example.com", OldHy2ZoneID: "hy2-old-zone",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}},
	}, RotateDirectDeps{
		CF: cf, IP: fakeIPDetector{ip: "203.0.113.10"}, Cert: &trackingCertManager{}, Runner: &recordingRunner{},
	}, nil, &errBuf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "warning") {
		t.Errorf("expected warning in stderr, got %q", errBuf.String())
	}
}

func TestRunRotateDirectRegenerateSubscriptionsVLESSOnly(t *testing.T) {
	withRotateDirectTempPaths(t)
	cfg := xray.NewBaseConfig("alice", "uuid-a", "pass-a")
	if err := xray.SaveAtomic(xrayConfigPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := RunRotateDirect(context.Background(), RotateDirectInputs{
		NewHost: "vpn.example.com", NewZone: "example.com", NewZoneID: "z",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}},
	}, RotateDirectDeps{
		CF: &fakeRotateDirectCF{}, IP: fakeIPDetector{ip: "203.0.113.10"}, Cert: &fakeCertManager{}, Runner: &recordingRunner{},
	}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	// RegenerateSubscriptions strips the `@vpn` suffix that templates emit
	// in client emails, so the file name is the bare user name.
	subFile := filepath.Join(subscriptionDir, "alice.txt")
	body, err := os.ReadFile(subFile)
	if err != nil {
		t.Fatal(err)
	}
	// Subscription is base64-encoded; decode to inspect URIs.
	decoded, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if decErr != nil {
		t.Fatalf("subscription file is not valid base64: %v; raw: %s", decErr, body)
	}
	// Must contain vless:// and must NOT contain trojan://
	if !bytes.Contains(decoded, []byte("vless://")) {
		t.Errorf("subscription missing vless URI: %s", decoded)
	}
	if bytes.Contains(decoded, []byte("trojan://")) {
		t.Errorf("subscription contains trojan URI (should be VLESS-only): %s", decoded)
	}
}

// TestRegenerateSubscriptionsCloudflareTemplateStripsVpnSuffix verifies that
// when xray config is produced by the production cloudflare template (which
// emits "<name>@vpn" in the client email), RegenerateSubscriptions strips the
// suffix once so subscription file names and URI fragments use the bare name.
// This guards against the regression where users provisioned via install/rotate
// templates ended up with `alice@vpn.txt` files and `#alice@vpn-HTTPUpgrade`
// fragments while users added later via xray.AddUser produced bare names.
func TestRegenerateSubscriptionsCloudflareTemplateStripsVpnSuffix(t *testing.T) {
	withRotateDirectTempPaths(t)

	xrayJSON, err := templates.RenderXrayCloudflareHTTPUpgrade(
		[]templates.XrayUser{{Name: "alice", UUID: "uuid-a"}},
		"vpn.example.com",
	)
	if err != nil {
		t.Fatalf("render cloudflare httpupgrade: %v", err)
	}
	if err := os.WriteFile(xrayConfigPath, []byte(xrayJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveAtomic(envFilePath, map[string]string{
		state.KeyMode: "cloudflare",
	}, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RegenerateSubscriptions("vpn.example.com"); err != nil {
		t.Fatalf("RegenerateSubscriptions: %v", err)
	}

	if _, err := os.Stat(filepath.Join(subscriptionDir, "alice@vpn.txt")); !os.IsNotExist(err) {
		t.Errorf("subscription file alice@vpn.txt should not exist (err=%v)", err)
	}
	body, err := os.ReadFile(filepath.Join(subscriptionDir, "alice.txt"))
	if err != nil {
		t.Fatalf("read alice.txt: %v", err)
	}
	decoded, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if decErr != nil {
		t.Fatalf("subscription not valid base64: %v; raw: %s", decErr, body)
	}
	if !bytes.Contains(decoded, []byte("vless://")) {
		t.Errorf("subscription missing vless URI: %s", decoded)
	}
	if !bytes.Contains(decoded, []byte("#alice-HTTPUpgrade")) {
		t.Errorf("URI fragment must be #alice-HTTPUpgrade (no @vpn): %s", decoded)
	}
	if bytes.Contains(decoded, []byte("#alice@vpn")) {
		t.Errorf("URI fragment must not contain @vpn: %s", decoded)
	}
}

// TestRegenerateSubscriptionsRealityTemplateStripsVpnSuffix is the direct/Reality
// counterpart: same invariant, exercised through RenderXrayDirectReality which
// also adds flow=xtls-rprx-vision.
func TestRegenerateSubscriptionsRealityTemplateStripsVpnSuffix(t *testing.T) {
	withRotateDirectTempPaths(t)

	xrayJSON, err := templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
		Users:       []templates.XrayUser{{Name: "alice", UUID: "uuid-a"}},
		PrivateKey:  "priv-stub",
		ShortIDs:    []string{"deadbeefdeadbeef"},
		Dest:        "www.microsoft.com:443",
		ServerNames: []string{"www.microsoft.com"},
	})
	if err != nil {
		t.Fatalf("render direct reality: %v", err)
	}
	if err := os.WriteFile(xrayConfigPath, []byte(xrayJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveAtomic(envFilePath, map[string]string{
		state.KeyMode:           "direct",
		state.KeyRealityPub:     "pub-stub",
		state.KeyRealityShortID: "deadbeefdeadbeef",
		state.KeyRealitySNI:     "www.microsoft.com",
		state.KeyRealityDest:    "www.microsoft.com:443",
	}, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RegenerateSubscriptions("vpn.example.com"); err != nil {
		t.Fatalf("RegenerateSubscriptions: %v", err)
	}

	if _, err := os.Stat(filepath.Join(subscriptionDir, "alice@vpn.txt")); !os.IsNotExist(err) {
		t.Errorf("subscription file alice@vpn.txt should not exist (err=%v)", err)
	}
	body, err := os.ReadFile(filepath.Join(subscriptionDir, "alice.txt"))
	if err != nil {
		t.Fatalf("read alice.txt: %v", err)
	}
	decoded, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if decErr != nil {
		t.Fatalf("subscription not valid base64: %v; raw: %s", decErr, body)
	}
	if !bytes.Contains(decoded, []byte("vless://")) {
		t.Errorf("subscription missing vless URI: %s", decoded)
	}
	if !bytes.Contains(decoded, []byte("#alice-Reality")) {
		t.Errorf("URI fragment must be #alice-Reality (no @vpn): %s", decoded)
	}
	if !bytes.Contains(decoded, []byte("pbk=pub-stub")) {
		t.Errorf("URI must include pbk=pub-stub: %s", decoded)
	}
	if !bytes.Contains(decoded, []byte("sid=deadbeefdeadbeef")) {
		t.Errorf("URI must include sid=deadbeefdeadbeef: %s", decoded)
	}
	if bytes.Contains(decoded, []byte("#alice@vpn")) {
		t.Errorf("URI fragment must not contain @vpn: %s", decoded)
	}
}

// ---------------------------------------------------------------------------
// Cloudflare-mode rotation tests
// ---------------------------------------------------------------------------

type fakeRotateCloudflareCF struct {
	upsertCNAME []struct{ zoneID, name, target string }
	deleteCNAME []struct{ zoneID, name string }
	upsertA     []struct{ zoneID, name, ip string }
	deleteA     []struct{ zoneID, name string }
}

func (f *fakeRotateCloudflareCF) UpsertCNAME(_ context.Context, zoneID, name, target string) error {
	f.upsertCNAME = append(f.upsertCNAME, struct{ zoneID, name, target string }{zoneID, name, target})
	return nil
}

func (f *fakeRotateCloudflareCF) DeleteCNAMEByName(_ context.Context, zoneID, name string) error {
	f.deleteCNAME = append(f.deleteCNAME, struct{ zoneID, name string }{zoneID, name})
	return nil
}

func (f *fakeRotateCloudflareCF) UpsertARecord(_ context.Context, zoneID, name, ip string) error {
	f.upsertA = append(f.upsertA, struct{ zoneID, name, ip string }{zoneID, name, ip})
	return nil
}

func (f *fakeRotateCloudflareCF) DeleteARecordByName(_ context.Context, zoneID, name string) error {
	f.deleteA = append(f.deleteA, struct{ zoneID, name string }{zoneID, name})
	return nil
}

// withRotateCloudflareTempPaths is the cloudflare-mode counterpart of
// withRotateDirectTempPaths: it ALSO overrides cloudflaredConfig so the
// rotate flow can write the rendered ingress without touching /etc.
func withRotateCloudflareTempPaths(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldEnv, oldXray, oldSub, oldHy, oldCfd := envFilePath, xrayConfigPath, subscriptionDir, hysteriaConfigPath, cloudflaredConfig
	envFilePath = filepath.Join(dir, "cfvpn.env")
	xrayConfigPath = filepath.Join(dir, "xray.json")
	subscriptionDir = filepath.Join(dir, "subs")
	hysteriaConfigPath = filepath.Join(dir, "hysteria.yaml")
	cloudflaredConfig = filepath.Join(dir, "cloudflared.yml")
	t.Cleanup(func() {
		envFilePath, xrayConfigPath, subscriptionDir, hysteriaConfigPath, cloudflaredConfig = oldEnv, oldXray, oldSub, oldHy, oldCfd
	})
	if err := state.SaveAtomic(envFilePath, map[string]string{}, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunRotateCloudflareHappyPath(t *testing.T) {
	withRotateCloudflareTempPaths(t)
	if err := state.SaveAtomic(envFilePath, map[string]string{
		"DOMAIN":            "old.example.com",
		"MODE":              "cloudflare",
		"ADMIN_TUNNEL_UUID": "tunnel-uuid-1",
		"ADMIN_HOST":        "admin.example.com",
		"PUBLIC_IP":         "203.0.113.10",
		"HY2_PORT":          "45321",
		"HY2_OBFS_PW":       "obfs-test",
	}, 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestHysteriaConfig(t)

	cf := &fakeRotateCloudflareCF{}
	cm := &trackingCertManager{}
	runner := &recordingRunner{}
	var out, errBuf bytes.Buffer

	res, err := RunRotateCloudflare(context.Background(), RotateCloudflareInputs{
		NewHost:       "vpn-new.example.com",
		NewZoneID:     "vpn-zone",
		OldHost:       "old.example.com",
		OldZoneID:     "old-zone",
		NewHy2Host:    "hy2-new.example.com",
		NewHy2ZoneID:  "hy2-zone",
		OldHy2Host:    "hy2-old.example.com",
		OldHy2ZoneID:  "hy2-old-zone",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}},
	}, RotateCloudflareDeps{CF: cf, Cert: cm, Runner: runner}, &out, &errBuf)
	if err != nil {
		t.Fatalf("RunRotateCloudflare: %v", err)
	}

	// Result fields: VpnHost = new host, PublicIP unchanged, Hy2Host = new HY2 host.
	if res.VpnHost != "vpn-new.example.com" {
		t.Errorf("res.VpnHost: got %q want vpn-new.example.com", res.VpnHost)
	}
	if res.PublicIP != "203.0.113.10" {
		t.Errorf("res.PublicIP: got %q want 203.0.113.10 (unchanged)", res.PublicIP)
	}
	if res.Hy2Host != "hy2-new.example.com" {
		t.Errorf("res.Hy2Host: got %q want hy2-new.example.com", res.Hy2Host)
	}

	// CNAME points at <tunnel>.cfargotunnel.com.
	if len(cf.upsertCNAME) != 1 ||
		cf.upsertCNAME[0].zoneID != "vpn-zone" ||
		cf.upsertCNAME[0].name != "vpn-new.example.com" ||
		cf.upsertCNAME[0].target != "tunnel-uuid-1.cfargotunnel.com" {
		t.Fatalf("unexpected CNAME upsert: %#v", cf.upsertCNAME)
	}
	// HY2 A record upserted with PUBLIC_IP from env.
	if len(cf.upsertA) != 1 ||
		cf.upsertA[0].zoneID != "hy2-zone" ||
		cf.upsertA[0].name != "hy2-new.example.com" ||
		cf.upsertA[0].ip != "203.0.113.10" {
		t.Fatalf("unexpected HY2 A upsert: %#v", cf.upsertA)
	}
	// Old VPN CNAME and old HY2 A record cleaned up.
	if len(cf.deleteCNAME) != 1 ||
		cf.deleteCNAME[0].zoneID != "old-zone" ||
		cf.deleteCNAME[0].name != "old.example.com" {
		t.Fatalf("unexpected CNAME delete: %#v", cf.deleteCNAME)
	}
	if len(cf.deleteA) != 1 ||
		cf.deleteA[0].zoneID != "hy2-old-zone" ||
		cf.deleteA[0].name != "hy2-old.example.com" {
		t.Fatalf("unexpected HY2 A delete: %#v", cf.deleteA)
	}

	// xray + cloudflared restarted (HY2 also restarted because rotateHy2Config kicks it).
	wantRestart := map[string]bool{
		"cfvpn-xray.service":         false,
		"cfvpn-cloudflared.service":  false,
		"cfvpn-hysteria.service":     false,
	}
	for _, c := range runner.calls {
		joined := strings.Join(c, " ")
		for svc := range wantRestart {
			if strings.Contains(joined, svc) && strings.Contains(joined, "restart") {
				wantRestart[svc] = true
			}
		}
	}
	for svc, ok := range wantRestart {
		if !ok {
			t.Errorf("missing restart for %s; calls=%#v", svc, runner.calls)
		}
	}

	// Cert issued for HY2 only (cloudflare-mode VPN host has no cert).
	if len(cm.hosts) != 1 || cm.hosts[0] != "hy2-new.example.com" {
		t.Errorf("expected one cert.Issue for hy2-new only, got %v", cm.hosts)
	}

	// xray config rewritten with new host header.
	xrayBytes, err := os.ReadFile(xrayConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(xrayBytes, []byte("vpn-new.example.com")) {
		t.Errorf("xray config missing new host: %s", xrayBytes)
	}
	if !bytes.Contains(xrayBytes, []byte("httpupgrade")) {
		t.Errorf("xray config missing httpupgrade transport: %s", xrayBytes)
	}

	// cloudflared.yml rewritten with new VPN host + same admin host.
	cfdBytes, err := os.ReadFile(cloudflaredConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(cfdBytes, []byte("vpn-new.example.com")) {
		t.Errorf("cloudflared.yml missing new VPN host: %s", cfdBytes)
	}
	if !bytes.Contains(cfdBytes, []byte("admin.example.com")) {
		t.Errorf("cloudflared.yml missing admin host: %s", cfdBytes)
	}
	if !bytes.Contains(cfdBytes, []byte("tunnel-uuid-1")) {
		t.Errorf("cloudflared.yml missing tunnel uuid: %s", cfdBytes)
	}

	// env updated.
	envBytes, _ := os.ReadFile(envFilePath)
	for _, want := range []string{
		"DOMAIN=vpn-new.example.com",
		"MODE=cloudflare",
		"HY2_HOST=hy2-new.example.com",
		"PUBLIC_IP=203.0.113.10",
	} {
		if !bytes.Contains(envBytes, []byte(want)) {
			t.Errorf("env missing %q: %s", want, envBytes)
		}
	}

	// Subscription regenerated under new host.
	subBytes, err := os.ReadFile(filepath.Join(subscriptionDir, "alice.txt"))
	if err != nil {
		t.Fatalf("read alice.txt: %v", err)
	}
	decoded, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(subBytes)))
	if decErr != nil {
		t.Fatalf("subscription not valid base64: %v", decErr)
	}
	if !bytes.Contains(decoded, []byte("vpn-new.example.com")) {
		t.Errorf("subscription URI missing new host: %s", decoded)
	}
}

func TestRunRotateCloudflareWithoutHy2(t *testing.T) {
	withRotateCloudflareTempPaths(t)
	if err := state.SaveAtomic(envFilePath, map[string]string{
		"DOMAIN":            "old.example.com",
		"MODE":              "cloudflare",
		"ADMIN_TUNNEL_UUID": "tunnel-uuid-1",
		"ADMIN_HOST":        "admin.example.com",
		"PUBLIC_IP":         "203.0.113.10",
	}, 0o600); err != nil {
		t.Fatal(err)
	}

	cf := &fakeRotateCloudflareCF{}
	cm := &trackingCertManager{}
	res, err := RunRotateCloudflare(context.Background(), RotateCloudflareInputs{
		NewHost:       "vpn-new.example.com",
		NewZoneID:     "vpn-zone",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}},
	}, RotateCloudflareDeps{CF: cf, Cert: cm, Runner: &recordingRunner{}}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("RunRotateCloudflare: %v", err)
	}

	// No cert issued (no HY2).
	if len(cm.hosts) != 0 {
		t.Errorf("expected zero cert.Issue calls when HY2 omitted, got %v", cm.hosts)
	}
	// No A record touched.
	if len(cf.upsertA) != 0 || len(cf.deleteA) != 0 {
		t.Errorf("expected no A record mutations when HY2 omitted; upsertA=%#v deleteA=%#v", cf.upsertA, cf.deleteA)
	}
	if res.Hy2Host != "" {
		t.Errorf("res.Hy2Host: got %q want empty", res.Hy2Host)
	}
}

func TestRunRotateCloudflareRollsBackOnXrayRestartFailure(t *testing.T) {
	withRotateCloudflareTempPaths(t)
	if err := state.SaveAtomic(envFilePath, map[string]string{
		"DOMAIN":            "old.example.com",
		"MODE":              "cloudflare",
		"ADMIN_TUNNEL_UUID": "tunnel-uuid-1",
		"ADMIN_HOST":        "admin.example.com",
		"PUBLIC_IP":         "203.0.113.10",
	}, 0o600); err != nil {
		t.Fatal(err)
	}
	// Pre-populate an old xray config so the rollback path has something to restore.
	oldXray := []byte(`{"old":true}`)
	if err := os.WriteFile(xrayConfigPath, oldXray, 0o600); err != nil {
		t.Fatal(err)
	}

	cf := &fakeRotateCloudflareCF{}
	_, err := RunRotateCloudflare(context.Background(), RotateCloudflareInputs{
		NewHost:       "vpn-new.example.com",
		NewZoneID:     "vpn-zone",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "uuid-a"}},
	}, RotateCloudflareDeps{CF: cf, Cert: &trackingCertManager{}, Runner: failingRunner{}}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected restart failure")
	}

	// Old xray config restored on rollback.
	got, readErr := os.ReadFile(xrayConfigPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldXray) {
		t.Errorf("xray config not restored: got %s want %s", got, oldXray)
	}

	// New CNAME deleted as part of rollback.
	if len(cf.deleteCNAME) != 1 ||
		cf.deleteCNAME[0].zoneID != "vpn-zone" ||
		cf.deleteCNAME[0].name != "vpn-new.example.com" {
		t.Errorf("expected rollback delete of new CNAME, got %#v", cf.deleteCNAME)
	}
}
