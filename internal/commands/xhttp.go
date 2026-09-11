package commands

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
)

// XHTTPEnabled reports whether a cloudflare-mode node also serves the XHTTP
// inbound (XHTTP_ENABLED in cfvpn.env). Absent = off: HTTPUpgrade alone is
// the historical default and every renderer keeps producing exactly that.
func XHTTPEnabled(env map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(env[state.KeyXHTTPEnabled])) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// RunXHTTPSet flips XHTTP_ENABLED on a cloudflare-mode node, re-renders xray
// (second inbound on templates.XHTTPPort) and cloudflared (ingress rule for
// templates.XHTTPPath), restarts what changed and regenerates the node-side
// subscription files. HTTPUpgrade is untouched either way.
func RunXHTTPSet(ctx context.Context, enable bool, runner systemd.Runner, stdout, stderr io.Writer) error {
	unlock, err := AcquireConfigLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire config lock: %w", err)
	}
	defer unlock()

	env, err := state.Load(envFilePath)
	if err != nil {
		return fmt.Errorf("load env: %w", err)
	}
	if mode := env[state.KeyMode]; mode != "cloudflare" {
		return fmt.Errorf("xhttp needs MODE=cloudflare (this node is MODE=%q)", mode)
	}
	if enable {
		env[state.KeyXHTTPEnabled] = "1"
	} else {
		env[state.KeyXHTTPEnabled] = "0"
	}
	domain := env[state.KeyDomain]
	adminHost := env[state.KeyAdminHost]
	tunnelUUID := env[state.KeyAdminTunnelUUID]
	if tunnelUUID == "" {
		tunnelUUID = env["TUNNEL_UUID"]
	}
	users, err := usersFromCurrentXray()
	if err != nil {
		return err
	}
	xrayRendered, err := templates.RenderXrayCloudflare(users, domain, xrayDNSServersFromEnv(env), enable)
	if err != nil {
		return fmt.Errorf("render xray cloudflare config: %w", err)
	}
	cfRendered, err := templates.RenderCloudflaredWithAdminOpts(tunnelUUID, domain, adminHost,
		templates.CloudflaredOptions{Protocol: env[state.KeyCloudflaredProtocol], XHTTP: enable})
	if err != nil {
		return fmt.Errorf("render cloudflared config: %w", err)
	}
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		return fmt.Errorf("save env: %w", err)
	}
	r := resolveRunner(runner)
	xrayChanged, err := writeXrayConfigIfChanged(ctx, xrayConfigPath, []byte(xrayRendered), 0o600)
	if err != nil {
		return fmt.Errorf("write xray config: %w", err)
	}
	cfChanged, err := writeIfChanged(cloudflaredConfig, []byte(cfRendered), 0o600)
	if err != nil {
		return fmt.Errorf("write cloudflared config: %w", err)
	}
	if xrayChanged {
		if err := systemd.Restart(ctx, r, "cfvpn-xray.service"); err != nil {
			return fmt.Errorf("restart cfvpn-xray.service: %w", err)
		}
	}
	if cfChanged {
		if err := systemd.Restart(ctx, r, "cfvpn-cloudflared.service"); err != nil {
			return fmt.Errorf("restart cfvpn-cloudflared.service: %w", err)
		}
	}
	if err := RegenerateSubscriptionsTo(domain, stderr); err != nil {
		return fmt.Errorf("regenerate subscriptions: %w", err)
	}
	if enable {
		fmt.Fprintf(stdout, "xhttp enabled: %s on 127.0.0.1:%d (%s)\n", templates.XHTTPPath, templates.XHTTPPort, templates.XHTTPMode)
	} else {
		fmt.Fprintln(stdout, "xhttp disabled")
	}
	return nil
}
