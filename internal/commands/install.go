package commands

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kulinh/cf-vpn/internal/binary"
	"github.com/kulinh/cf-vpn/internal/cert"
	"github.com/kulinh/cf-vpn/internal/hysteria"
	"github.com/kulinh/cf-vpn/internal/netcheck"
	"github.com/kulinh/cf-vpn/internal/netinfo"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/subscription"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/xray"
	"github.com/kulinh/cf-vpn/internal/zones"
)

const adminHostZone = "rwl247.dev"

// InstallInputs carries the user-provided inputs to install.
type InstallInputs struct {
	CFAPIToken   string
	CFAccountID  string
	Domain       string
	NodeID       string
	User1Name    string
	Mode         string
	Hy2Host      string
	Hy2Port      string
	Hy2ObfsPW    string
	Hy2PassUser1 string
	// XrayDNSServers is the optional comma-separated resolver override from
	// XRAY_DNS_SERVERS. Empty means the international DoH default.
	XrayDNSServers string
}

// InstallCFClient is the Cloudflare dependency required by RunInstall.
type InstallCFClient interface {
	GetZoneID(ctx context.Context, domain string) (string, error)
	CreateTunnel(ctx context.Context, name string) (id string, creds []byte, err error)
	UpsertCNAME(ctx context.Context, zoneID, name, target string) error
	UpsertARecord(ctx context.Context, zoneID, name, ip string) error
	DeleteARecordByName(ctx context.Context, zoneID, name string) error
	DeleteTunnel(ctx context.Context, id string) error
}

type UFWRunner interface {
	Allow(ctx context.Context, rule string) error
}

type PortProber interface {
	Probe(ctx context.Context) error
}

type UDPProber interface {
	ProbeUDP(ctx context.Context, port int) error
}

// InstallDeps are injected collaborators for RunInstall.
type InstallDeps struct {
	CF            InstallCFClient
	IP            IPDetector
	Cert          cert.Manager
	UFW           UFWRunner
	PortProber    PortProber
	UDPProber     UDPProber
	Random        io.Reader
	BinaryRunner  binary.Runner
	SystemdRunner systemd.Runner
}

// resolveBinaryRunner returns r or a default ExecRunner when nil. The
// systemd.ExecRunner satisfies binary.Runner because the interfaces share an
// identical Run(ctx, name, args...) error method.
func resolveBinaryRunner(r binary.Runner) binary.Runner {
	if r == nil {
		return systemd.ExecRunner{}
	}
	return r
}

func ensureRuntimeBinaries(ctx context.Context, runner binary.Runner) error {
	if err := binary.EnsureXray(ctx, runner, binary.Exists("xray")); err != nil {
		return fmt.Errorf("ensure xray: %w", err)
	}
	if err := binary.EnsureCloudflared(ctx, runner, binary.Exists("cloudflared")); err != nil {
		return fmt.Errorf("ensure cloudflared: %w", err)
	}
	if err := binary.EnsureHysteria(ctx, runner, binary.Exists("hysteria")); err != nil {
		return fmt.Errorf("ensure hysteria: %w", err)
	}
	if err := binary.EnsureLego(ctx, runner, binary.Exists("lego")); err != nil {
		return fmt.Errorf("ensure lego: %w", err)
	}
	return nil
}

// hysteriaConfigPath is defined in rotate.go to share the constant with
// rotateHy2Config. It is package-level so tests can redirect it.
// systemdUnitDir is defined in healthcheck.go.

type UpgradeInputs struct {
	BackupRoot string
	Mode       string
	Now        func() time.Time
}

type UpgradeResult struct {
	OldHost  string
	NewHost  string
	PublicIP string
	Skipped  bool // true when HY2 was already present (idempotent no-op)
}

func RunUpgradeCheck(ctx context.Context, in UpgradeInputs, deps InstallDeps, stdout, stderr io.Writer) error {
	env, err := loadUpgradeEnv()
	if err != nil {
		return err
	}
	if in.BackupRoot == "" {
		in.BackupRoot = "/etc"
	}
	if deps.CF == nil {
		return fmt.Errorf("cloudflare client is required")
	}
	if deps.IP == nil {
		deps.IP = netinfo.NewDefault()
	}
	if zoneOfDomain(env["DOMAIN"]) == "" {
		return fmt.Errorf("resolve zone for %s: invalid domain", env["DOMAIN"])
	}
	if _, err := deps.CF.GetZoneID(ctx, env["DOMAIN"]); err != nil {
		return fmt.Errorf("get zone id for %s: %w", env["DOMAIN"], err)
	}
	ip, err := deps.IP.Detect(ctx)
	if err != nil {
		return fmt.Errorf("detect public ip: %w", err)
	}
	if err := validateIPv4(ip); err != nil {
		return err
	}
	if _, err := usersFromCurrentXray(); err != nil {
		return err
	}
	cfgDir := filepath.Dir(envFilePath)
	if st, err := os.Stat(cfgDir); err != nil {
		return fmt.Errorf("read config dir: %w", err)
	} else if !st.IsDir() {
		return fmt.Errorf("read config dir: %s is not a directory", cfgDir)
	}
	if err := os.MkdirAll(in.BackupRoot, 0o755); err != nil {
		return fmt.Errorf("access backup root: %w", err)
	}
	if stdout != nil {
		fmt.Fprintln(stdout, "pre-flight OK; ready to run cfvpnctl install --upgrade")
	}
	return nil
}

func RunUpgrade(ctx context.Context, in UpgradeInputs, deps InstallDeps, stdout, stderr io.Writer) (UpgradeResult, error) {
	if in.Now == nil {
		in.Now = time.Now
	}
	if in.BackupRoot == "" {
		in.BackupRoot = "/etc"
	}
	if in.Mode == "" {
		in.Mode = "direct"
	}
	if in.Mode == "auto" {
		in.Mode = netcheck.SuggestMode()
		fmt.Fprintf(stdout, "auto-detected mode: %s\n", in.Mode)
	}
	if in.Mode != "direct" && in.Mode != "cloudflare" {
		return UpgradeResult{}, fmt.Errorf("MODE must be direct or cloudflare")
	}
	if deps.CF == nil {
		return UpgradeResult{}, fmt.Errorf("cloudflare client is required")
	}
	if deps.IP == nil {
		deps.IP = netinfo.NewDefault()
	}
	if deps.Cert == nil {
		deps.Cert = cert.NewDefault()
	}
	if deps.UFW == nil {
		deps.UFW = NewExecUFW()
	}
	if deps.UDPProber == nil {
		deps.UDPProber = UDPListenProber{}
	}
	rng := deps.Random
	if rng == nil {
		rng = rand.Reader
	}
	binRunner := resolveBinaryRunner(deps.BinaryRunner)
	if err := ensureRuntimeBinaries(ctx, binRunner); err != nil {
		return UpgradeResult{}, err
	}

	env, err := loadUpgradeEnv()
	if err != nil {
		return UpgradeResult{}, err
	}

	if env["HY2_HOST"] != "" {
		currentMode := env["MODE"]
		if currentMode == "" {
			currentMode = "direct"
		}
		if currentMode == in.Mode {
			// Same-mode upgrade: don't rotate the host (would burn a new
			// domain + leak DNS), but DO re-render xray + cloudflared from
			// the latest templates and restart if anything changed.
			return reRenderInPlace(ctx, in, deps, env, stdout, stderr)
		}
		return runUpgradeCore(ctx, in, deps, env, rng, stdout, stderr)
	}

	// --- HY2 backfill: generate and persist missing HY2 fields ---
	zone := zoneOfDomain(env["DOMAIN"])
	if zone == "" {
		return UpgradeResult{}, fmt.Errorf("resolve zone for %s: invalid domain", env["DOMAIN"])
	}

	hy2Host, err := zones.GenerateHy2Host(rng, zone)
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("generate hy2 host: %w", err)
	}

	hy2Port, err := pickHy2UDPPort(ctx, rng, deps.UDPProber)
	if err != nil {
		return UpgradeResult{}, err
	}

	hy2ObfsPW, err := generatePasswordFrom(rng, 24)
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("generate hy2 obfs password: %w", err)
	}

	// Migrate password: TROJAN_PASS_USER1 -> HY2_PASS_USER1
	var hy2PassUser1 string
	if oldPW := env["TROJAN_PASS_USER1"]; oldPW != "" {
		hy2PassUser1 = oldPW
	} else {
		hy2PassUser1, err = generatePasswordFrom(rng, 24)
		if err != nil {
			return UpgradeResult{}, fmt.Errorf("generate hy2 user password: %w", err)
		}
	}

	// Issue HY2 certificate and write hysteria config.
	hy2CertPath, hy2KeyPath := HysteriaCertPaths()
	if err := deps.Cert.Issue(ctx, hy2Host, hy2CertPath, hy2KeyPath, env["CF_API_TOKEN"]); err != nil {
		return UpgradeResult{}, fmt.Errorf("issue cert for %s: %w", hy2Host, err)
	}

	// Get existing user name for hysteria config.
	userName := env["USER1_NAME"]
	if userName == "" {
		userName = "alice"
	}

	// Read existing users from xray config to populate hysteria config.
	// ListUserNames returns canonical bare names (the xray package strips the
	// internal "@vpn" suffix) so a direct compare against userName is correct.
	xrayCfg, _ := xray.Load(xrayConfigPath)
	var hysteriaUsers []templates.HysteriaUser
	for _, name := range xray.ListUserNames(xrayCfg) {
		var pass string
		if name == userName && hy2PassUser1 != "" {
			pass = hy2PassUser1
		} else {
			pass, _ = generatePasswordFrom(rng, 24)
		}
		hysteriaUsers = append(hysteriaUsers, templates.HysteriaUser{Name: name, Password: pass})
	}
	if len(hysteriaUsers) == 0 {
		hysteriaUsers = append(hysteriaUsers, templates.HysteriaUser{Name: userName, Password: hy2PassUser1})
	}

	hyInputs := templates.HysteriaInputs{
		Listen:   ":" + strconv.Itoa(hy2Port),
		TLSCert:  hy2CertPath,
		TLSKey:   hy2KeyPath,
		ObfsPW:   hy2ObfsPW,
		UpMbps:   100,
		DownMbps: 100,
		Users:    hysteriaUsers,
	}
	hyBody, err := templates.RenderHysteriaConfig(hyInputs)
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("render hysteria config: %w", err)
	}
	if err := writeAtomicFile(hysteriaConfigPath, hyBody, 0o600); err != nil {
		return UpgradeResult{}, fmt.Errorf("write hysteria config: %w", err)
	}

	// Persist HY2 fields into env (but defer saving until runUpgradeCore).
	env["HY2_HOST"] = hy2Host
	env["HY2_PORT"] = strconv.Itoa(hy2Port)
	env["HY2_OBFS_PW"] = hy2ObfsPW
	env["HY2_PASS_USER1"] = hy2PassUser1
	delete(env, "TROJAN_PASS_USER1")

	return runUpgradeCore(ctx, in, deps, env, rng, stdout, stderr)
}

// runUpgradeCore is the inner upgrade path that assumes HY2 env fields are already present or being added atomically.
func runUpgradeCore(ctx context.Context, in UpgradeInputs, deps InstallDeps, env map[string]string, rng io.Reader, stdout, stderr io.Writer) (UpgradeResult, error) {
	oldHost := env["DOMAIN"]
	zone := zoneOfDomain(oldHost)
	if zone == "" {
		return UpgradeResult{}, fmt.Errorf("resolve zone for %s: invalid domain", oldHost)
	}
	adminHost := env["ADMIN_HOST"]
	if env["NODE_ID"] != "" {
		var err error
		adminHost, err = generateAdminHost(env["NODE_ID"])
		if err != nil {
			return UpgradeResult{}, err
		}
	}
	if adminHost == "" {
		return UpgradeResult{}, fmt.Errorf("NODE_ID or ADMIN_HOST is required")
	}
	zoneID, err := deps.CF.GetZoneID(ctx, zone)
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("get zone id for %s: %w", zone, err)
	}
	backupDir := filepath.Join(in.BackupRoot, fmt.Sprintf("cfvpn.backup-%d", in.Now().Unix()))
	cfgDir := filepath.Dir(envFilePath)
	if err := copyTree(cfgDir, backupDir); err != nil {
		return UpgradeResult{}, fmt.Errorf("backup config: %w", err)
	}
	runner := resolveRunner(deps.SystemdRunner)
	rb := &rollbacker{cf: deps.CF, runner: runner, backupDir: backupDir, configDir: cfgDir}
	fail := func(e error) (UpgradeResult, error) { rb.run(ctx, stderr); return UpgradeResult{}, e }

	newHost := oldHost
	if in.Mode == "direct" {
		newHost, err = zones.GenerateHost(rng, zone)
		if err != nil {
			return fail(fmt.Errorf("generate host: %w", err))
		}
	}
	ip, err := deps.IP.Detect(ctx)
	if err != nil {
		return fail(fmt.Errorf("detect public ip: %w", err))
	}
	ip = strings.TrimSpace(ip)
	if err := validateIPv4(ip); err != nil {
		return fail(err)
	}
	users, err := usersFromCurrentXray()
	if err != nil {
		return fail(err)
	}

	var realityParams xray.RealityParams
	var xrayRendered string
	if in.Mode == "direct" {
		var ok bool
		realityParams, ok = loadRealityFromEnv(env)
		if !ok {
			realityParams, err = xray.GenerateRealityParams(xray.GenerateRealityOptions{})
			if err != nil {
				return fail(fmt.Errorf("generate reality params: %w", err))
			}
		}
		xrayRendered, err = templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
			Users:       users,
			PrivateKey:  realityParams.PrivateKey,
			ShortIDs:    []string{realityParams.ShortID},
			Dest:        realityParams.Dest,
			ServerNames: []string{realityParams.SNI},
			DNSServers:  xrayDNSServersFromEnv(env),
		})
		if err != nil {
			return fail(fmt.Errorf("render xray reality config: %w", err))
		}
	} else {
		xrayRendered, err = templates.RenderXrayCloudflareHTTPUpgrade(users, newHost, xrayDNSServersFromEnv(env))
		if err != nil {
			return fail(fmt.Errorf("render xray cloudflare config: %w", err))
		}
	}
	if err := writeAtomicFile(xrayConfigPath, []byte(xrayRendered), 0o600); err != nil {
		return fail(fmt.Errorf("write xray config: %w", err))
	}

	oldTunnel := env["ADMIN_TUNNEL_UUID"]
	if oldTunnel == "" {
		oldTunnel = env["TUNNEL_UUID"]
	}
	var cfRendered string
	if in.Mode == "direct" {
		cfRendered, err = templates.RenderCloudflaredAdmin(oldTunnel, adminHost)
		if err != nil {
			return fail(fmt.Errorf("render cloudflared admin config: %w", err))
		}
	} else {
		cfRendered, err = templates.RenderCloudflaredWithAdmin(oldTunnel, newHost, adminHost)
		if err != nil {
			return fail(fmt.Errorf("render cloudflared config: %w", err))
		}
	}
	if err := writeAtomicFile(cloudflaredConfig, []byte(cfRendered), 0o600); err != nil {
		return fail(fmt.Errorf("write cloudflared config: %w", err))
	}

	if in.Mode == "direct" {
		if err := deps.UFW.Allow(ctx, "443/tcp"); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: ufw allow 443/tcp failed: %v\n", err)
		}
		if err := deps.CF.UpsertARecord(ctx, zoneID, newHost, ip); err != nil {
			return fail(fmt.Errorf("upsert dns a record: %w", err))
		}
		rb.addCreatedA(zoneID, newHost)
	} else if err := deps.CF.UpsertCNAME(ctx, zoneID, newHost, oldTunnel+".cfargotunnel.com"); err != nil {
		return fail(fmt.Errorf("upsert vpn dns cname: %w", err))
	}
	if hy2Host := env["HY2_HOST"]; hy2Host != "" {
		if err := deps.CF.UpsertARecord(ctx, zoneID, hy2Host, ip); err != nil {
			return fail(fmt.Errorf("upsert hy2 dns a record: %w", err))
		}
		rb.addCreatedA(zoneID, hy2Host)
	}
	if err := systemd.Restart(ctx, runner, "cfvpn-xray.service"); err != nil {
		return fail(fmt.Errorf("restart cfvpn-xray.service: %w", err))
	}
	rb.markServiceMutated("cfvpn-xray.service")
	if err := systemd.Restart(ctx, runner, "cfvpn-cloudflared.service"); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "warning: restart cfvpn-cloudflared.service failed: %v\n", err)
	} else if err == nil {
		rb.markServiceMutated("cfvpn-cloudflared.service")
	}
	if err := hysteria.ReloadService(ctx, runner); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "warning: restart cfvpn-hysteria.service failed: %v\n", err)
	} else if err == nil {
		rb.markServiceMutated("cfvpn-hysteria.service")
	}
	env["DOMAIN"] = newHost
	env["MODE"] = in.Mode
	env["PUBLIC_IP"] = ip
	env["ADMIN_HOST"] = adminHost
	env["ADMIN_TUNNEL_UUID"] = oldTunnel
	delete(env, "TUNNEL_UUID")
	if in.Mode == "direct" {
		env[state.KeyRealityPriv] = realityParams.PrivateKey
		env[state.KeyRealityPub] = realityParams.PublicKey
		env[state.KeyRealityShortID] = realityParams.ShortID
		env[state.KeyRealityDest] = realityParams.Dest
		env[state.KeyRealitySNI] = realityParams.SNI
	} else {
		env[state.KeyXHTTPPath] = templates.VLESSPath
	}
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		return fail(fmt.Errorf("save env: %w", err))
	}
	if err := RegenerateSubscriptions(newHost); err != nil {
		return fail(err)
	}
	adminZoneID, err := deps.CF.GetZoneID(ctx, adminHostZone)
	if err != nil {
		return fail(fmt.Errorf("get zone id for %s: %w", adminHostZone, err))
	}
	if err := deps.CF.UpsertCNAME(ctx, adminZoneID, adminHost, oldTunnel+".cfargotunnel.com"); err != nil {
		return fail(fmt.Errorf("upsert admin dns cname: %w", err))
	}

	units := map[string]string{
		"cfvpn-hysteria.service":   systemd.HysteriaService(hysteriaConfigPath),
		"cfvpn-cert-renew.service": systemd.CertRenewService(),
		"cfvpn-cert-renew.timer":   systemd.CertRenewTimer(),
	}
	for name, content := range units {
		if err := writeAtomicFile(filepath.Join(systemdUnitDir, name), []byte(content), 0o644); err != nil {
			return fail(fmt.Errorf("write %s: %w", name, err))
		}
	}
	if err := systemd.DaemonReload(ctx, runner); err != nil {
		return fail(fmt.Errorf("systemctl daemon-reload: %w", err))
	}
	for _, svc := range []string{"cfvpn-hysteria.service", "cfvpn-cert-renew.timer"} {
		if err := systemd.EnableNow(ctx, runner, svc); err != nil {
			return fail(fmt.Errorf("enable %s: %w", svc, err))
		}
	}

	hy2Port := env["HY2_PORT"]
	if hy2Port != "" {
		if err := deps.UFW.Allow(ctx, hy2Port+"/udp"); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: ufw allow %s/udp failed: %v\n", hy2Port, err)
		}
	}

	if stdout != nil {
		fmt.Fprintf(stdout, "upgrade complete: %s -> %s mode %s (%s)\n", oldHost, newHost, in.Mode, ip)
	}
	return UpgradeResult{OldHost: oldHost, NewHost: newHost, PublicIP: ip}, nil
}

// reRenderInPlace re-renders xray + cloudflared configs against the current
// env (no DNS, no host change, no HY2 mutations) and restarts services only
// if the rendered config differs from what's on disk. Used by RunUpgrade
// when the requested mode equals the current mode — a re-deploy of the
// binary without rotating the public domain.
func reRenderInPlace(ctx context.Context, in UpgradeInputs, deps InstallDeps, env map[string]string, stdout, stderr io.Writer) (UpgradeResult, error) {
	domain := env["DOMAIN"]
	adminHost := env["ADMIN_HOST"]
	tunnelUUID := env["ADMIN_TUNNEL_UUID"]
	if tunnelUUID == "" {
		tunnelUUID = env["TUNNEL_UUID"]
	}
	users, err := usersFromCurrentXray()
	if err != nil {
		return UpgradeResult{}, err
	}

	var xrayRendered string
	if in.Mode == "direct" {
		realityParams, ok := loadRealityFromEnv(env)
		if !ok {
			// Legacy direct node without Reality; nothing safe to re-render.
			if stdout != nil {
				fmt.Fprintln(stdout, "in-place re-render skipped: legacy direct node without Reality params")
			}
			return UpgradeResult{OldHost: domain, NewHost: domain, PublicIP: env["PUBLIC_IP"], Skipped: true}, nil
		}
		xrayRendered, err = templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
			Users:       users,
			PrivateKey:  realityParams.PrivateKey,
			ShortIDs:    []string{realityParams.ShortID},
			Dest:        realityParams.Dest,
			ServerNames: []string{realityParams.SNI},
			DNSServers:  xrayDNSServersFromEnv(env),
		})
		if err != nil {
			return UpgradeResult{}, fmt.Errorf("render xray reality config: %w", err)
		}
	} else {
		xrayRendered, err = templates.RenderXrayCloudflareHTTPUpgrade(users, domain, xrayDNSServersFromEnv(env))
		if err != nil {
			return UpgradeResult{}, fmt.Errorf("render xray cloudflare config: %w", err)
		}
	}

	var cfRendered string
	if in.Mode == "direct" {
		cfRendered, err = templates.RenderCloudflaredAdmin(tunnelUUID, adminHost)
	} else {
		cfRendered, err = templates.RenderCloudflaredWithAdmin(tunnelUUID, domain, adminHost)
	}
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("render cloudflared config: %w", err)
	}

	runner := resolveRunner(deps.SystemdRunner)
	xrayChanged, err := writeIfChanged(xrayConfigPath, []byte(xrayRendered), 0o600)
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("write xray config: %w", err)
	}
	cfChanged, err := writeIfChanged(cloudflaredConfig, []byte(cfRendered), 0o600)
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("write cloudflared config: %w", err)
	}

	if xrayChanged {
		if err := systemd.Restart(ctx, runner, "cfvpn-xray.service"); err != nil {
			return UpgradeResult{}, fmt.Errorf("restart cfvpn-xray.service: %w", err)
		}
	}
	if cfChanged {
		if err := systemd.Restart(ctx, runner, "cfvpn-cloudflared.service"); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: restart cfvpn-cloudflared.service failed: %v\n", err)
		}
	}

	if stdout != nil {
		switch {
		case xrayChanged && cfChanged:
			fmt.Fprintln(stdout, "in-place re-render: xray + cloudflared updated")
		case xrayChanged:
			fmt.Fprintln(stdout, "in-place re-render: xray updated")
		case cfChanged:
			fmt.Fprintln(stdout, "in-place re-render: cloudflared updated")
		default:
			fmt.Fprintln(stdout, "in-place re-render: configs already up to date")
		}
	}

	// Self-heal drifted systemd units. The installer only writes unit files at
	// first install and never regenerates them on binary upgrade, so an
	// in-place re-deploy is the natural point to bring stale units (e.g. an old
	// cert-renew ExecStart) back in line with the templates.
	reconcileOut := stdout
	if reconcileOut == nil {
		reconcileOut = io.Discard
	}
	if err := RunReconcileUnits(ctx, runner, reconcileOut); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "warning: reconcile systemd units failed: %v\n", err)
	}

	return UpgradeResult{
		OldHost:  domain,
		NewHost:  domain,
		PublicIP: env["PUBLIC_IP"],
		Skipped:  !xrayChanged && !cfChanged,
	}, nil
}

// writeIfChanged writes content to path only if it differs from current
// on-disk content (or path doesn't exist). Returns true if a write occurred.
func writeIfChanged(path string, content []byte, mode os.FileMode) (bool, error) {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, content) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, writeAtomicFile(path, content, mode)
}

func loadUpgradeEnv() (map[string]string, error) {
	env, err := state.Load(envFilePath)
	if err != nil {
		return nil, fmt.Errorf("load env: %w", err)
	}
	for _, k := range []string{"CF_API_TOKEN", "CF_ACCOUNT_ID", "DOMAIN"} {
		if env[k] == "" {
			return nil, fmt.Errorf("CF_API_TOKEN, CF_ACCOUNT_ID, and DOMAIN are required")
		}
	}
	if env["ADMIN_TUNNEL_UUID"] == "" && env["TUNNEL_UUID"] == "" {
		return nil, fmt.Errorf("ADMIN_TUNNEL_UUID or TUNNEL_UUID is required")
	}
	return env, nil
}

func validateIPv4(ip string) error {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || !addr.Is4() {
		return fmt.Errorf("detect public ip: expected IPv4 address, got %q", strings.TrimSpace(ip))
	}
	return nil
}

func loadRealityFromEnv(env map[string]string) (xray.RealityParams, bool) {
	p := xray.RealityParams{
		PrivateKey: env[state.KeyRealityPriv],
		PublicKey:  env[state.KeyRealityPub],
		ShortID:    env[state.KeyRealityShortID],
		Dest:       env[state.KeyRealityDest],
		SNI:        env[state.KeyRealitySNI],
	}
	return p, p.PrivateKey != "" && p.ShortID != ""
}

func usersFromCurrentXray() ([]templates.XrayUser, error) {
	cfg, err := xray.Load(xrayConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load xray config: %w", err)
	}
	var users []templates.XrayUser
	for _, name := range xray.ListUserNames(cfg) {
		uuid, ok := xray.GetVLESSClient(cfg, name)
		if !ok {
			return nil, fmt.Errorf("user %q has no vless client", name)
		}
		users = append(users, templates.XrayUser{Name: name, UUID: uuid})
	}
	return users, nil
}

type rollbacker struct {
	cf                   InstallCFClient
	runner               systemd.Runner
	createdA             [][2]string
	mutatedServices      []string
	backupDir, configDir string
}

func (r *rollbacker) addCreatedA(zoneID, name string) {
	r.createdA = append(r.createdA, [2]string{zoneID, name})
}

func (r *rollbacker) markServiceMutated(unit string) {
	for _, existing := range r.mutatedServices {
		if existing == unit {
			return
		}
	}
	r.mutatedServices = append(r.mutatedServices, unit)
}

func (r *rollbacker) run(ctx context.Context, stderr io.Writer) {
	for _, a := range r.createdA {
		if err := r.cf.DeleteARecordByName(ctx, a[0], a[1]); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: rollback delete A record %s failed: %v\n", a[1], err)
		}
	}
	restored := false
	if !safeRollbackConfigDir(r.configDir) {
		if stderr != nil {
			fmt.Fprintf(stderr, "warning: rollback skipped unsafe config dir %q\n", r.configDir)
		}
	} else {
		if err := os.RemoveAll(r.configDir); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: rollback remove config failed: %v\n", err)
		} else if err == nil {
			if err := copyTree(r.backupDir, r.configDir); err != nil && stderr != nil {
				fmt.Fprintf(stderr, "warning: rollback restore config failed: %v\n", err)
			} else if err == nil {
				restored = true
			}
		}
	}
	if restored && r.runner != nil {
		for _, unit := range r.mutatedServices {
			if err := systemd.Restart(ctx, r.runner, unit); err != nil && stderr != nil {
				fmt.Fprintf(stderr, "warning: rollback restart %s failed: %v\n", unit, err)
			}
		}
	}
}

func safeRollbackConfigDir(dir string) bool {
	if dir == "" || dir == "." || dir == string(filepath.Separator) || dir == "/etc" {
		return false
	}
	clean := filepath.Clean(dir)
	if clean != dir || clean == "." || clean == string(filepath.Separator) || clean == "/etc" {
		return false
	}
	if filepath.VolumeName(clean) == clean {
		return false
	}
	return filepath.Base(clean) == "cfvpn"
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(to, info.Mode().Perm())
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.WriteFile(to, data, info.Mode().Perm())
	})
}

// RunInstall performs the full install orchestration per section 6 of the
// standalone design spec.
func RunInstall(ctx context.Context, in InstallInputs, deps InstallDeps, stdout, stderr io.Writer) error {
	if in.Mode == "" || in.Mode == "auto" {
		in.Mode = netcheck.SuggestMode()
		fmt.Fprintf(stdout, "auto-detected mode: %s\n", in.Mode)
	}
	if in.Mode != "direct" && in.Mode != "cloudflare" {
		return fmt.Errorf("MODE must be direct or cloudflare")
	}
	if in.CFAPIToken == "" || in.CFAccountID == "" || in.User1Name == "" || in.NodeID == "" {
		return fmt.Errorf("CF_API_TOKEN, CF_ACCOUNT_ID, NODE_ID, and USER1_NAME are required")
	}
	adminHost, err := generateAdminHost(in.NodeID)
	if err != nil {
		return err
	}
	if deps.CF == nil {
		return fmt.Errorf("cloudflare client is required")
	}
	if deps.IP == nil {
		deps.IP = netinfo.NewDefault()
	}
	if deps.Cert == nil {
		deps.Cert = cert.NewDefault()
	}
	if deps.UFW == nil {
		deps.UFW = NewExecUFW()
	}
	if deps.PortProber == nil {
		deps.PortProber = TCP443Prober{}
	}
	if deps.UDPProber == nil {
		deps.UDPProber = UDPListenProber{}
	}
	rng := deps.Random
	if rng == nil {
		rng = rand.Reader
	}
	if in.Mode == "direct" {
		if err := deps.PortProber.Probe(ctx); err != nil {
			return fmt.Errorf("port_443_busy: %w", err)
		}
	}

	binRunner := resolveBinaryRunner(deps.BinaryRunner)
	sysRunner := resolveRunner(deps.SystemdRunner)
	domain := strings.TrimSpace(in.Domain)
	zone := ""
	zoneID := ""
	if domain != "" {
		zone = zoneOfDomain(domain)
		if zone == "" {
			return fmt.Errorf("resolve zone for %s: invalid domain", domain)
		}
	} else {
		picked, err := zones.PickZone(rng, zones.DefaultPool, "")
		if err != nil {
			return fmt.Errorf("pick zone: %w", err)
		}
		generated, err := zones.GenerateHost(rng, picked.Name)
		if err != nil {
			return fmt.Errorf("generate host: %w", err)
		}
		if _, err := deps.CF.GetZoneID(ctx, picked.Name); err != nil {
			return fmt.Errorf("zone %s not found via CF token; check internal/zones/pool.go matches the token's account: %w", picked.Name, err)
		}
		domain = generated
		zone = picked.Name
		zoneID = picked.CFZoneID
	}

	hy2Host := in.Hy2Host
	if hy2Host == "" {
		hy2Host, err = zones.GenerateHy2Host(rng, zone)
		if err != nil {
			return fmt.Errorf("generate hy2 host: %w", err)
		}
	}
	hy2Port := in.Hy2Port
	if hy2Port == "" {
		port, err := pickHy2UDPPort(ctx, rng, deps.UDPProber)
		if err != nil {
			return err
		}
		hy2Port = strconv.Itoa(port)
	} else if _, err := validateHy2Port(hy2Port); err != nil {
		return err
	}
	hy2ObfsPW := in.Hy2ObfsPW
	if hy2ObfsPW == "" {
		hy2ObfsPW, err = generatePasswordFrom(rng, 24)
		if err != nil {
			return fmt.Errorf("generate hy2 obfs password: %w", err)
		}
	}

	fmt.Fprintln(stdout, "ensuring binaries...")
	if err := ensureRuntimeBinaries(ctx, binRunner); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "issuing certificates...")
	hy2CertPath, hy2KeyPath := HysteriaCertPaths()
	if err := deps.Cert.Issue(ctx, hy2Host, hy2CertPath, hy2KeyPath, in.CFAPIToken); err != nil {
		return fmt.Errorf("issue cert for %s: %w", hy2Host, err)
	}

	fmt.Fprintln(stdout, "creating admin tunnel...")
	tunnelName := tunnelNameForNode(in.NodeID)
	if tunnelName == "" {
		return fmt.Errorf("derive tunnel name: NODE_ID %q is not a valid DNS label", in.NodeID)
	}
	tunnelID, creds, err := deps.CF.CreateTunnel(ctx, tunnelName)
	if err != nil {
		return fmt.Errorf("create tunnel: %w", err)
	}
	if tunnelID == "" {
		return fmt.Errorf("create tunnel: empty tunnel id")
	}
	credPath := filepath.Join(cloudflaredCredDir, tunnelID+".json")
	if err := writeAtomicFile(credPath, creds, 0o600); err != nil {
		printRotateHint(stdout, "cfvpnctl install", tunnelID)
		return fmt.Errorf("write tunnel credentials: %w", err)
	}

	fmt.Fprintln(stdout, "detecting public ip...")
	ip, err := deps.IP.Detect(ctx)
	if err != nil {
		printRotateHint(stdout, "cfvpnctl install", tunnelID)
		return fmt.Errorf("detect public ip: %w", err)
	}
	ip = strings.TrimSpace(ip)
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() {
		return fmt.Errorf("detect public ip: expected IPv4 address, got %q", ip)
	}

	userUUID, err := GenerateUUIDv4()
	if err != nil {
		return fmt.Errorf("generate uuid: %w", err)
	}
	hy2PassUser1 := in.Hy2PassUser1
	if hy2PassUser1 == "" {
		hy2PassUser1, err = GeneratePassword(24)
		if err != nil {
			return fmt.Errorf("generate hy2 user password: %w", err)
		}
	}

	var realityParams xray.RealityParams
	if in.Mode == "direct" {
		var err error
		realityParams, err = xray.GenerateRealityParams(xray.GenerateRealityOptions{})
		if err != nil {
			return fmt.Errorf("generate reality params: %w", err)
		}
		fmt.Fprintf(stdout, "generated Reality keypair (pub: %s)\n", realityParams.PublicKey)
	}

	fmt.Fprintln(stdout, "configuring dns...")
	if zoneID == "" {
		zone := zoneOfDomain(domain)
		var err error
		zoneID, err = deps.CF.GetZoneID(ctx, zone)
		if err != nil {
			printRotateHint(stdout, "cfvpnctl install", tunnelID)
			return fmt.Errorf("get zone id for %s: %w", zone, err)
		}
	}
	if in.Mode == "direct" {
		if err := deps.CF.UpsertARecord(ctx, zoneID, domain, ip); err != nil {
			return fmt.Errorf("upsert dns a record: %w", err)
		}
	} else {
		if err := deps.CF.UpsertCNAME(ctx, zoneID, domain, tunnelID+".cfargotunnel.com"); err != nil {
			return fmt.Errorf("upsert vpn dns cname: %w", err)
		}
	}
	adminZoneID, err := deps.CF.GetZoneID(ctx, adminHostZone)
	if err != nil {
		printRotateHint(stdout, "cfvpnctl install", tunnelID)
		return fmt.Errorf("get zone id for %s: %w", adminHostZone, err)
	}
	if err := deps.CF.UpsertCNAME(ctx, adminZoneID, adminHost, tunnelID+".cfargotunnel.com"); err != nil {
		return fmt.Errorf("upsert admin dns cname: %w", err)
	}

	fmt.Fprintln(stdout, "rendering configs...")
	users := []templates.XrayUser{{Name: in.User1Name, UUID: userUUID}}
	var xrayRendered string
	if in.Mode == "direct" {
		xrayRendered, err = templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
			Users:       users,
			PrivateKey:  realityParams.PrivateKey,
			ShortIDs:    []string{realityParams.ShortID},
			Dest:        realityParams.Dest,
			ServerNames: []string{realityParams.SNI},
			DNSServers:  xrayDNSServersCSV(in.XrayDNSServers),
		})
		if err != nil {
			return fmt.Errorf("render xray reality config: %w", err)
		}
	} else {
		xrayRendered, err = templates.RenderXrayCloudflareHTTPUpgrade(users, domain, xrayDNSServersCSV(in.XrayDNSServers))
		if err != nil {
			return fmt.Errorf("render xray cloudflare config: %w", err)
		}
	}
	if err := writeAtomicFile(xrayConfigPath, []byte(xrayRendered), 0o600); err != nil {
		return fmt.Errorf("write xray config: %w", err)
	}
	hyRendered, err := templates.RenderHysteriaConfig(templates.HysteriaInputs{Listen: ":" + hy2Port, TLSCert: hy2CertPath, TLSKey: hy2KeyPath, ObfsPW: hy2ObfsPW, UpMbps: 100, DownMbps: 100, Users: []templates.HysteriaUser{{Name: in.User1Name, Password: hy2PassUser1}}})
	if err != nil {
		return fmt.Errorf("render hysteria config: %w", err)
	}
	if err := writeAtomicFile(hysteriaConfigPath, hyRendered, 0o600); err != nil {
		return fmt.Errorf("write hysteria config: %w", err)
	}
	var cfRendered string
	if in.Mode == "direct" {
		cfRendered, err = templates.RenderCloudflaredAdmin(tunnelID, adminHost)
		if err != nil {
			return fmt.Errorf("render cloudflared admin config: %w", err)
		}
	} else {
		cfRendered, err = templates.RenderCloudflaredWithAdmin(tunnelID, domain, adminHost)
		if err != nil {
			return fmt.Errorf("render cloudflared config: %w", err)
		}
	}
	if err := writeAtomicFile(cloudflaredConfig, []byte(cfRendered), 0o600); err != nil {
		return fmt.Errorf("write cloudflared config: %w", err)
	}

	// Start from any existing env file so operator-supplied keys (notably
	// AGENT_SHARED_SECRET, which install-node.sh writes before invoking
	// `cfvpnctl install`) survive the rewrite.
	envMap, err := state.Load(envFilePath)
	if err != nil {
		envMap = map[string]string{}
	}
	envMap["CF_API_TOKEN"] = in.CFAPIToken
	envMap["CF_ACCOUNT_ID"] = in.CFAccountID
	envMap["NODE_ID"] = in.NodeID
	envMap["DOMAIN"] = domain
	envMap["USER1_NAME"] = in.User1Name
	envMap["MODE"] = in.Mode
	envMap["PUBLIC_IP"] = ip
	envMap["ADMIN_HOST"] = adminHost
	envMap["ADMIN_TUNNEL_UUID"] = tunnelID
	envMap["UUID_USER1"] = userUUID
	envMap["HY2_HOST"] = hy2Host
	envMap["HY2_PORT"] = hy2Port
	envMap["HY2_OBFS_PW"] = hy2ObfsPW
	envMap["HY2_PASS_USER1"] = hy2PassUser1
	if in.Mode == "direct" {
		envMap[state.KeyRealityPriv] = realityParams.PrivateKey
		envMap[state.KeyRealityPub] = realityParams.PublicKey
		envMap[state.KeyRealityShortID] = realityParams.ShortID
		envMap[state.KeyRealityDest] = realityParams.Dest
		envMap[state.KeyRealitySNI] = realityParams.SNI
	} else {
		envMap[state.KeyXHTTPPath] = templates.VLESSPath
	}
	if err := state.SaveAtomic(envFilePath, envMap, 0o600); err != nil {
		return fmt.Errorf("save env: %w", err)
	}

	fmt.Fprintln(stdout, "configuring dns (hy2)...")
	if err := deps.CF.UpsertARecord(ctx, zoneID, hy2Host, ip); err != nil {
		return fmt.Errorf("upsert hy2 dns a record: %w", err)
	}

	fmt.Fprintln(stdout, "installing systemd units...")
	// Single source of truth for the unit set (shared with reconcile) so a fresh
	// install can't drift from what reconcile expects — previously this map
	// omitted the healthcheck service+timer, leaving new nodes without it.
	for name, content := range canonicalUnits() {
		if err := writeAtomicFile(filepath.Join(systemdUnitDir, name), []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	if err := systemd.DaemonReload(ctx, sysRunner); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	for _, svc := range []string{"cfvpn-xray.service", "cfvpn-cloudflared.service", "cfvpn-agent.service", "cfvpn-hysteria.service", "cfvpn-cert-renew.timer", "cfvpn-healthcheck.timer"} {
		if err := systemd.EnableNow(ctx, sysRunner, svc); err != nil {
			return fmt.Errorf("enable %s: %w", svc, err)
		}
	}
	if in.Mode == "direct" {
		if err := deps.UFW.Allow(ctx, "443/tcp"); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: ufw allow 443/tcp failed: %v\n", err)
		}
	}
	if err := deps.UFW.Allow(ctx, hy2Port+"/udp"); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "warning: ufw allow %s/udp failed: %v\n", hy2Port, err)
	}

	fmt.Fprintf(stdout, "install complete: %s mode %s -> %s, admin %s\n", in.Mode, domain, ip, adminHost)
	var vlessURI string
	if in.Mode == "direct" {
		vlessURI = subscription.BuildVLESSRealityURI(in.User1Name, userUUID, domain,
			realityParams.SNI, realityParams.PublicKey, realityParams.ShortID)
	} else {
		// Phase 0: XHTTP failed through cloudflared; using HTTPUpgrade instead
		vlessURI = subscription.BuildVLESSHTTPUpgradeURI(in.User1Name, userUUID, domain, templates.VLESSPath)
	}
	sub := base64.StdEncoding.EncodeToString([]byte(vlessURI))
	fmt.Fprintln(stdout, sub)
	return nil
}

// tunnelNameForNode derives the Cloudflare tunnel name from a node's NODE_ID,
// e.g. "hkg-01" -> "cfvpn-HKG-01". This mirrors the admin_host label
// (hkg-01.rwl247.dev) so the tunnel is trivially identifiable in the Cloudflare
// dashboard. Returns "" if NODE_ID is not a valid DNS label.
func tunnelNameForNode(nodeID string) string {
	label := normalizeNodeIDForHost(nodeID)
	if label == "" {
		return ""
	}
	return "cfvpn-" + strings.ToUpper(label)
}

func generateAdminHost(nodeID string) (string, error) {
	normalized := normalizeNodeIDForHost(nodeID)
	if normalized == "" {
		return "", fmt.Errorf("NODE_ID must be a DNS label")
	}
	return normalized + "." + adminHostZone, nil
}

func normalizeNodeIDForHost(nodeID string) string {
	normalized := strings.ToLower(strings.TrimSpace(nodeID))
	if len(normalized) == 0 || len(normalized) > 63 || normalized[0] == '-' || normalized[len(normalized)-1] == '-' {
		return ""
	}
	for _, r := range normalized {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return ""
	}
	return normalized
}

func zoneOfDomain(domain string) string {
	parts := strings.Split(strings.Trim(strings.ToLower(domain), "."), ".")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

type execUFW struct{}

func NewExecUFW() UFWRunner { return execUFW{} }
func (execUFW) Allow(ctx context.Context, rule string) error {
	return systemd.ExecRunner{}.Run(ctx, "ufw", "allow", rule)
}

type TCP443Prober struct{}

func (TCP443Prober) Probe(ctx context.Context) error {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", ":443")
	if err != nil {
		return err
	}
	return ln.Close()
}

type UDPListenProber struct{}

func (UDPListenProber) ProbeUDP(ctx context.Context, port int) error {
	lc := net.ListenConfig{}
	pc, err := lc.ListenPacket(ctx, "udp", ":"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	return pc.Close()
}

func pickHy2UDPPort(ctx context.Context, rng io.Reader, prober UDPProber) (int, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		port, err := randomPortInRange(rng, 20000, 60000)
		if err != nil {
			return 0, fmt.Errorf("generate hy2 port: %w", err)
		}
		if err := prober.ProbeUDP(ctx, port); err != nil {
			lastErr = err
			continue
		}
		return port, nil
	}
	return 0, fmt.Errorf("hy2_udp_port_busy: %w", lastErr)
}

func randomPortInRange(rng io.Reader, min, max int) (int, error) {
	if min > max {
		return 0, fmt.Errorf("invalid port range [%d,%d]", min, max)
	}
	span := big.NewInt(int64(max - min + 1))
	n, err := rand.Int(rng, span)
	if err != nil {
		return 0, err
	}
	return min + int(n.Int64()), nil
}

func validateHy2Port(raw string) (int, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || port < 20000 || port > 60000 {
		return 0, fmt.Errorf("HY2_PORT must be in [20000,60000]")
	}
	return port, nil
}

func generatePasswordFrom(rng io.Reader, nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := io.ReadFull(rng, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func printRotateHint(stdout io.Writer, resumeCmd, tunnelID string) {
	fmt.Fprintf(stdout, "operation failed after tunnel provisioning\n")
	fmt.Fprintf(stdout, "resume command: %s\n", resumeCmd)
	fmt.Fprintf(stdout, "cleanup command: cfvpnctl rotate-domain --cleanup %s\n", tunnelID)
}

func CertPathsForHost(host string) (certPath, keyPath string) {
	return filepath.Join("/etc/cfvpn/certs", host, "fullchain.pem"), filepath.Join("/etc/cfvpn/certs", host, "privkey.pem")
}

func HysteriaCertPaths() (certPath, keyPath string) {
	return "/etc/cfvpn/hysteria/cert.pem", "/etc/cfvpn/hysteria/key.pem"
}

func XrayCertPaths() (certPath, keyPath string) {
	return "/etc/cfvpn/xray/cert.pem", "/etc/cfvpn/xray/key.pem"
}
