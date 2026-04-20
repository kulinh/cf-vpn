package commands

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/kulinh/cf-vpn/internal/binary"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/subscription"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/xray"
)

// InstallInputs carries the user-provided inputs to install.
type InstallInputs struct {
	CFAPIToken  string
	CFAccountID string
	Domain      string
	User1Name   string
}

// InstallCFClient is the Cloudflare dependency required by RunInstall.
type InstallCFClient interface {
	GetZoneID(ctx context.Context, domain string) (string, error)
	CreateTunnel(ctx context.Context, name string) (id string, creds []byte, err error)
	UpsertCNAME(ctx context.Context, zoneID, name, target string) error
}

// InstallDeps are injected collaborators for RunInstall.
type InstallDeps struct {
	CF            InstallCFClient
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

// RunInstall performs the full install orchestration per section 6 of the
// standalone design spec.
func RunInstall(ctx context.Context, in InstallInputs, deps InstallDeps, stdout, stderr io.Writer) error {
	_ = stderr
	if in.CFAPIToken == "" || in.CFAccountID == "" || in.Domain == "" {
		return fmt.Errorf("CF_API_TOKEN, CF_ACCOUNT_ID, and DOMAIN are required")
	}
	if in.User1Name == "" {
		in.User1Name = "user1"
	}
	if deps.CF == nil {
		return fmt.Errorf("cloudflare client is required")
	}

	binRunner := resolveBinaryRunner(deps.BinaryRunner)
	sysRunner := resolveRunner(deps.SystemdRunner)

	// 1) Ensure binaries.
	fmt.Fprintln(stdout, "ensuring binaries...")
	if err := binary.EnsureXray(ctx, binRunner, binary.Exists("xray")); err != nil {
		return fmt.Errorf("ensure xray: %w", err)
	}
	if err := binary.EnsureCloudflared(ctx, binRunner, binary.Exists("cloudflared")); err != nil {
		return fmt.Errorf("ensure cloudflared: %w", err)
	}

	// 2) Create tunnel and persist credentials.
	fmt.Fprintln(stdout, "creating tunnel...")
	tunnelID, creds, err := deps.CF.CreateTunnel(ctx, "cfvpn-"+in.Domain)
	if err != nil {
		return fmt.Errorf("create tunnel: %w", err)
	}
	if tunnelID == "" {
		return fmt.Errorf("create tunnel: empty tunnel id")
	}
	credPath := filepath.Join(cloudflaredCredDir, tunnelID+".json")
	if err := writeAtomicFile(credPath, creds, 0o600); err != nil {
		return fmt.Errorf("write tunnel credentials: %w", err)
	}

	// 3) Configure DNS.
	fmt.Fprintln(stdout, "configuring dns...")
	zoneID, err := deps.CF.GetZoneID(ctx, in.Domain)
	if err != nil {
		return fmt.Errorf("get zone id for %s: %w", in.Domain, err)
	}
	if err := deps.CF.UpsertCNAME(ctx, zoneID, in.Domain, tunnelID+".cfargotunnel.com"); err != nil {
		return fmt.Errorf("upsert dns cname: %w", err)
	}

	// 4) Render configs and persist env.
	fmt.Fprintln(stdout, "rendering configs...")
	uuid, err := generateUUIDv4()
	if err != nil {
		return fmt.Errorf("generate uuid: %w", err)
	}
	pass, err := generatePassword(24)
	if err != nil {
		return fmt.Errorf("generate password: %w", err)
	}

	xrayCfg := xray.NewBaseConfig(in.User1Name, uuid, pass)
	if err := xray.SaveAtomic(xrayConfigPath, xrayCfg, 0o600); err != nil {
		return fmt.Errorf("save xray config: %w", err)
	}

	cfRendered, err := templates.RenderCloudflared(tunnelID, in.Domain)
	if err != nil {
		return fmt.Errorf("render cloudflared config: %w", err)
	}
	if err := writeAtomicFile(cloudflaredConfig, []byte(cfRendered), 0o600); err != nil {
		return fmt.Errorf("write cloudflared config: %w", err)
	}

	if err := state.SaveAtomic(envFilePath, map[string]string{
		"CF_API_TOKEN":      in.CFAPIToken,
		"CF_ACCOUNT_ID":     in.CFAccountID,
		"DOMAIN":            in.Domain,
		"USER1_NAME":        in.User1Name,
		"TUNNEL_UUID":       tunnelID,
		"UUID_USER1":        uuid,
		"TROJAN_PASS_USER1": pass,
	}, 0o600); err != nil {
		return fmt.Errorf("save env: %w", err)
	}

	// 5) Install systemd units and enable them.
	fmt.Fprintln(stdout, "installing systemd units...")
	xrayUnit := systemd.XrayService(xrayConfigPath)
	cfUnit := systemd.CloudflaredService(cloudflaredConfig)

	xrayUnitPath := filepath.Join(systemdUnitDir, "cfvpn-xray.service")
	cfUnitPath := filepath.Join(systemdUnitDir, "cfvpn-cloudflared.service")
	if err := writeAtomicFile(xrayUnitPath, []byte(xrayUnit), 0o644); err != nil {
		return fmt.Errorf("write cfvpn-xray.service: %w", err)
	}
	if err := writeAtomicFile(cfUnitPath, []byte(cfUnit), 0o644); err != nil {
		return fmt.Errorf("write cfvpn-cloudflared.service: %w", err)
	}

	if err := systemd.DaemonReload(ctx, sysRunner); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := systemd.EnableNow(ctx, sysRunner, "cfvpn-xray.service"); err != nil {
		return fmt.Errorf("enable cfvpn-xray.service: %w", err)
	}
	if err := systemd.EnableNow(ctx, sysRunner, "cfvpn-cloudflared.service"); err != nil {
		return fmt.Errorf("enable cfvpn-cloudflared.service: %w", err)
	}

	// 6) Probe; non-fatal — the tunnel may still be coming up.
	probeInstall(ctx, in.Domain, stdout)

	// 7) Print user1 subscription.
	sub := subscription.BuildSubscriptionB64(
		subscription.BuildVLESSURI(in.User1Name, uuid, in.Domain),
		subscription.BuildTrojanURI(in.User1Name, pass, in.Domain),
	)
	fmt.Fprintln(stdout, sub)
	return nil
}

// probeInstall attempts an HTTPS GET to https://<domain>/vless. The tunnel
// may take minutes to register, so any transport error or non-healthy code
// is logged but does not fail the install.
func probeInstall(ctx context.Context, domain string, stdout io.Writer) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/vless", nil)
	if err != nil {
		fmt.Fprintf(stdout, "probe: skipped (%v)\n", err)
		return
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stdout, "probe: transport error: %v (non-fatal — tunnel may still be connecting)\n", err)
		return
	}
	defer resp.Body.Close()
	if IsHealthyCode(resp.StatusCode) {
		fmt.Fprintf(stdout, "probe: OK code=%d\n", resp.StatusCode)
		return
	}
	fmt.Fprintf(stdout, "probe: code=%d (non-fatal — tunnel may still be connecting)\n", resp.StatusCode)
}
