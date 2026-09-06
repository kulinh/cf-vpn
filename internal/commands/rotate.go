package commands

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kulinh/cf-vpn/internal/cert"
	"github.com/kulinh/cf-vpn/internal/fsutil"
	"github.com/kulinh/cf-vpn/internal/hysteria"
	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/subscription"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/xray"
)

var (
	envFilePath        = paths.EnvFile
	cloudflaredCredDir = paths.CloudflaredCredDir
	cloudflaredConfig  = paths.CloudflaredConfig
	hysteriaConfigPath = "/etc/cfvpn/hysteria/config.yaml"
)

type RotateDeps struct {
	CF     RotateCFClient
	Runner systemd.Runner
}

type RotateCFClient interface {
	GetZoneID(ctx context.Context, domain string) (string, error)
	CreateTunnel(ctx context.Context, name string) (id string, creds []byte, err error)
	UpsertCNAME(ctx context.Context, zoneID, name, target string) error
	DeleteTunnel(ctx context.Context, id string) error
}

func RunRotateCleanup(ctx context.Context, tunnelID string, deps RotateDeps, stdout, stderr io.Writer) error {
	_ = stderr
	if strings.TrimSpace(tunnelID) == "" {
		return fmt.Errorf("tunnel id is required")
	}
	if deps.CF == nil {
		return fmt.Errorf("cloudflare client is required")
	}
	if err := deps.CF.DeleteTunnel(ctx, tunnelID); err != nil {
		return fmt.Errorf("delete tunnel: %w", err)
	}

	credPath := filepath.Join(cloudflaredCredDir, tunnelID+".json")
	if err := os.Remove(credPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove tunnel credentials: %w", err)
	}

	fmt.Fprintln(stdout, "cleanup complete")
	return nil
}

// RegenerateSubscriptions rebuilds per-user subscription files for all users
// currently in the xray config, using the active mode + Reality params from env.
// Callers must invoke this after any user-set or host change so panel-served
// subscriptions stay in sync with the live xray state.
func RegenerateSubscriptions(domain string) error {
	return RegenerateSubscriptionsTo(domain, os.Stderr)
}

// RegenerateSubscriptionsTo is RegenerateSubscriptions with an explicit sink
// for the "this node cannot serve a usable URI" warnings (see buildUserURIs).
func RegenerateSubscriptionsTo(domain string, warn io.Writer) error {
	cfg, err := xray.Load(xrayConfigPath)
	if err != nil {
		return fmt.Errorf("load xray config: %w", err)
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		return fmt.Errorf("load env: %w", err)
	}

	hy2PW, hy2Err := hy2PasswordsByName()
	if hy2Err != nil {
		warnf(warn, "warning: cannot read hysteria config (%v); subscriptions will have no HY2 line", hy2Err)
	}

	for _, name := range xray.ListUserNames(cfg) {
		uuid, ok := xray.GetVLESSClient(cfg, name)
		if !ok {
			return fmt.Errorf("user %q has no vless client", name)
		}
		sub := subscription.BuildSubscriptionB64(buildUserURIs(name, uuid, domain, hy2PW[name], env, warn)...)
		if err := writeSubscriptionFile(name, sub+"\n"); err != nil {
			return fmt.Errorf("write subscription for %s: %w", name, err)
		}
	}
	return nil
}

// WriteAtomicFile is exported for use by packages outside commands (e.g. cfvpn-agent).
func WriteAtomicFile(path string, content []byte, mode os.FileMode) error {
	return writeAtomicFile(path, content, mode)
}

// xrayDNSServersFromEnv reads the optional XRAY_DNS_SERVERS override (a
// comma-separated resolver list, e.g. DoH URLs) from cfvpn.env. Returns nil
// when unset so renderers fall back to the international DoH default; CHN nodes
// set this to domestic resolvers.
func xrayDNSServersFromEnv(env map[string]string) []string {
	return xrayDNSServersCSV(env["XRAY_DNS_SERVERS"])
}

// XrayDNSServersFromEnv is the exported form for out-of-package callers
// (cfvpn-agent) that re-render xray config on user mutations.
func XrayDNSServersFromEnv(env map[string]string) []string {
	return xrayDNSServersFromEnv(env)
}

// xrayDNSServersCSV splits a comma-separated resolver list, trimming blanks.
// Empty input returns nil so renderers fall back to the international DoH default.
func xrayDNSServersCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// writeAtomicFile is the package-local alias for the one shared crash-atomic
// writer (unique temp name + fsync of the parent directory).
func writeAtomicFile(path string, content []byte, mode os.FileMode) error {
	return fsutil.WriteFile(path, content, mode)
}

type RotateDirectCF interface {
	UpsertARecord(ctx context.Context, zoneID, name, ip string) error
	DeleteARecordByName(ctx context.Context, zoneID, name string) error
}

type IPDetector interface {
	Detect(ctx context.Context) (string, error)
}

type ExistingUser struct {
	Name string
	UUID string
}

type RotateDirectInputs struct {
	NewHost       string
	NewZone       string
	NewZoneID     string
	OldHost       string
	OldZoneID     string
	CFAPIToken    string
	ExistingUsers []ExistingUser
	// HY2 fields
	NewHy2Host   string
	NewHy2Zone   string
	NewHy2ZoneID string
	OldHy2Host   string
	OldHy2ZoneID string
}

type RotateDirectDeps struct {
	CF     RotateDirectCF
	IP     IPDetector
	Cert   cert.Manager
	Runner systemd.Runner
}

type RotateDirectResult struct {
	VpnHost   string
	PublicIP  string
	Hy2Host   string
	Hy2Port   int
	Hy2ObfsPW string
}

func RunRotateDirect(ctx context.Context, in RotateDirectInputs, deps RotateDirectDeps, stdout, stderr io.Writer) (RotateDirectResult, error) {
	if deps.CF == nil {
		return RotateDirectResult{}, fmt.Errorf("cloudflare client is required")
	}
	if deps.IP == nil {
		return RotateDirectResult{}, fmt.Errorf("ip detector is required")
	}
	if deps.Cert == nil {
		return RotateDirectResult{}, fmt.Errorf("cert manager is required")
	}
	in.NewHost = strings.TrimSpace(in.NewHost)
	in.NewZone = strings.TrimSpace(in.NewZone)
	in.NewZoneID = strings.TrimSpace(in.NewZoneID)
	if in.NewHost == "" {
		return RotateDirectResult{}, fmt.Errorf("new host is required")
	}
	if in.NewZone == "" {
		return RotateDirectResult{}, fmt.Errorf("new zone is required")
	}
	if in.NewZoneID == "" {
		return RotateDirectResult{}, fmt.Errorf("new zone id is required")
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		return RotateDirectResult{}, fmt.Errorf("load env: %w", err)
	}
	realityParams, realityMode := loadRealityFromEnv(env)
	if !realityMode {
		return RotateDirectResult{}, fmt.Errorf("rotate-direct requires Reality params in cfvpn.env (REALITY_PRIVATE_KEY, REALITY_SHORT_ID); legacy WS+TLS direct mode is no longer supported — run `cfvpnctl upgrade --mode direct` to migrate this node")
	}

	ip, err := deps.IP.Detect(ctx)
	if err != nil {
		return RotateDirectResult{}, fmt.Errorf("detect public ip: %w", err)
	}
	ip = strings.TrimSpace(ip)
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() {
		return RotateDirectResult{}, fmt.Errorf("detect public ip: expected IPv4 address, got %q", ip)
	}

	// Reality nodes have no public LE cert on :443; only HY2 needs a real cert.
	in.NewHy2Host = strings.TrimSpace(in.NewHy2Host)
	in.NewHy2ZoneID = strings.TrimSpace(in.NewHy2ZoneID)
	hy2CertPath, hy2KeyPath := HysteriaCertPaths()
	if in.NewHy2Host != "" {
		if err := deps.Cert.Issue(ctx, in.NewHy2Host, hy2CertPath, hy2KeyPath, in.CFAPIToken); err != nil {
			return RotateDirectResult{}, fmt.Errorf("issue cert for %s: %w", in.NewHy2Host, err)
		}
	}

	users, err := rotateDirectUsers(in.ExistingUsers)
	if err != nil {
		return RotateDirectResult{}, err
	}
	rendered, err := templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
		Users:       users,
		PrivateKey:  realityParams.PrivateKey,
		ShortIDs:    []string{realityParams.ShortID},
		Dest:        realityParams.Dest,
		ServerNames: []string{realityParams.SNI},
		DNSServers:  xrayDNSServersFromEnv(env),
	})
	if err != nil {
		return RotateDirectResult{}, fmt.Errorf("render xray reality config: %w", err)
	}

	oldConfig, oldConfigErr := os.ReadFile(xrayConfigPath)
	oldConfigExists := oldConfigErr == nil
	if oldConfigErr != nil && !os.IsNotExist(oldConfigErr) {
		return RotateDirectResult{}, fmt.Errorf("read existing xray config: %w", oldConfigErr)
	}

	dnsCreated := false
	hy2DnsCreated := false
	rollbackDNS := func(cause error) (RotateDirectResult, error) {
		if hy2DnsCreated {
			if derr := deps.CF.DeleteARecordByName(ctx, in.NewHy2ZoneID, in.NewHy2Host); derr != nil && stderr != nil {
				fmt.Fprintf(stderr, "warning: rollback delete HY2 A record failed: %v\n", derr)
			}
		}
		if dnsCreated {
			if derr := deps.CF.DeleteARecordByName(ctx, in.NewZoneID, in.NewHost); derr != nil && stderr != nil {
				fmt.Fprintf(stderr, "warning: rollback delete A record failed: %v\n", derr)
			}
		}
		return RotateDirectResult{}, cause
	}

	if err := deps.CF.UpsertARecord(ctx, in.NewZoneID, in.NewHost, ip); err != nil {
		return RotateDirectResult{}, fmt.Errorf("upsert dns a record: %w", err)
	}
	dnsCreated = true

	if in.NewHy2Host != "" && in.NewHy2ZoneID != "" {
		if err := deps.CF.UpsertARecord(ctx, in.NewHy2ZoneID, in.NewHy2Host, ip); err != nil {
			return rollbackDNS(fmt.Errorf("upsert hy2 dns a record: %w", err))
		}
		hy2DnsCreated = true
	}

	// H12: refuse to publish a config xray cannot load; the live one stays.
	if err := writeXrayConfigChecked(ctx, xrayConfigPath, []byte(rendered), 0o600); err != nil {
		return rollbackDNS(fmt.Errorf("write xray config: %w", err))
	}
	if err := systemd.Restart(ctx, resolveRunner(deps.Runner), "cfvpn-xray.service"); err != nil {
		if oldConfigExists {
			if rerr := writeAtomicFile(xrayConfigPath, oldConfig, 0o600); rerr != nil {
				return rollbackDNS(fmt.Errorf("restart cfvpn-xray.service: %w; additionally failed to restore previous xray config: %v", err, rerr))
			}
			// Restore the file AND restart so xray comes back up on the
			// known-good config instead of being left in a failed state.
			if rerr := systemd.Restart(ctx, resolveRunner(deps.Runner), "cfvpn-xray.service"); rerr != nil && stderr != nil {
				fmt.Fprintf(stderr, "warning: restart cfvpn-xray.service with restored config failed: %v\n", rerr)
			}
		} else if rerr := os.Remove(xrayConfigPath); rerr != nil && !os.IsNotExist(rerr) {
			return rollbackDNS(fmt.Errorf("restart cfvpn-xray.service: %w; additionally failed to remove new xray config: %v", err, rerr))
		}
		return rollbackDNS(fmt.Errorf("restart cfvpn-xray.service: %w", err))
	}

	env["DOMAIN"] = in.NewHost
	env["PUBLIC_IP"] = ip
	env["MODE"] = "direct"

	if in.NewHy2Host != "" {
		if err := rotateHy2Config(ctx, in.NewHy2Host, hy2CertPath, hy2KeyPath, env, resolveRunner(deps.Runner)); err != nil {
			// xray is already live with the new host; persist partial state so the
			// system state is visible (DOMAIN/PUBLIC_IP updated, HY2_HOST not yet).
			_ = state.SaveAtomic(envFilePath, env, 0o600)
			return RotateDirectResult{}, err
		}
		env["HY2_HOST"] = in.NewHy2Host
	}

	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		return RotateDirectResult{}, fmt.Errorf("save env: %w", err)
	}

	if err := RegenerateSubscriptions(in.NewHost); err != nil {
		return RotateDirectResult{}, err
	}

	if strings.TrimSpace(in.OldHost) != "" && strings.TrimSpace(in.OldZoneID) != "" {
		if err := deps.CF.DeleteARecordByName(ctx, in.OldZoneID, in.OldHost); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: delete old A record failed: %v\n", err)
		}
	}
	if strings.TrimSpace(in.OldHy2Host) != "" && strings.TrimSpace(in.OldHy2ZoneID) != "" {
		if err := deps.CF.DeleteARecordByName(ctx, in.OldHy2ZoneID, in.OldHy2Host); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: delete old hy2 A record failed: %v\n", err)
		}
	}

	// Build result: HY2 fields from env (port and obfsPW are unchanged).
	hy2Port := 0
	if p, err := strconv.Atoi(env["HY2_PORT"]); err == nil {
		hy2Port = p
	}
	result := RotateDirectResult{
		VpnHost:   in.NewHost,
		PublicIP:  ip,
		Hy2ObfsPW: env["HY2_OBFS_PW"],
		Hy2Port:   hy2Port,
	}
	if in.NewHy2Host != "" {
		result.Hy2Host = in.NewHy2Host
	} else {
		result.Hy2Host = env["HY2_HOST"]
	}

	if stdout != nil {
		fmt.Fprintf(stdout, "direct rotation complete: %s -> %s\n", in.NewHost, ip)
	}
	return result, nil
}

// rotateHy2Config rewrites the hysteria config with the new HY2 host cert paths
// (preserving users, port, and obfs PW from env) and restarts the service.
func rotateHy2Config(ctx context.Context, newHy2Host, certPath, keyPath string, env map[string]string, runner systemd.Runner) error {
	users, err := hysteria.ListUsers(hysteriaConfigPath)
	if err != nil {
		return fmt.Errorf("load hysteria users: %w", err)
	}
	port := env["HY2_PORT"]
	if port == "" {
		return fmt.Errorf("HY2_PORT is required in env for hysteria config rotation")
	}
	obfsPW := env["HY2_OBFS_PW"]
	if obfsPW == "" {
		return fmt.Errorf("HY2_OBFS_PW is required in env for hysteria config rotation")
	}
	hi := templates.HysteriaInputs{
		Listen:   ":" + port,
		TLSCert:  certPath,
		TLSKey:   keyPath,
		ObfsPW:   obfsPW,
		UpMbps:   100,
		DownMbps: 100,
	}
	for _, u := range users {
		hi.Users = append(hi.Users, templates.HysteriaUser{Name: u.Name, Password: u.Password})
	}
	body, err := templates.RenderHysteriaConfig(hi)
	if err != nil {
		return fmt.Errorf("render hysteria config: %w", err)
	}

	oldHyConfig, oldHyConfigErr := os.ReadFile(hysteriaConfigPath)
	oldHyConfigExists := oldHyConfigErr == nil
	if oldHyConfigErr != nil && !os.IsNotExist(oldHyConfigErr) {
		return fmt.Errorf("read existing hysteria config: %w", oldHyConfigErr)
	}

	if err := writeAtomicFile(hysteriaConfigPath, body, 0o600); err != nil {
		return fmt.Errorf("write hysteria config: %w", err)
	}
	if err := hysteria.ReloadService(ctx, runner); err != nil {
		if oldHyConfigExists {
			// Restore the file AND reload so hysteria comes back up on the
			// known-good config instead of being left in a failed state.
			_ = writeAtomicFile(hysteriaConfigPath, oldHyConfig, 0o600)
			_ = hysteria.ReloadService(ctx, runner)
		} else {
			_ = os.Remove(hysteriaConfigPath)
		}
		return fmt.Errorf("restart cfvpn-hysteria.service: %w", err)
	}
	return nil
}

type RotateCloudflareCF interface {
	UpsertCNAME(ctx context.Context, zoneID, name, target string) error
	DeleteCNAMEByName(ctx context.Context, zoneID, name string) error
	UpsertARecord(ctx context.Context, zoneID, name, ip string) error
	DeleteARecordByName(ctx context.Context, zoneID, name string) error
}

type RotateCloudflareInputs struct {
	NewHost       string
	NewZoneID     string
	OldHost       string
	OldZoneID     string
	CFAPIToken    string
	ExistingUsers []ExistingUser
	NewHy2Host    string
	NewHy2ZoneID  string
	OldHy2Host    string
	OldHy2ZoneID  string
}

type RotateCloudflareDeps struct {
	CF     RotateCloudflareCF
	Cert   cert.Manager
	Runner systemd.Runner
}

// RunRotateCloudflare rotates the VPN host of a cloudflare-mode node. Unlike
// direct mode, the node's IP is unchanged: the VPN host is a CNAME to
// <admin_tunnel_uuid>.cfargotunnel.com fronted by Cloudflare's edge, xray
// listens only on 127.0.0.1:10001 with HTTPUpgrade transport, and the host
// header in xray's streamSettings + the cloudflared.yml ingress hostname
// must both be rewritten to the new host. HY2 still goes direct (A record +
// real cert), so its rotation is identical to the direct-mode flow.
func RunRotateCloudflare(ctx context.Context, in RotateCloudflareInputs, deps RotateCloudflareDeps, stdout, stderr io.Writer) (RotateDirectResult, error) {
	if deps.CF == nil {
		return RotateDirectResult{}, fmt.Errorf("cloudflare client is required")
	}
	if deps.Cert == nil {
		return RotateDirectResult{}, fmt.Errorf("cert manager is required")
	}
	in.NewHost = strings.TrimSpace(in.NewHost)
	in.NewZoneID = strings.TrimSpace(in.NewZoneID)
	if in.NewHost == "" {
		return RotateDirectResult{}, fmt.Errorf("new host is required")
	}
	if in.NewZoneID == "" {
		return RotateDirectResult{}, fmt.Errorf("new zone id is required")
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		return RotateDirectResult{}, fmt.Errorf("load env: %w", err)
	}
	tunnelUUID := strings.TrimSpace(env["ADMIN_TUNNEL_UUID"])
	if tunnelUUID == "" {
		tunnelUUID = strings.TrimSpace(env["TUNNEL_UUID"])
	}
	if tunnelUUID == "" {
		return RotateDirectResult{}, fmt.Errorf("ADMIN_TUNNEL_UUID is required in env for cloudflare rotation")
	}
	adminHost := strings.TrimSpace(env["ADMIN_HOST"])
	if adminHost == "" {
		return RotateDirectResult{}, fmt.Errorf("ADMIN_HOST is required in env for cloudflare rotation")
	}

	in.NewHy2Host = strings.TrimSpace(in.NewHy2Host)
	in.NewHy2ZoneID = strings.TrimSpace(in.NewHy2ZoneID)
	hy2CertPath, hy2KeyPath := HysteriaCertPaths()
	if in.NewHy2Host != "" {
		if err := deps.Cert.Issue(ctx, in.NewHy2Host, hy2CertPath, hy2KeyPath, in.CFAPIToken); err != nil {
			return RotateDirectResult{}, fmt.Errorf("issue cert for %s: %w", in.NewHy2Host, err)
		}
	}

	users, err := rotateDirectUsers(in.ExistingUsers)
	if err != nil {
		return RotateDirectResult{}, err
	}

	xrayRendered, err := templates.RenderXrayCloudflareHTTPUpgrade(users, in.NewHost, xrayDNSServersFromEnv(env))
	if err != nil {
		return RotateDirectResult{}, fmt.Errorf("render xray cloudflare config: %w", err)
	}
	cfRendered, err := templates.RenderCloudflaredWithAdmin(tunnelUUID, in.NewHost, adminHost)
	if err != nil {
		return RotateDirectResult{}, fmt.Errorf("render cloudflared config: %w", err)
	}

	oldXray, oldXrayErr := os.ReadFile(xrayConfigPath)
	oldXrayExists := oldXrayErr == nil
	if oldXrayErr != nil && !os.IsNotExist(oldXrayErr) {
		return RotateDirectResult{}, fmt.Errorf("read existing xray config: %w", oldXrayErr)
	}
	oldCfd, oldCfdErr := os.ReadFile(cloudflaredConfig)
	oldCfdExists := oldCfdErr == nil
	if oldCfdErr != nil && !os.IsNotExist(oldCfdErr) {
		return RotateDirectResult{}, fmt.Errorf("read existing cloudflared config: %w", oldCfdErr)
	}

	cnameCreated := false
	hy2DnsCreated := false
	rollbackDNS := func(cause error) (RotateDirectResult, error) {
		if hy2DnsCreated {
			if derr := deps.CF.DeleteARecordByName(ctx, in.NewHy2ZoneID, in.NewHy2Host); derr != nil && stderr != nil {
				fmt.Fprintf(stderr, "warning: rollback delete HY2 A record failed: %v\n", derr)
			}
		}
		if cnameCreated {
			if derr := deps.CF.DeleteCNAMEByName(ctx, in.NewZoneID, in.NewHost); derr != nil && stderr != nil {
				fmt.Fprintf(stderr, "warning: rollback delete VPN CNAME failed: %v\n", derr)
			}
		}
		return RotateDirectResult{}, cause
	}
	restoreXray := func() {
		if oldXrayExists {
			_ = writeAtomicFile(xrayConfigPath, oldXray, 0o600)
		} else {
			_ = os.Remove(xrayConfigPath)
		}
	}
	restoreCfd := func() {
		if oldCfdExists {
			_ = writeAtomicFile(cloudflaredConfig, oldCfd, 0o600)
		} else {
			_ = os.Remove(cloudflaredConfig)
		}
	}

	if err := deps.CF.UpsertCNAME(ctx, in.NewZoneID, in.NewHost, tunnelUUID+".cfargotunnel.com"); err != nil {
		return RotateDirectResult{}, fmt.Errorf("upsert vpn cname: %w", err)
	}
	cnameCreated = true

	if in.NewHy2Host != "" && in.NewHy2ZoneID != "" {
		ip := strings.TrimSpace(env["PUBLIC_IP"])
		if ip == "" {
			return rollbackDNS(fmt.Errorf("PUBLIC_IP is required in env to upsert HY2 A record"))
		}
		if err := deps.CF.UpsertARecord(ctx, in.NewHy2ZoneID, in.NewHy2Host, ip); err != nil {
			return rollbackDNS(fmt.Errorf("upsert hy2 dns a record: %w", err))
		}
		hy2DnsCreated = true
	}

	if err := writeXrayConfigChecked(ctx, xrayConfigPath, []byte(xrayRendered), 0o600); err != nil {
		return rollbackDNS(fmt.Errorf("write xray config: %w", err))
	}
	if err := systemd.Restart(ctx, resolveRunner(deps.Runner), "cfvpn-xray.service"); err != nil {
		restoreXray()
		// Restart on the restored config so xray isn't left in a failed state.
		if rerr := systemd.Restart(ctx, resolveRunner(deps.Runner), "cfvpn-xray.service"); rerr != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: restart cfvpn-xray.service with restored config failed: %v\n", rerr)
		}
		return rollbackDNS(fmt.Errorf("restart cfvpn-xray.service: %w", err))
	}

	if err := writeAtomicFile(cloudflaredConfig, []byte(cfRendered), 0o600); err != nil {
		restoreXray()
		_ = systemd.Restart(ctx, resolveRunner(deps.Runner), "cfvpn-xray.service")
		return rollbackDNS(fmt.Errorf("write cloudflared config: %w", err))
	}
	if err := systemd.Restart(ctx, resolveRunner(deps.Runner), "cfvpn-cloudflared.service"); err != nil {
		restoreCfd()
		restoreXray()
		_ = systemd.Restart(ctx, resolveRunner(deps.Runner), "cfvpn-xray.service")
		// Restart cloudflared on the restored config too — otherwise a failed
		// rotate can leave the admin tunnel (remote management path) down.
		if rerr := systemd.Restart(ctx, resolveRunner(deps.Runner), "cfvpn-cloudflared.service"); rerr != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: restart cfvpn-cloudflared.service with restored config failed: %v\n", rerr)
		}
		return rollbackDNS(fmt.Errorf("restart cfvpn-cloudflared.service: %w", err))
	}

	env["DOMAIN"] = in.NewHost
	env["MODE"] = "cloudflare"

	if in.NewHy2Host != "" {
		if err := rotateHy2Config(ctx, in.NewHy2Host, hy2CertPath, hy2KeyPath, env, resolveRunner(deps.Runner)); err != nil {
			// xray + cloudflared are already live with the new host; persist
			// partial state so the system state is visible.
			_ = state.SaveAtomic(envFilePath, env, 0o600)
			return RotateDirectResult{}, err
		}
		env["HY2_HOST"] = in.NewHy2Host
	}

	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		return RotateDirectResult{}, fmt.Errorf("save env: %w", err)
	}

	if err := RegenerateSubscriptions(in.NewHost); err != nil {
		return RotateDirectResult{}, err
	}

	if strings.TrimSpace(in.OldHost) != "" && strings.TrimSpace(in.OldZoneID) != "" {
		if err := deps.CF.DeleteCNAMEByName(ctx, in.OldZoneID, in.OldHost); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: delete old vpn cname failed: %v\n", err)
		}
	}
	if strings.TrimSpace(in.OldHy2Host) != "" && strings.TrimSpace(in.OldHy2ZoneID) != "" {
		if err := deps.CF.DeleteARecordByName(ctx, in.OldHy2ZoneID, in.OldHy2Host); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: delete old hy2 a record failed: %v\n", err)
		}
	}

	hy2Port := 0
	if p, err := strconv.Atoi(env["HY2_PORT"]); err == nil {
		hy2Port = p
	}
	result := RotateDirectResult{
		VpnHost:   in.NewHost,
		PublicIP:  env["PUBLIC_IP"],
		Hy2ObfsPW: env["HY2_OBFS_PW"],
		Hy2Port:   hy2Port,
	}
	if in.NewHy2Host != "" {
		result.Hy2Host = in.NewHy2Host
	} else {
		result.Hy2Host = env["HY2_HOST"]
	}

	if stdout != nil {
		fmt.Fprintf(stdout, "cloudflare rotation complete: %s\n", in.NewHost)
	}
	return result, nil
}

func rotateDirectUsers(existing []ExistingUser) ([]templates.XrayUser, error) {
	if existing != nil {
		users := make([]templates.XrayUser, 0, len(existing))
		for _, u := range existing {
			users = append(users, templates.XrayUser{Name: u.Name, UUID: u.UUID})
		}
		return users, nil
	}
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
		users = append(users, templates.XrayUser{Name: strings.TrimSuffix(name, "@vpn"), UUID: uuid})
	}
	return users, nil
}
