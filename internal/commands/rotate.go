package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
)

type RotateInputs struct {
	NewDomain   string
	OldDomain   string
	OldTunnel   string
	CFAPIToken  string
	CFAccountID string
}

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

func ValidateRotateDomains(oldDomain, newDomain string) error {
	oldDomain = strings.TrimSpace(oldDomain)
	newDomain = strings.TrimSpace(newDomain)
	if oldDomain == "" {
		return fmt.Errorf("no current DOMAIN set")
	}
	if newDomain == "" {
		return fmt.Errorf("new domain is required")
	}
	if oldDomain == newDomain {
		return fmt.Errorf("new-domain matches current DOMAIN")
	}
	return nil
}

func RunRotateDomain(ctx context.Context, in RotateInputs, deps RotateDeps, stdout, stderr io.Writer) error {
	_ = stderr
	if deps.CF == nil {
		return fmt.Errorf("cloudflare client is required")
	}
	if err := ValidateRotateDomains(in.OldDomain, in.NewDomain); err != nil {
		return err
	}
	if strings.TrimSpace(in.OldTunnel) == "" {
		return fmt.Errorf("no current TUNNEL_UUID set")
	}

	zoneID, err := deps.CF.GetZoneID(ctx, in.NewDomain)
	if err != nil {
		return fmt.Errorf("get zone id for %s: %w", in.NewDomain, err)
	}

	newTunnelID, creds, err := deps.CF.CreateTunnel(ctx, "cfvpn-"+in.NewDomain)
	if err != nil {
		return fmt.Errorf("create tunnel: %w", err)
	}
	if newTunnelID == "" {
		return fmt.Errorf("create tunnel: empty tunnel id")
	}

	credPath := filepath.Join(cloudflaredCredDir, newTunnelID+".json")
	if err := writeAtomicFile(credPath, creds, 0o600); err != nil {
		return fmt.Errorf("write tunnel credentials: %w", err)
	}

	if err := deps.CF.UpsertCNAME(ctx, zoneID, in.NewDomain, newTunnelID+".cfargotunnel.com"); err != nil {
		return fmt.Errorf("upsert dns cname: %w", err)
	}

	rendered, err := templates.RenderCloudflared(newTunnelID, in.NewDomain)
	if err != nil {
		return fmt.Errorf("render cloudflared config: %w", err)
	}
	if err := writeAtomicFile(cloudflaredConfig, []byte(rendered), 0o600); err != nil {
		return fmt.Errorf("write cloudflared config: %w", err)
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		return fmt.Errorf("load env: %w", err)
	}
	env["DOMAIN"] = in.NewDomain
	env["TUNNEL_UUID"] = newTunnelID
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		return fmt.Errorf("save env: %w", err)
	}

	runner := resolveRunner(deps.Runner)
	if err := systemd.Restart(ctx, runner, "cfvpn-cloudflared.service"); err != nil {
		return fmt.Errorf("restart cfvpn-cloudflared.service: %w", err)
	}
	if err := systemd.Restart(ctx, runner, "cfvpn-xray.service"); err != nil {
		return fmt.Errorf("restart cfvpn-xray.service: %w", err)
	}

	if err := regenerateSubscriptions(in.NewDomain); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "rotation complete. Old tunnel %s still active.\n", in.OldTunnel)
	fmt.Fprintf(stdout, "After verifying clients, run: cfvpnctl rotate-domain --cleanup %s\n", in.OldTunnel)
	return nil
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

func regenerateSubscriptions(domain string) error {
	cfg, err := xray.Load(xrayConfigPath)
	if err != nil {
		return fmt.Errorf("load xray config: %w", err)
	}

	for _, name := range xray.ListUserNames(cfg) {
		uuid, ok := xray.GetVLESSClient(cfg, name)
		if !ok {
			return fmt.Errorf("user %q has no vless client", name)
		}
		password, ok := xray.GetTrojanClient(cfg, name)
		if !ok {
			return fmt.Errorf("user %q has no trojan client", name)
		}
		sub := subscription.BuildSubscriptionB64(
			subscription.BuildVLESSURI(name, uuid, domain),
			subscription.BuildTrojanURI(name, password, domain),
		)
		if err := writeSubscriptionFile(name, sub+"\n"); err != nil {
			return fmt.Errorf("write subscription for %s: %w", name, err)
		}
	}
	return nil
}

func writeAtomicFile(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
