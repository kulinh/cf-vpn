package commands

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/zones"
)

type fakeInstallCF struct {
	zones          map[string]string
	failCNAME      bool
	tunnelID       string
	creds          []byte
	createCalls    int
	lastTunnelName string
	cnames         [][3]string
	aRecords       [][3]string
	deletedA       [][2]string
	getZoneCalls   int
	getZoneNames   []string
	events         []string
}

func (f *fakeInstallCF) GetZoneID(_ context.Context, domain string) (string, error) {
	f.getZoneCalls++
	f.getZoneNames = append(f.getZoneNames, domain)
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
	if f.failCNAME {
		return errors.New("forced cname failure")
	}
	f.cnames = append(f.cnames, [3]string{z, n, target})
	f.events = append(f.events, "cname")
	return nil
}

func (f *fakeInstallCF) UpsertARecord(_ context.Context, z, n, ip string) error {
	f.aRecords = append(f.aRecords, [3]string{z, n, ip})
	f.events = append(f.events, "a")
	return nil
}
func (f *fakeInstallCF) DeleteARecordByName(_ context.Context, z, n string) error {
	f.deletedA = append(f.deletedA, [2]string{z, n})
	return nil
}
func (f *fakeInstallCF) DeleteTunnel(_ context.Context, id string) error { return nil }

type fakeUFW struct{ rules []string }

func (f *fakeUFW) Allow(_ context.Context, rule string) error {
	f.rules = append(f.rules, rule)
	return nil
}

type fakeInstallIP struct{ ip string }

func (f fakeInstallIP) Detect(context.Context) (string, error) { return f.ip, nil }

type fakePortProber struct{ err error }

func (f fakePortProber) Probe(context.Context) error { return f.err }

type fakeUDPProber struct {
	busy  map[int]bool
	ports []int
}

func (f *fakeUDPProber) ProbeUDP(_ context.Context, port int) error {
	f.ports = append(f.ports, port)
	if f.busy != nil && f.busy[port] {
		return errors.New("udp busy")
	}
	return nil
}

type fakeInstallCert struct {
	host, cert, key string
	issues          []fakeCertIssue
}

type fakeCertIssue struct{ host, cert, key, token string }

func (f *fakeInstallCert) Issue(_ context.Context, host, certPath, keyPath, token string) error {
	f.host = host
	f.cert = certPath
	f.key = keyPath
	f.issues = append(f.issues, fakeCertIssue{host: host, cert: certPath, key: keyPath, token: token})
	return nil
}
func (f *fakeInstallCert) Renew(_ context.Context, host, certPath, keyPath, token string, days int) error {
	f.host = host
	f.cert = certPath
	f.key = keyPath
	return nil
}

// installRecorder records every Run call made against both the binary
// runner and the systemd runner.
type installRecorder struct {
	calls [][]string
	fail  string
}

func (r *installRecorder) Run(_ context.Context, name string, args ...string) error {
	if r.fail == "" && name == "systemctl" && len(args) == 2 && args[0] == "restart" && args[1] == "cfvpn-xray.service" && r.countJoined("systemctl restart cfvpn-xray.service") == 1 {
		env, _ := os.ReadFile(envFilePath)
		if !strings.Contains(string(env), "DOMAIN=proxied.example.com") {
			return errors.New("rollback restarted xray before env restore")
		}
	}
	inv := append([]string{name}, args...)
	r.calls = append(r.calls, inv)
	if r.fail != "" && strings.Contains(strings.Join(inv, " "), r.fail) {
		return errors.New("forced failure")
	}
	return nil
}

func (r *installRecorder) countJoined(want string) int {
	count := 0
	for _, call := range r.calls {
		if strings.Join(call, " ") == want {
			count++
		}
	}
	return count
}

func withInstallSeams(t *testing.T) string {
	dir := t.TempDir()
	oldEnv, oldCred, oldCfg, oldXray, oldHy, oldSub, oldSysd := envFilePath, cloudflaredCredDir, cloudflaredConfig, xrayConfigPath, hysteriaConfigPath, subscriptionDir, systemdUnitDir
	envFilePath = filepath.Join(dir, "cfvpn.env")
	cloudflaredCredDir = filepath.Join(dir, "creds")
	cloudflaredConfig = filepath.Join(dir, "cloudflared.yml")
	xrayConfigPath = filepath.Join(dir, "xray.json")
	hysteriaConfigPath = filepath.Join(dir, "hysteria.yaml")
	subscriptionDir = filepath.Join(dir, "subs")
	systemdUnitDir = filepath.Join(dir, "units")
	t.Cleanup(func() {
		envFilePath, cloudflaredCredDir, cloudflaredConfig, xrayConfigPath, hysteriaConfigPath, subscriptionDir, systemdUnitDir = oldEnv, oldCred, oldCfg, oldXray, oldHy, oldSub, oldSysd
	})
	return dir
}

func baseInstallDeps(cf *fakeInstallCF) InstallDeps {
	return InstallDeps{
		CF:            cf,
		IP:            fakeInstallIP{ip: "203.0.113.42"},
		Cert:          &fakeInstallCert{},
		UFW:           &fakeUFW{},
		PortProber:    fakePortProber{},
		UDPProber:     &fakeUDPProber{},
		BinaryRunner:  &installRecorder{},
		SystemdRunner: &installRecorder{},
	}
}

func TestRunInstallAutoPicksDomainFromDefaultPool(t *testing.T) {
	withInstallSeams(t)
	cfZones := map[string]string{adminHostZone: "admin-zone"}
	poolIDs := make(map[string]string)
	zoneAlternates := make([]string, 0, len(zones.DefaultPool))
	for _, z := range zones.DefaultPool {
		cfZones[z.Name] = "visible-" + z.CFZoneID
		poolIDs[z.Name] = z.CFZoneID
		zoneAlternates = append(zoneAlternates, regexp.QuoteMeta(z.Name))
	}
	cfZones[adminHostZone] = "admin-zone"
	cf := &fakeInstallCF{zones: cfZones, tunnelID: "tun-auto", creds: []byte(`{"k":"v"}`)}
	var out, errBuf bytes.Buffer
	in := InstallInputs{CFAPIToken: "cf-token", CFAccountID: "cf-acct", NodeID: "JPY-04", User1Name: "alice", Mode: "direct"}
	if err := RunInstall(context.Background(), in, baseInstallDeps(cf), &out, &errBuf); err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	// Now 2 A records: vpn host + hy2 host.
	if len(cf.aRecords) != 2 {
		t.Fatalf("A upserts = %d, want 2: %#v", len(cf.aRecords), cf.aRecords)
	}
	host := cf.aRecords[0][1]
	re := regexp.MustCompile(`^(cdn|static|assets|edge|media)-[0-9a-f]{8}\.(` + strings.Join(zoneAlternates, "|") + `)$`)
	m := re.FindStringSubmatch(host)
	if m == nil {
		t.Fatalf("auto host %q does not match CDN pool format", host)
	}
	zone := m[2]
	if cf.getZoneCalls != 2 || len(cf.getZoneNames) != 2 || cf.getZoneNames[0] != zone || cf.getZoneNames[1] != adminHostZone {
		t.Fatalf("GetZoneID calls = %d names=%#v, want VPN zone then admin zone", cf.getZoneCalls, cf.getZoneNames)
	}
	if got, want := cf.aRecords[0][0], poolIDs[zone]; got != want {
		t.Fatalf("A upsert zone id = %q, want pool id %q", got, want)
	}
	// Second A record is hy2 host in the same zone.
	if cf.aRecords[1][2] != "203.0.113.42" {
		t.Fatalf("HY2 A record ip = %q, want 203.0.113.42", cf.aRecords[1][2])
	}
	if len(cf.cnames) != 1 || cf.cnames[0] != [3]string{"admin-zone", "jpy-04.rwl247.dev", "tun-auto.cfargotunnel.com"} {
		t.Fatalf("CNAME upsert = %#v", cf.cnames)
	}
	if !strings.Contains(out.String(), "install complete: direct mode "+host) {
		t.Fatalf("stdout does not include generated host %q: %s", host, out.String())
	}
}

func TestRunInstallExplicitDomainUsesLookupZoneID(t *testing.T) {
	withInstallSeams(t)
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-id", adminHostZone: "admin-zone"}, tunnelID: "tun-explicit", creds: []byte(`{"k":"v"}`)}
	var out, errBuf bytes.Buffer
	in := InstallInputs{CFAPIToken: "cf-token", CFAccountID: "cf-acct", Domain: "foo.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: "direct"}
	if err := RunInstall(context.Background(), in, baseInstallDeps(cf), &out, &errBuf); err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if cf.getZoneCalls != 2 || len(cf.getZoneNames) != 2 || cf.getZoneNames[0] != "example.com" || cf.getZoneNames[1] != adminHostZone {
		t.Fatalf("GetZoneID calls = %d names=%#v", cf.getZoneCalls, cf.getZoneNames)
	}
	// 2 A records: vpn domain + hy2 host.
	if len(cf.aRecords) != 2 || cf.aRecords[0] != [3]string{"zone-id", "foo.example.com", "203.0.113.42"} {
		t.Fatalf("A upsert[0] = %#v", cf.aRecords)
	}
	if cf.aRecords[1][2] != "203.0.113.42" {
		t.Fatalf("HY2 A record ip = %q, want 203.0.113.42", cf.aRecords[1][2])
	}
	if len(cf.cnames) != 1 || cf.cnames[0] != [3]string{"admin-zone", "jpy-04.rwl247.dev", "tun-explicit.cfargotunnel.com"} {
		t.Fatalf("CNAME upsert = %#v", cf.cnames)
	}
}

func TestRunInstallAutoPickZoneNotVisible(t *testing.T) {
	withInstallSeams(t)
	cf := &fakeInstallCF{zones: map[string]string{}, tunnelID: "tun-hidden", creds: []byte(`{"k":"v"}`)}
	var out, errBuf bytes.Buffer
	in := InstallInputs{CFAPIToken: "cf-token", CFAccountID: "cf-acct", NodeID: "JPY-04", User1Name: "alice", Mode: "direct"}
	err := RunInstall(context.Background(), in, baseInstallDeps(cf), &out, &errBuf)
	if err == nil {
		t.Fatalf("expected RunInstall error")
	}
	if len(cf.getZoneNames) != 1 {
		t.Fatalf("GetZoneID names = %#v, want one picked zone", cf.getZoneNames)
	}
	want := "zone " + cf.getZoneNames[0] + " not found via CF token; check internal/zones/pool.go matches the token's account"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want containing %q", err.Error(), want)
	}
	if len(cf.aRecords) != 0 {
		t.Fatalf("unexpected A upsert after hidden zone: %#v", cf.aRecords)
	}
}

func TestRunInstallGeneratesHy2HostPortObfsAndPersistsEnv(t *testing.T) {
	withInstallSeams(t)
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-id", adminHostZone: "admin-zone"}, tunnelID: "tun-hy2", creds: []byte(`{"k":"v"}`)}
	udp := &fakeUDPProber{}
	deps := baseInstallDeps(cf)
	deps.UDPProber = udp
	var out, errBuf bytes.Buffer
	in := InstallInputs{CFAPIToken: "cf-token", CFAccountID: "cf-acct", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: "direct"}

	if err := RunInstall(context.Background(), in, deps, &out, &errBuf); err != nil {
		t.Fatalf("RunInstall: %v", err)
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := regexp.MatchString(`^(quic|udp|hy)-[0-9a-f]{8}\.example\.com$`, env["HY2_HOST"]); !ok {
		t.Fatalf("HY2_HOST = %q, want generated hy2 host in example.com", env["HY2_HOST"])
	}
	if len(udp.ports) != 1 {
		t.Fatalf("UDP probes = %#v, want one", udp.ports)
	}
	if env["HY2_PORT"] != strconv.Itoa(udp.ports[0]) {
		t.Fatalf("HY2_PORT = %q, probed ports %#v", env["HY2_PORT"], udp.ports)
	}
	if udp.ports[0] < 20000 || udp.ports[0] > 60000 {
		t.Fatalf("HY2_PORT = %d, want in [20000,60000]", udp.ports[0])
	}
	if env["HY2_OBFS_PW"] == "" {
		t.Fatalf("HY2_OBFS_PW was not persisted")
	}
}

func TestRunInstallRetriesBusyHy2UDPPortAndPreservesExplicitHy2Inputs(t *testing.T) {
	withInstallSeams(t)
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-id", adminHostZone: "admin-zone"}, tunnelID: "tun-hy2-explicit", creds: []byte(`{"k":"v"}`)}
	udp := &fakeUDPProber{busy: map[int]bool{20000: true, 20001: true}}
	deps := baseInstallDeps(cf)
	deps.UDPProber = udp
	deps.Random = bytes.NewReader(append([]byte{0, 0, 0, 1, 0, 2}, bytes.Repeat([]byte{3}, 64)...))
	var out, errBuf bytes.Buffer
	in := InstallInputs{CFAPIToken: "cf-token", CFAccountID: "cf-acct", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: "direct", Hy2Host: "hy2.example.com", Hy2ObfsPW: "explicit-obfs"}

	if err := RunInstall(context.Background(), in, deps, &out, &errBuf); err != nil {
		t.Fatalf("RunInstall: %v", err)
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if env["HY2_HOST"] != "hy2.example.com" || env["HY2_OBFS_PW"] != "explicit-obfs" {
		t.Fatalf("explicit HY2 inputs not preserved: %#v", env)
	}
	if env["HY2_PORT"] != "20002" {
		t.Fatalf("HY2_PORT = %q, want first non-busy retry 20002", env["HY2_PORT"])
	}
	if want := []int{20000, 20001, 20002}; !reflect.DeepEqual(udp.ports, want) {
		t.Fatalf("UDP probes = %#v, want %#v", udp.ports, want)
	}
}

func TestRunInstallPreservesExplicitHy2PortWithoutProbingUDP(t *testing.T) {
	withInstallSeams(t)
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-id", adminHostZone: "admin-zone"}, tunnelID: "tun-hy2-port", creds: []byte(`{"k":"v"}`)}
	udp := &fakeUDPProber{}
	deps := baseInstallDeps(cf)
	deps.UDPProber = udp
	var out, errBuf bytes.Buffer
	in := InstallInputs{CFAPIToken: "cf-token", CFAccountID: "cf-acct", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: "direct", Hy2Host: "hy2.example.com", Hy2Port: "24444", Hy2ObfsPW: "explicit-obfs"}

	if err := RunInstall(context.Background(), in, deps, &out, &errBuf); err != nil {
		t.Fatalf("RunInstall: %v", err)
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if env["HY2_PORT"] != "24444" {
		t.Fatalf("HY2_PORT = %q, want explicit port", env["HY2_PORT"])
	}
	if len(udp.ports) != 0 {
		t.Fatalf("UDP probes = %#v, want none for explicit port", udp.ports)
	}
}

func TestRunInstallIssuesHy2AndDirectVPNCertsToServicePaths(t *testing.T) {
	withInstallSeams(t)
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-id", adminHostZone: "admin-zone"}, tunnelID: "tun-certs", creds: []byte(`{"k":"v"}`)}
	cert := &fakeInstallCert{}
	deps := baseInstallDeps(cf)
	deps.Cert = cert
	var out, errBuf bytes.Buffer
	in := InstallInputs{CFAPIToken: "cf-token", CFAccountID: "cf-acct", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: "direct", Hy2Host: "hy2.example.com", Hy2Port: "24444", Hy2ObfsPW: "explicit-obfs"}

	if err := RunInstall(context.Background(), in, deps, &out, &errBuf); err != nil {
		t.Fatalf("RunInstall: %v", err)
	}

	want := []fakeCertIssue{
		{host: "hy2.example.com", cert: "/etc/cfvpn/hysteria/cert.pem", key: "/etc/cfvpn/hysteria/key.pem", token: "cf-token"},
	}
	if !reflect.DeepEqual(cert.issues, want) {
		t.Fatalf("cert issues = %#v, want %#v", cert.issues, want)
	}
}

func TestRunInstallRejectsInvalidExplicitHy2PortBeforeMutating(t *testing.T) {
	for _, hy2Port := range []string{"abc", "0", "19999", "60001", "24444x"} {
		t.Run(hy2Port, func(t *testing.T) {
			withInstallSeams(t)
			cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-id", adminHostZone: "admin-zone"}, tunnelID: "tun-hy2-port", creds: []byte(`{"k":"v"}`)}
			udp := &fakeUDPProber{}
			deps := baseInstallDeps(cf)
			deps.UDPProber = udp
			var out, errBuf bytes.Buffer
			in := InstallInputs{CFAPIToken: "cf-token", CFAccountID: "cf-acct", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: "direct", Hy2Host: "hy2.example.com", Hy2Port: hy2Port, Hy2ObfsPW: "explicit-obfs"}

			err := RunInstall(context.Background(), in, deps, &out, &errBuf)
			if err == nil || !strings.Contains(err.Error(), "HY2_PORT must be in [20000,60000]") {
				t.Fatalf("RunInstall error = %v, want HY2_PORT validation error", err)
			}
			if cf.getZoneCalls != 0 || cf.createCalls != 0 || len(cf.aRecords) != 0 || len(cf.cnames) != 0 {
				t.Fatalf("invalid HY2_PORT mutated Cloudflare: %#v", cf)
			}
			if len(udp.ports) != 0 {
				t.Fatalf("UDP probes = %#v, want none for invalid explicit port", udp.ports)
			}
			if _, err := os.Stat(envFilePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("env file stat err = %v, want not exist", err)
			}
		})
	}
}

func TestInstallRequiresToken(t *testing.T) {
	var out, errBuf bytes.Buffer
	cfg := InstallInputs{CFAPIToken: "", CFAccountID: "a", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: "direct"}
	if err := RunInstall(context.Background(), cfg, InstallDeps{CF: &fakeInstallCF{}}, &out, &errBuf); err == nil {
		t.Fatalf("expected error when token empty")
	}
}

func TestInstallRejectsBadModeBeforeDNS(t *testing.T) {
	for _, mode := range []string{"proxy", "DIRECT"} {
		var out, errBuf bytes.Buffer
		cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-1", adminHostZone: "admin-zone"}}
		cfg := InstallInputs{CFAPIToken: "t", CFAccountID: "a", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: mode}
		err := RunInstall(context.Background(), cfg, InstallDeps{CF: cf}, &out, &errBuf)
		if err == nil || err.Error() != "MODE must be direct or cloudflare" {
			t.Fatalf("mode %q error = %v", mode, err)
		}
		if cf.getZoneCalls != 0 || len(cf.aRecords) != 0 || len(cf.cnames) != 0 || cf.createCalls != 0 {
			t.Fatalf("mode %q mutated Cloudflare: %#v", mode, cf)
		}
	}
}

func TestRunInstallDirectModeAbortsWhenPort443Busy(t *testing.T) {
	withInstallSeams(t)
	var out, errBuf bytes.Buffer
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-1", adminHostZone: "admin-zone"}, tunnelID: "tun", creds: []byte(`{"k":"v"}`)}
	binRunner := &installRecorder{}
	sysRunner := &installRecorder{}
	cert := &fakeInstallCert{}
	ufw := &fakeUFW{}
	deps := InstallDeps{
		CF:            cf,
		IP:            fakeInstallIP{ip: "203.0.113.42"},
		Cert:          cert,
		UFW:           ufw,
		PortProber:    fakePortProber{err: errors.New("listen tcp :443: bind: address already in use")},
		BinaryRunner:  binRunner,
		SystemdRunner: sysRunner,
	}
	cfg := InstallInputs{CFAPIToken: "t", CFAccountID: "a", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: "direct"}

	err := RunInstall(context.Background(), cfg, deps, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), "port_443_busy") {
		t.Fatalf("RunInstall error = %v, want port_443_busy", err)
	}
	if cf.getZoneCalls != 0 || cf.createCalls != 0 || len(cf.aRecords) != 0 || len(cf.cnames) != 0 {
		t.Fatalf("Cloudflare mutated after busy port failure: %#v", cf)
	}
	if len(binRunner.calls) != 0 || len(sysRunner.calls) != 0 {
		t.Fatalf("runners called after busy port failure: bin=%#v sys=%#v", binRunner.calls, sysRunner.calls)
	}
	if cert.host != "" || len(ufw.rules) != 0 {
		t.Fatalf("later mutating deps called after busy port failure: cert=%#v ufw=%#v", cert, ufw.rules)
	}
	if _, err := os.Stat(envFilePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("env file stat err = %v, want not exist", err)
	}
}

func TestInstallRejectsInvalidNodeIDBeforeDNS(t *testing.T) {
	for _, nodeID := range []string{"bad.name", "SG 2", "_bad", "-bad", "bad-"} {
		var out, errBuf bytes.Buffer
		cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-1", adminHostZone: "admin-zone"}}
		cfg := InstallInputs{CFAPIToken: "t", CFAccountID: "a", Domain: "vpn.example.com", NodeID: nodeID, User1Name: "alice", Mode: "direct"}
		err := RunInstall(context.Background(), cfg, InstallDeps{CF: cf}, &out, &errBuf)
		if err == nil || err.Error() != "NODE_ID must be a DNS label" {
			t.Fatalf("nodeID %q error = %v", nodeID, err)
		}
		if cf.getZoneCalls != 0 || len(cf.aRecords) != 0 || len(cf.cnames) != 0 || cf.createCalls != 0 {
			t.Fatalf("nodeID %q mutated Cloudflare: %#v", nodeID, cf)
		}
	}
}

func TestRunInstallCloudflareModeRendersTunnelIngressAndLoopbackXray(t *testing.T) {
	dir := t.TempDir()
	oldEnv, oldCred, oldCfg, oldXray, oldHy, oldSub, oldSysd := envFilePath, cloudflaredCredDir, cloudflaredConfig, xrayConfigPath, hysteriaConfigPath, subscriptionDir, systemdUnitDir
	envFilePath = filepath.Join(dir, "cfvpn.env")
	cloudflaredCredDir = filepath.Join(dir, "creds")
	cloudflaredConfig = filepath.Join(dir, "cloudflared.yml")
	xrayConfigPath = filepath.Join(dir, "xray.json")
	hysteriaConfigPath = filepath.Join(dir, "hysteria.yaml")
	subscriptionDir = filepath.Join(dir, "subs")
	systemdUnitDir = filepath.Join(dir, "units")
	t.Cleanup(func() {
		envFilePath, cloudflaredCredDir, cloudflaredConfig, xrayConfigPath, hysteriaConfigPath, subscriptionDir, systemdUnitDir = oldEnv, oldCred, oldCfg, oldXray, oldHy, oldSub, oldSysd
	})

	cf := &fakeInstallCF{
		zones:    map[string]string{"vpn.example.com": "zone-1", "example.com": "zone-1", adminHostZone: "admin-zone"},
		tunnelID: "tun-abc",
		creds:    []byte(`{"k":"v"}`),
	}
	cert := &fakeInstallCert{}
	ufw := &fakeUFW{}
	deps := InstallDeps{
		CF:            cf,
		IP:            fakeInstallIP{ip: "203.0.113.42"},
		Cert:          cert,
		UFW:           ufw,
		PortProber:    fakePortProber{},
		UDPProber:     &fakeUDPProber{},
		BinaryRunner:  &installRecorder{},
		SystemdRunner: &installRecorder{},
	}
	in := InstallInputs{CFAPIToken: "cf-token", CFAccountID: "cf-acct", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: "cloudflare", Hy2Port: "25000"}

	var out, errBuf bytes.Buffer
	if err := RunInstall(context.Background(), in, deps, &out, &errBuf); err != nil {
		t.Fatalf("RunInstall: %v", err)
	}

	cfCfg, err := os.ReadFile(cloudflaredConfig)
	if err != nil {
		t.Fatal(err)
	}
	cfS := string(cfCfg)
	for _, want := range []string{"hostname: vpn.example.com", "path: ^/api/v1/sync", "service: http://127.0.0.1:10001", "hostname: jpy-04.rwl247.dev", "service: http://127.0.0.1:6788"} {
		if !strings.Contains(cfS, want) {
			t.Fatalf("cloudflared config missing %q:\n%s", want, cfS)
		}
	}
	for _, notWant := range []string{"/trojan", "10002"} {
		if strings.Contains(cfS, notWant) {
			t.Fatalf("cloudflared config contains %q:\n%s", notWant, cfS)
		}
	}

	xrayCfg, err := os.ReadFile(xrayConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	xrayS := string(xrayCfg)
	for _, want := range []string{"\"listen\": \"127.0.0.1\"", "\"port\": 10001", "alice@vpn"} {
		if !strings.Contains(xrayS, want) {
			t.Fatalf("xray config missing %q:\n%s", want, xrayS)
		}
	}
	for _, notWant := range []string{"\"port\": 443", "/trojan", "10002", "/etc/cfvpn/xray/cert.pem", "/etc/cfvpn/xray/key.pem"} {
		if strings.Contains(xrayS, notWant) {
			t.Fatalf("xray config contains %q:\n%s", notWant, xrayS)
		}
	}

	envRaw, err := os.ReadFile(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envRaw), "MODE=cloudflare") {
		t.Fatalf("env missing MODE=cloudflare:\n%s", envRaw)
	}
	// Cloudflare mode: only HY2 A record; no VPN domain A record.
	if len(cf.aRecords) != 1 {
		t.Fatalf("cloudflare mode A upserts = %d, want 1 (hy2 only): %#v", len(cf.aRecords), cf.aRecords)
	}
	if cf.aRecords[0][2] != "203.0.113.42" {
		t.Fatalf("HY2 A record ip = %q, want 203.0.113.42", cf.aRecords[0][2])
	}
	if len(cf.cnames) != 2 {
		t.Fatalf("cloudflare mode CNAME upserts = %#v, want vpn and admin CNAMEs", cf.cnames)
	}
	if cf.cnames[0] != [3]string{"zone-1", "vpn.example.com", "tun-abc.cfargotunnel.com"} {
		t.Fatalf("VPN CNAME upsert = %#v", cf.cnames[0])
	}
	if cf.cnames[1] != [3]string{"admin-zone", "jpy-04.rwl247.dev", "tun-abc.cfargotunnel.com"} {
		t.Fatalf("admin CNAME upsert = %#v", cf.cnames[1])
	}
	if len(cert.issues) != 1 || cert.issues[0].host == "vpn.example.com" {
		t.Fatalf("cloudflare mode should not issue xray domain cert, issues=%#v", cert.issues)
	}
	// UFW: cloudflare mode must NOT allow 443/tcp but must allow hy2 port/udp.
	for _, rule := range ufw.rules {
		if rule == "443/tcp" {
			t.Fatalf("cloudflare mode opened 443/tcp in UFW: %#v", ufw.rules)
		}
	}
	if len(ufw.rules) != 1 || ufw.rules[0] != "25000/udp" {
		t.Fatalf("cloudflare mode UFW rules = %#v, want [25000/udp]", ufw.rules)
	}
}

func TestRunInstallDirectModeWiresAllSteps(t *testing.T) {
	dir := t.TempDir()
	oldEnv, oldCred, oldCfg, oldXray, oldHy, oldSub, oldSysd := envFilePath, cloudflaredCredDir, cloudflaredConfig, xrayConfigPath, hysteriaConfigPath, subscriptionDir, systemdUnitDir
	envFilePath = filepath.Join(dir, "cfvpn.env")
	cloudflaredCredDir = filepath.Join(dir, "creds")
	cloudflaredConfig = filepath.Join(dir, "cloudflared.yml")
	xrayConfigPath = filepath.Join(dir, "xray.json")
	hysteriaConfigPath = filepath.Join(dir, "hysteria.yaml")
	subscriptionDir = filepath.Join(dir, "subs")
	systemdUnitDir = filepath.Join(dir, "units")
	t.Cleanup(func() {
		envFilePath, cloudflaredCredDir, cloudflaredConfig, xrayConfigPath, hysteriaConfigPath, subscriptionDir, systemdUnitDir = oldEnv, oldCred, oldCfg, oldXray, oldHy, oldSub, oldSysd
	})

	cf := &fakeInstallCF{
		zones:    map[string]string{"vpn.example.com": "zone-1", "example.com": "zone-1", adminHostZone: "admin-zone"},
		tunnelID: "tun-abc",
		creds:    []byte(`{"k":"v"}`),
	}
	binRunner := &installRecorder{}
	sysRunner := &installRecorder{}

	in := InstallInputs{
		CFAPIToken:   "cf-token",
		CFAccountID:  "cf-acct",
		Domain:       "vpn.example.com",
		NodeID:       "JPY-04",
		User1Name:    "alice",
		Mode:         "direct",
		Hy2Host:      "hy2.example.com",
		Hy2Port:      "24430",
		Hy2ObfsPW:    "obfs-secret",
		Hy2PassUser1: "hy2-user-pass",
	}
	ufw := &fakeUFW{}
	cert := &fakeInstallCert{cert: "/etc/cfvpn/certs/vpn.example.com/fullchain.pem", key: "/etc/cfvpn/certs/vpn.example.com/privkey.pem"}
	deps := InstallDeps{
		CF:            cf,
		IP:            fakeInstallIP{ip: "203.0.113.42"},
		Cert:          cert,
		UFW:           ufw,
		PortProber:    fakePortProber{},
		UDPProber:     &fakeUDPProber{},
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
	if !strings.HasPrefix(cf.lastTunnelName, "cfvpn-admin-") {
		t.Fatalf("tunnel name = %q, want cfvpn-admin-*", cf.lastTunnelName)
	}
	if cf.getZoneCalls != 2 || len(cf.getZoneNames) != 2 || cf.getZoneNames[0] != "example.com" || cf.getZoneNames[1] != adminHostZone {
		t.Fatalf("GetZoneID calls = %d names=%#v", cf.getZoneCalls, cf.getZoneNames)
	}
	if len(cf.aRecords) != 2 {
		t.Fatalf("A upserts = %d, want 2 (vpn host + hy2 host): %#v", len(cf.aRecords), cf.aRecords)
	}
	if cf.aRecords[0] != [3]string{"zone-1", "vpn.example.com", "203.0.113.42"} {
		t.Fatalf("A upsert[0] = %#v, want vpn.example.com -> 203.0.113.42", cf.aRecords[0])
	}
	if cf.aRecords[1] != [3]string{"zone-1", "hy2.example.com", "203.0.113.42"} {
		t.Fatalf("A upsert[1] = %#v, want hy2.example.com -> 203.0.113.42", cf.aRecords[1])
	}
	if len(cf.cnames) != 1 || cf.cnames[0] != [3]string{"admin-zone", "jpy-04.rwl247.dev", "tun-abc.cfargotunnel.com"} {
		t.Fatalf("CNAME upsert = %#v", cf.cnames)
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
	hyRaw, err := os.ReadFile(hysteriaConfigPath)
	if err != nil {
		t.Fatalf("hysteria config missing: %v", err)
	}
	wantHy := `listen: ":24430"
tls:
  cert: "/etc/cfvpn/hysteria/cert.pem"
  key: "/etc/cfvpn/hysteria/key.pem"
obfs:
  type: salamander
  salamander:
    password: "obfs-secret"
bandwidth:
  up: 100mbps
  down: 100mbps
auth:
  type: userpass
  userpass:
    "alice": "hy2-user-pass"
`
	if string(hyRaw) != wantHy {
		t.Fatalf("hysteria config mismatch:\nwant:\n%s\ngot:\n%s", wantHy, string(hyRaw))
	}
	for _, unit := range []string{"cfvpn-xray.service", "cfvpn-cloudflared.service", "cfvpn-agent.service"} {
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
		"MODE=direct",
		"PUBLIC_IP=203.0.113.42",
		"ADMIN_HOST=jpy-04.rwl247.dev",
		"ADMIN_TUNNEL_UUID=tun-abc",
		"UUID_USER1=",
		"HY2_HOST=hy2.example.com",
		"HY2_PORT=24430",
		"HY2_OBFS_PW=obfs-secret",
		"HY2_PASS_USER1=hy2-user-pass",
		"USER1_NAME=alice",
		"CF_API_TOKEN=cf-token",
		"CF_ACCOUNT_ID=cf-acct",
	} {
		if !strings.Contains(envS, want) {
			t.Fatalf("env file missing %q; full file:\n%s", want, envS)
		}
	}
	if strings.Contains(envS, "TROJAN_PASS_USER1=") {
		t.Fatalf("env file must not contain TROJAN_PASS_USER1; full file:\n%s", envS)
	}

	// Systemd runner: daemon-reload + enable --now for 5 services.
	if len(sysRunner.calls) != 6 {
		t.Fatalf("expected 6 systemd calls, got %d: %#v", len(sysRunner.calls), sysRunner.calls)
	}
	expect := [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "--now", "cfvpn-xray.service"},
		{"systemctl", "enable", "--now", "cfvpn-cloudflared.service"},
		{"systemctl", "enable", "--now", "cfvpn-agent.service"},
		{"systemctl", "enable", "--now", "cfvpn-hysteria.service"},
		{"systemctl", "enable", "--now", "cfvpn-cert-renew.timer"},
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
	if !strings.Contains(stdoutStr, "creating admin tunnel...") {
		t.Fatalf("stdout missing 'creating admin tunnel...': %q", stdoutStr)
	}
	if cert.host != "hy2.example.com" {
		t.Fatalf("cert host = %q, want hy2.example.com (xray cert no longer issued for direct Reality mode)", cert.host)
	}
	if len(ufw.rules) != 2 || ufw.rules[0] != "443/tcp" || ufw.rules[1] != "24430/udp" {
		t.Fatalf("ufw rules = %#v, want [443/tcp 24430/udp]", ufw.rules)
	}
	cfCfg, _ := os.ReadFile(cloudflaredConfig)
	if strings.Contains(string(cfCfg), "/api/v1/sync") || strings.Contains(string(cfCfg), "/trojan") || !strings.Contains(string(cfCfg), "127.0.0.1:6788") {
		t.Fatalf("bad cloudflared config: %s", cfCfg)
	}
	xrayCfg, _ := os.ReadFile(xrayConfigPath)
	for _, want := range []string{"\"listen\": \"0.0.0.0\"", "\"port\": 443", "\"network\": \"tcp\"", "\"security\": \"reality\"", "\"flow\": \"xtls-rprx-vision\"", "alice@vpn"} {
		if !strings.Contains(string(xrayCfg), want) {
			t.Fatalf("xray config missing %q: %s", want, xrayCfg)
		}
	}
	for _, notWant := range []string{"127.0.0.1", "10001", "ws", "tls-fallback", "fallbacks"} {
		if strings.Contains(string(xrayCfg), notWant) {
			t.Fatalf("direct xray config contains fallback shim %q: %s", notWant, xrayCfg)
		}
	}

	// The final line is the base64 subscription; decode it to be sure.
	lines := strings.Split(strings.TrimRight(stdoutStr, "\n"), "\n")
	subLine := lines[len(lines)-1]
	dec, err := base64.StdEncoding.DecodeString(subLine)
	if err != nil {
		t.Fatalf("final stdout line is not base64 (%q): %v", subLine, err)
	}
	if !strings.Contains(string(dec), "vless://") {
		t.Fatalf("decoded subscription lacks vless URI: %q", dec)
	}
	if strings.Contains(string(dec), "trojan://") {
		t.Fatalf("decoded subscription contains trojan URI: %q", dec)
	}

	// Binary runner: EnsureXray/EnsureCloudflared may have been called 0..2
	// times depending on whether `xray` / `cloudflared` exist on the test
	// host. Just assert the recorder was handed off (no panic on nil).
	_ = binRunner.calls
}

func TestRunInstallN7HysteriaServiceEnabled(t *testing.T) {
	withInstallSeams(t)
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-id", adminHostZone: "admin-zone"}, tunnelID: "tun-n7", creds: []byte(`{"k":"v"}`)}
	sys := &installRecorder{}
	deps := baseInstallDeps(cf)
	deps.SystemdRunner = sys
	in := InstallInputs{CFAPIToken: "t", CFAccountID: "a", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: "direct", Hy2Host: "hy2.example.com", Hy2Port: "24444"}
	var out, errBuf bytes.Buffer
	if err := RunInstall(context.Background(), in, deps, &out, &errBuf); err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	wantEnabled := []string{"cfvpn-hysteria.service", "cfvpn-cert-renew.timer"}
	for _, svc := range wantEnabled {
		found := false
		for _, call := range sys.calls {
			if strings.Join(call, " ") == "systemctl enable --now "+svc {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not enabled; calls = %#v", svc, sys.calls)
		}
	}
	// Unit files must exist on disk.
	for _, unit := range []string{"cfvpn-hysteria.service", "cfvpn-cert-renew.service", "cfvpn-cert-renew.timer"} {
		p := filepath.Join(systemdUnitDir, unit)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("unit file %s missing: %v", unit, err)
		}
	}
}

func TestRunInstallN7UFWHy2UDPAllowedBothModes(t *testing.T) {
	for _, mode := range []string{"direct", "cloudflare"} {
		t.Run(mode, func(t *testing.T) {
			withInstallSeams(t)
			cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-id", adminHostZone: "admin-zone"}, tunnelID: "tun-ufw", creds: []byte(`{"k":"v"}`)}
			ufw := &fakeUFW{}
			deps := baseInstallDeps(cf)
			deps.UFW = ufw
			in := InstallInputs{CFAPIToken: "t", CFAccountID: "a", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: mode, Hy2Host: "hy2.example.com", Hy2Port: "30000"}
			var out, errBuf bytes.Buffer
			if err := RunInstall(context.Background(), in, deps, &out, &errBuf); err != nil {
				t.Fatalf("RunInstall mode=%s: %v", mode, err)
			}
			found443 := false
			foundUDP := false
			for _, r := range ufw.rules {
				if r == "443/tcp" {
					found443 = true
				}
				if r == "30000/udp" {
					foundUDP = true
				}
			}
			if !foundUDP {
				t.Fatalf("mode=%s: 30000/udp not in UFW rules: %#v", mode, ufw.rules)
			}
			if mode == "direct" && !found443 {
				t.Fatalf("mode=direct: 443/tcp not in UFW rules: %#v", ufw.rules)
			}
			if mode == "cloudflare" && found443 {
				t.Fatalf("mode=cloudflare: 443/tcp must not be in UFW rules: %#v", ufw.rules)
			}
		})
	}
}

func TestRunInstallN7Hy2ARecordAlwaysWritten(t *testing.T) {
	for _, mode := range []string{"direct", "cloudflare"} {
		t.Run(mode, func(t *testing.T) {
			withInstallSeams(t)
			cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-id", adminHostZone: "admin-zone"}, tunnelID: "tun-hy2dns", creds: []byte(`{"k":"v"}`)}
			deps := baseInstallDeps(cf)
			in := InstallInputs{CFAPIToken: "t", CFAccountID: "a", Domain: "vpn.example.com", NodeID: "JPY-04", User1Name: "alice", Mode: mode, Hy2Host: "hy2.example.com", Hy2Port: "31000"}
			var out, errBuf bytes.Buffer
			if err := RunInstall(context.Background(), in, deps, &out, &errBuf); err != nil {
				t.Fatalf("RunInstall mode=%s: %v", mode, err)
			}
			foundHy2A := false
			for _, a := range cf.aRecords {
				if a[1] == "hy2.example.com" && a[2] == "203.0.113.42" {
					foundHy2A = true
				}
			}
			if !foundHy2A {
				t.Fatalf("mode=%s: HY2 A record (hy2.example.com -> 203.0.113.42) not written; aRecords=%#v", mode, cf.aRecords)
			}
		})
	}
}

func withUpgradeSeams(t *testing.T) string {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "etc", "cfvpn")
	oldEnv, oldCfg, oldCloud, oldSub, oldSysd := envFilePath, xrayConfigPath, cloudflaredConfig, subscriptionDir, systemdUnitDir
	envFilePath = filepath.Join(cfgDir, "cfvpn.env")
	xrayConfigPath = filepath.Join(cfgDir, "xray.json")
	cloudflaredConfig = filepath.Join(cfgDir, "cloudflared.yml")
	subscriptionDir = filepath.Join(cfgDir, "subscriptions")
	systemdUnitDir = filepath.Join(dir, "units")
	t.Cleanup(func() {
		envFilePath, xrayConfigPath, cloudflaredConfig, subscriptionDir, systemdUnitDir = oldEnv, oldCfg, oldCloud, oldSub, oldSysd
	})
	return dir
}

func seedUpgradeConfig(t *testing.T) {
	t.Helper()
	if err := state.SaveAtomic(envFilePath, map[string]string{"CF_API_TOKEN": "t", "CF_ACCOUNT_ID": "a", "DOMAIN": "proxied.example.com", "TUNNEL_UUID": "old-tun", "NODE_ID": "JPY-04", "USER1_NAME": "alice", "UUID_USER1": "u-1", "TROJAN_PASS_USER1": "p-1", "SUB_TOKEN_USER1": "subtok"}, 0o600); err != nil {
		t.Fatal(err)
	}
	rendered, err := templates.RenderXrayDirect(templates.XrayDirectInputs{Users: []templates.XrayUser{{Name: "alice", UUID: "u-1"}}, Certs: []templates.XrayCert{{Zone: "example.com", CertFile: "/old/cert", KeyFile: "/old/key"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicFile(xrayConfigPath, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicFile(cloudflaredConfig, []byte("old tunnel config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunUpgradePreservesUsersAndSetsDirectEnv(t *testing.T) {
	dir := withUpgradeSeams(t)
	seedUpgradeConfig(t)
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-1", adminHostZone: "admin-zone"}}
	cert := &fakeInstallCert{}
	deps := InstallDeps{CF: cf, IP: fakeInstallIP{ip: "203.0.113.42"}, Cert: cert, UFW: &fakeUFW{}, SystemdRunner: &installRecorder{}}
	var out, errBuf bytes.Buffer
	res, err := RunUpgrade(context.Background(), UpgradeInputs{BackupRoot: dir, Now: func() time.Time { return time.Unix(1234, 0) }}, deps, &out, &errBuf)
	if err != nil {
		t.Fatalf("RunUpgrade: %v", err)
	}
	if res.OldHost != "proxied.example.com" || !strings.HasSuffix(res.NewHost, ".example.com") || res.PublicIP != "203.0.113.42" {
		t.Fatalf("bad result: %#v", res)
	}
	newHostPattern := regexp.MustCompile(`^(cdn|static|assets|edge|media)-[0-9a-f]{8}\.example\.com$`)
	if !newHostPattern.MatchString(res.NewHost) {
		t.Fatalf("NewHost = %q, want CDN-style generated host", res.NewHost)
	}
	oldHostPattern := regexp.MustCompile(`^[0-9a-f]{4}\.example\.com$`)
	if oldHostPattern.MatchString(res.NewHost) {
		t.Fatalf("NewHost = %q, must not use old 4-hex format", res.NewHost)
	}
	envRaw, _ := os.ReadFile(envFilePath)
	envS := string(envRaw)
	// TROJAN_PASS_USER1 migrates to HY2_PASS_USER1 (P1 backfill).
	// HY2 fields are added as part of the upgrade.
	for _, want := range []string{"MODE=direct", "PUBLIC_IP=203.0.113.42", "DOMAIN=" + res.NewHost, "ADMIN_TUNNEL_UUID=old-tun", "ADMIN_HOST=jpy-04.rwl247.dev", "UUID_USER1=u-1", "HY2_PASS_USER1=p-1", "SUB_TOKEN_USER1=subtok", "HY2_HOST=", "HY2_PORT=", "HY2_OBFS_PW="} {
		if !strings.Contains(envS, want) {
			t.Fatalf("env missing %q:\n%s", want, envS)
		}
	}
	for _, line := range strings.Split(envS, "\n") {
		if strings.HasPrefix(line, "TUNNEL_UUID=") {
			t.Fatalf("env still has TUNNEL_UUID:\n%s", envS)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "cfvpn.backup-1234")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	xrayRaw, _ := os.ReadFile(xrayConfigPath)
	if !strings.Contains(string(xrayRaw), "u-1") {
		t.Fatalf("xray did not preserve credentials: %s", xrayRaw)
	}
	// Reality direct mode no longer issues xray certs.
	for _, notWant := range []string{"/etc/cfvpn/certs/", "fullchain.pem", "privkey.pem"} {
		if strings.Contains(string(xrayRaw), notWant) {
			t.Fatalf("xray config should not contain cert path %q: %s", notWant, xrayRaw)
		}
	}
	if len(cf.aRecords) != 2 || cf.aRecords[0][0] != "zone-1" || cf.aRecords[0][1] != res.NewHost || cf.aRecords[0][2] != "203.0.113.42" {
		t.Fatalf("A records = %#v", cf.aRecords)
	}
	hy2HostAfter := cf.aRecords[1][1]
	if cf.aRecords[1][0] != "zone-1" || cf.aRecords[1][2] != "203.0.113.42" || hy2HostAfter == res.NewHost {
		t.Fatalf("HY2 A record = %#v", cf.aRecords[1])
	}
	if len(cf.cnames) != 1 || cf.cnames[0] != [3]string{"admin-zone", "jpy-04.rwl247.dev", "old-tun.cfargotunnel.com"} {
		t.Fatalf("CNAME records = %#v", cf.cnames)
	}
	if got := strings.Join(cf.events, ","); got != "a,a,cname" {
		t.Fatalf("dns event order = %q, want a,a,cname with VPN+HY2 A then admin CNAME", got)
	}
}

func TestRunUpgradeRollsBackAfterAdminZoneLookupFailure(t *testing.T) {
	dir := withUpgradeSeams(t)
	seedUpgradeConfig(t)
	origEnv, _ := os.ReadFile(envFilePath)
	origXray, _ := os.ReadFile(xrayConfigPath)
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-1"}}
	runner := &installRecorder{}
	deps := InstallDeps{CF: cf, IP: fakeInstallIP{ip: "203.0.113.42"}, Cert: &fakeInstallCert{cert: "/etc/cfvpn/certs/vpn.example.com/fullchain.pem", key: "/etc/cfvpn/certs/vpn.example.com/privkey.pem"}, UFW: &fakeUFW{}, SystemdRunner: runner}
	_, err := RunUpgrade(context.Background(), UpgradeInputs{BackupRoot: dir, Now: func() time.Time { return time.Unix(7788, 0) }}, deps, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "get zone id for "+adminHostZone) {
		t.Fatalf("expected admin zone failure, got %v", err)
	}
	gotEnv, _ := os.ReadFile(envFilePath)
	gotXray, _ := os.ReadFile(xrayConfigPath)
	if !bytes.Equal(origEnv, gotEnv) || !bytes.Equal(origXray, gotXray) {
		t.Fatalf("config not restored after rollback")
	}
	if got := runner.countJoined("systemctl restart cfvpn-xray.service"); got != 2 {
		t.Fatalf("xray restart count = %d, want 2; calls=%#v", got, runner.calls)
	}
	if !rollbackDeletedAll(cf.aRecords, cf.deletedA) {
		t.Fatalf("rollback did not delete all created A records: created=%#v deleted=%#v", cf.aRecords, cf.deletedA)
	}
	if !strings.Contains(string(gotEnv), "DOMAIN=proxied.example.com") {
		t.Fatalf("env should restore old domain, got:\n%s", gotEnv)
	}
}

func TestRunUpgradeRollsBackAfterAdminCNAMEFailure(t *testing.T) {
	dir := withUpgradeSeams(t)
	seedUpgradeConfig(t)
	origEnv, _ := os.ReadFile(envFilePath)
	origXray, _ := os.ReadFile(xrayConfigPath)
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-1", adminHostZone: "admin-zone"}, failCNAME: true}
	runner := &installRecorder{}
	deps := InstallDeps{CF: cf, IP: fakeInstallIP{ip: "203.0.113.42"}, Cert: &fakeInstallCert{cert: "/etc/cfvpn/certs/vpn.example.com/fullchain.pem", key: "/etc/cfvpn/certs/vpn.example.com/privkey.pem"}, UFW: &fakeUFW{}, SystemdRunner: runner}
	_, err := RunUpgrade(context.Background(), UpgradeInputs{BackupRoot: dir, Now: func() time.Time { return time.Unix(8899, 0) }}, deps, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "upsert admin dns cname") {
		t.Fatalf("expected admin CNAME failure, got %v", err)
	}
	gotEnv, _ := os.ReadFile(envFilePath)
	gotXray, _ := os.ReadFile(xrayConfigPath)
	if !bytes.Equal(origEnv, gotEnv) || !bytes.Equal(origXray, gotXray) {
		t.Fatalf("config not restored after rollback")
	}
	if got := runner.countJoined("systemctl restart cfvpn-xray.service"); got != 2 {
		t.Fatalf("xray restart count = %d, want 2; calls=%#v", got, runner.calls)
	}
	if !rollbackDeletedAll(cf.aRecords, cf.deletedA) {
		t.Fatalf("rollback did not delete all created A records: created=%#v deleted=%#v", cf.aRecords, cf.deletedA)
	}
	if !strings.Contains(string(gotEnv), "DOMAIN=proxied.example.com") {
		t.Fatalf("env should restore old domain, got:\n%s", gotEnv)
	}
}

func TestRunUpgradeCheckIsDryRun(t *testing.T) {
	withUpgradeSeams(t)
	seedUpgradeConfig(t)
	before, _ := os.ReadFile(envFilePath)
	cf := &fakeInstallCF{zones: map[string]string{"proxied.example.com": "zone-1"}}
	var out bytes.Buffer
	if err := RunUpgradeCheck(context.Background(), UpgradeInputs{}, InstallDeps{CF: cf, IP: fakeInstallIP{ip: "203.0.113.42"}}, &out, nil); err != nil {
		t.Fatalf("RunUpgradeCheck: %v", err)
	}
	after, _ := os.ReadFile(envFilePath)
	if !bytes.Equal(before, after) {
		t.Fatalf("env changed during dry run")
	}
	if !strings.Contains(out.String(), "pre-flight OK") {
		t.Fatalf("stdout missing OK: %q", out.String())
	}
}

func TestRunUpgradeRollsBackAfterARecordFailure(t *testing.T) {
	dir := withUpgradeSeams(t)
	seedUpgradeConfig(t)
	origEnv, _ := os.ReadFile(envFilePath)
	origXray, _ := os.ReadFile(xrayConfigPath)
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-1"}}
	deps := InstallDeps{CF: cf, IP: fakeInstallIP{ip: "203.0.113.42"}, Cert: &fakeInstallCert{cert: "/etc/cfvpn/certs/vpn.example.com/fullchain.pem", key: "/etc/cfvpn/certs/vpn.example.com/privkey.pem"}, UFW: &fakeUFW{}, SystemdRunner: &installRecorder{fail: "restart cfvpn-xray.service"}}
	_, err := RunUpgrade(context.Background(), UpgradeInputs{BackupRoot: dir, Now: func() time.Time { return time.Unix(5678, 0) }}, deps, nil, nil)
	if err == nil {
		t.Fatalf("expected restart failure")
	}
	gotEnv, _ := os.ReadFile(envFilePath)
	gotXray, _ := os.ReadFile(xrayConfigPath)
	if !bytes.Equal(origEnv, gotEnv) || !bytes.Equal(origXray, gotXray) {
		t.Fatalf("config not restored after rollback")
	}
	if !rollbackDeletedAll(cf.aRecords, cf.deletedA) {
		t.Fatalf("rollback did not delete all created A records: created=%#v deleted=%#v", cf.aRecords, cf.deletedA)
	}
	if len(cf.cnames) != 0 {
		t.Fatalf("admin CNAME should not be mutated before restart failure: %#v", cf.cnames)
	}
}

// rollbackDeletedAll reports whether every created A record appears in the
// deleted list. Order is not asserted so future rollback ordering changes
// don't break these tests.
func rollbackDeletedAll(created [][3]string, deleted [][2]string) bool {
	if len(created) == 0 || len(deleted) != len(created) {
		return false
	}
	for _, c := range created {
		found := false
		for _, d := range deleted {
			if d[0] == c[0] && d[1] == c[1] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestRunUpgradeRollsBackAndRestartsXrayAfterPostRestartFailure(t *testing.T) {
	dir := withUpgradeSeams(t)
	seedUpgradeConfig(t)
	origEnv, _ := os.ReadFile(envFilePath)
	origXray, _ := os.ReadFile(xrayConfigPath)
	if err := os.WriteFile(subscriptionDir, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-1"}}
	runner := &installRecorder{}
	deps := InstallDeps{CF: cf, IP: fakeInstallIP{ip: "203.0.113.42"}, Cert: &fakeInstallCert{cert: "/etc/cfvpn/certs/vpn.example.com/fullchain.pem", key: "/etc/cfvpn/certs/vpn.example.com/privkey.pem"}, UFW: &fakeUFW{}, SystemdRunner: runner}
	_, err := RunUpgrade(context.Background(), UpgradeInputs{BackupRoot: dir, Now: func() time.Time { return time.Unix(6789, 0) }}, deps, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "write subscription") {
		t.Fatalf("expected subscription failure, got %v", err)
	}
	gotEnv, _ := os.ReadFile(envFilePath)
	gotXray, _ := os.ReadFile(xrayConfigPath)
	if !bytes.Equal(origEnv, gotEnv) || !bytes.Equal(origXray, gotXray) {
		t.Fatalf("config not restored after rollback")
	}
	if got := runner.countJoined("systemctl restart cfvpn-xray.service"); got != 2 {
		t.Fatalf("xray restart count = %d, want 2; calls=%#v", got, runner.calls)
	}
	if len(cf.cnames) != 0 {
		t.Fatalf("admin CNAME should not be mutated before subscription failure: %#v", cf.cnames)
	}
}

// seedUpgradeTrojanConfig seeds a Trojan-era config (no HY2 fields) for P1 tests.
func seedUpgradeTrojanConfig(t *testing.T) {
	t.Helper()
	if err := state.SaveAtomic(envFilePath, map[string]string{
		"CF_API_TOKEN":      "t",
		"CF_ACCOUNT_ID":     "a",
		"DOMAIN":            "proxied.example.com",
		"TUNNEL_UUID":       "old-tun",
		"NODE_ID":           "JPY-04",
		"USER1_NAME":        "alice",
		"UUID_USER1":        "u-1",
		"TROJAN_PASS_USER1": "oldpw",
	}, 0o600); err != nil {
		t.Fatal(err)
	}
	// Trojan-era xray config (VLESS-only, since K1 already removed Trojan inbound).
	rendered, err := templates.RenderXrayDirect(templates.XrayDirectInputs{
		Users: []templates.XrayUser{{Name: "alice", UUID: "u-1"}},
		Certs: []templates.XrayCert{{Zone: "example.com", CertFile: "/old/cert", KeyFile: "/old/key"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicFile(xrayConfigPath, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicFile(cloudflaredConfig, []byte("old tunnel config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunUpgradeWithTrojanBackfillsHY2FieldsAndMigratesPassword(t *testing.T) {
	dir := withUpgradeSeams(t)
	seedUpgradeTrojanConfig(t)
	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-1", adminHostZone: "admin-zone"}}
	cert := &fakeInstallCert{}
	udp := &fakeUDPProber{}
	runner := &installRecorder{}
	deps := baseInstallDeps(cf)
	deps.Cert = cert
	deps.UDPProber = udp
	deps.SystemdRunner = runner
	deps.Random = bytes.NewReader(append([]byte{0, 0, 0, 1, 0, 2}, bytes.Repeat([]byte{3}, 64)...))
	var out, errBuf bytes.Buffer
	_, runErr := RunUpgrade(context.Background(), UpgradeInputs{BackupRoot: dir, Now: func() time.Time { return time.Unix(9001, 0) }}, deps, &out, &errBuf)
	if runErr != nil {
		t.Fatalf("RunUpgrade: %v", runErr)
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		t.Fatal(err)
	}

	// HY2 password migrated from TROJAN_PASS_USER1.
	if env["HY2_PASS_USER1"] != "oldpw" {
		t.Fatalf("HY2_PASS_USER1 = %q, want migrated 'oldpw'", env["HY2_PASS_USER1"])
	}

	// TROJAN_PASS_USER1 deleted.
	if _, ok := env["TROJAN_PASS_USER1"]; ok {
		t.Fatalf("TROJAN_PASS_USER1 still present in env: %#v", env)
	}

	// HY2 fields present.
	for _, k := range []string{"HY2_HOST", "HY2_PORT", "HY2_OBFS_PW"} {
		if env[k] == "" {
			t.Fatalf("HY2 backfill missing %s", k)
		}
	}

	// HY2 host generated in example.com zone.
	hostPattern := regexp.MustCompile(`^(quic|udp|hy)-[0-9a-f]{8}\.example\.com$`)
	if !hostPattern.MatchString(env["HY2_HOST"]) {
		t.Fatalf("HY2_HOST = %q, want generated hy2 host", env["HY2_HOST"])
	}

	// HY2 port in valid range and was probed.
	hy2Port, _ := strconv.Atoi(env["HY2_PORT"])
	if hy2Port < 20000 || hy2Port > 60000 {
		t.Fatalf("HY2_PORT = %d, want in [20000,60000]", hy2Port)
	}
	if len(udp.ports) == 0 {
		t.Fatalf("UDP prober not called; ports = %#v", udp.ports)
	}

	// Hysteria config written with correct structure.
	hyRaw, err := os.ReadFile(hysteriaConfigPath)
	if err != nil {
		t.Fatalf("hysteria config missing: %v", err)
	}
	for _, want := range []string{
		"tls:", "cert:", "key:", "obfs:",
		"salamander", "password:", "alice",
	} {
		if !strings.Contains(string(hyRaw), want) {
			t.Fatalf("hysteria config missing %q:\n%s", want, string(hyRaw))
		}
	}
	// Password in hysteria config must be the migrated one.
	if !strings.Contains(string(hyRaw), "oldpw") {
		t.Fatalf("hysteria config missing migrated password 'oldpw':\n%s", string(hyRaw))
	}

	// HY2 cert issued.
	if len(cert.issues) == 0 || cert.issues[0].host != env["HY2_HOST"] {
		t.Fatalf("HY2 cert not issued: %#v", cert.issues)
	}

	// Systemd units written and enabled.
	for _, unit := range []string{"cfvpn-hysteria.service", "cfvpn-cert-renew.service", "cfvpn-cert-renew.timer"} {
		p := filepath.Join(systemdUnitDir, unit)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("systemd unit %s not written: %v", unit, err)
		}
	}
	for _, svc := range []string{"cfvpn-hysteria.service", "cfvpn-cert-renew.timer"} {
		found := false
		for _, call := range runner.calls {
			if strings.Join(call, " ") == "systemctl enable --now "+svc {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not enabled; calls = %#v", svc, runner.calls)
		}
	}

	// Xray config is still VLESS-only (RenderXrayDirect already drops Trojan).
	xrayRaw, _ := os.ReadFile(xrayConfigPath)
	if !strings.Contains(string(xrayRaw), "u-1") {
		t.Fatalf("xray config lost user credentials: %s", xrayRaw)
	}

	// Env saved with MODE=direct and HY2 fields.
	envS := string(envRaw(t))
	for _, want := range []string{
		"MODE=direct",
		"HY2_HOST=" + env["HY2_HOST"],
		"HY2_PORT=" + env["HY2_PORT"],
		"HY2_PASS_USER1=oldpw",
	} {
		if !strings.Contains(envS, want) {
			t.Fatalf("env missing %q after HY2 backfill", want)
		}
	}

	// DNS A record must be created for the HY2 host so clients can resolve it;
	// without this, hysteria connections time out.
	foundHy2 := false
	for _, rec := range cf.aRecords {
		if rec[0] == "zone-1" && rec[1] == env["HY2_HOST"] {
			foundHy2 = true
			break
		}
	}
	if !foundHy2 {
		t.Fatalf("HY2 host A record not upserted; aRecords = %#v, HY2_HOST = %q", cf.aRecords, env["HY2_HOST"])
	}
}

func envRaw(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRunUpgradeWithExistingHY2IsIdempotent(t *testing.T) {
	dir := withUpgradeSeams(t)
	// Seed with existing HY2 fields (simulating a node already running HY2).
	if err := state.SaveAtomic(envFilePath, map[string]string{
		"CF_API_TOKEN":   "t",
		"CF_ACCOUNT_ID":  "a",
		"DOMAIN":         "vpn.example.com",
		"NODE_ID":        "JPY-04",
		"USER1_NAME":     "alice",
		"UUID_USER1":     "u-1",
		"HY2_HOST":       "hy2.example.com",
		"HY2_PORT":       "21000",
		"HY2_OBFS_PW":    "existing-obfs",
		"HY2_PASS_USER1": "existing-hy2pw",
		"TUNNEL_UUID":    "tun-existing",
	}, 0o600); err != nil {
		t.Fatal(err)
	}
	rendered, err := templates.RenderXrayDirect(templates.XrayDirectInputs{
		Users: []templates.XrayUser{{Name: "alice", UUID: "u-1"}},
		Certs: []templates.XrayCert{{Zone: "example.com", CertFile: "/etc/cfvpn/xray/cert.pem", KeyFile: "/etc/cfvpn/xray/key.pem"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicFile(xrayConfigPath, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicFile(cloudflaredConfig, []byte("cloudflared config\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Capture original env for comparison.
	origEnv, _ := os.ReadFile(envFilePath)

	cf := &fakeInstallCF{zones: map[string]string{"example.com": "zone-1", adminHostZone: "admin-zone"}}
	runner := &installRecorder{}
	deps := InstallDeps{
		CF:            cf,
		IP:            fakeInstallIP{ip: "203.0.113.42"},
		Cert:          &fakeInstallCert{},
		UFW:           &fakeUFW{},
		UDPProber:     &fakeUDPProber{},
		SystemdRunner: runner,
	}
	var out, errBuf bytes.Buffer
	res, err := RunUpgrade(context.Background(), UpgradeInputs{BackupRoot: dir, Now: func() time.Time { return time.Unix(9999, 0) }}, deps, &out, &errBuf)
	if err != nil {
		t.Fatalf("RunUpgrade with existing HY2: %v", err)
	}

	// Env unchanged (idempotent).
	afterEnv, _ := os.ReadFile(envFilePath)
	if !bytes.Equal(origEnv, afterEnv) {
		t.Fatalf("idempotent re-run changed env:\nbefore:\n%s\nafter:\n%s", origEnv, afterEnv)
	}

	// Idempotent: HY2 fields preserved, env unchanged.
	for _, k := range []string{"HY2_HOST=hy2.example.com", "HY2_PORT=21000", "HY2_PASS_USER1=existing-hy2pw"} {
		if !strings.Contains(string(afterEnv), k) {
			t.Fatalf("idempotent re-run lost HY2 field %q", k)
		}
	}

	// Skipped flag set.
	if !res.Skipped {
		t.Fatalf("res.Skipped = false, want true for existing HY2")
	}

	// Stdout shows no-op message.
	if !strings.Contains(out.String(), "HY2 already configured") {
		t.Fatalf("stdout missing idempotent no-op message: %s", out.String())
	}
}
