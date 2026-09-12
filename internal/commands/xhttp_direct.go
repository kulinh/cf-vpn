package commands

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/validate"
)

// xrayCloudflareOptsFromEnv gathers every optional cloudflare-mode inbound a
// node has switched on, so all renderers (install, upgrade, rotate, agent
// sync, xhttp enable/disable) keep them.
func xrayCloudflareOptsFromEnv(env map[string]string) templates.XrayCloudflareOptions {
	return templates.XrayCloudflareOptions{
		XHTTP:      XHTTPEnabled(env),
		DirectHost: strings.TrimSpace(env[state.KeyXHTTPDirectHost]),
		DirectPath: strings.TrimSpace(env[state.KeyXHTTPDirectPath]),
	}
}

// XrayCloudflareOptsFromEnv is the exported form for cfvpn-agent.
func XrayCloudflareOptsFromEnv(env map[string]string) templates.XrayCloudflareOptions {
	return xrayCloudflareOptsFromEnv(env)
}

// RunXHTTPDirectSet enables (host+path) or disables (both empty) the direct
// XHTTP route on a cloudflare-mode node: persists XHTTP_DIRECT_HOST/PATH,
// re-renders xray (inbound on templates.XHTTPDirectPort) and restarts it if
// changed, and regenerates the node-side subscription files. The TLS front
// (Caddy) that terminates :443 and proxies the path is outside cfvpnctl.
func RunXHTTPDirectSet(ctx context.Context, host, path string, runner systemd.Runner, stdout, stderr io.Writer) error {
	host = strings.TrimSpace(host)
	path = strings.TrimSpace(path)
	enable := host != "" || path != ""
	if enable {
		if host == "" || path == "" {
			return fmt.Errorf("--host and --path are both required")
		}
		if err := validate.Hostname(host); err != nil {
			return fmt.Errorf("--host: %w", err)
		}
		if !strings.HasPrefix(path, "/") || len(path) < 17 || strings.ContainsAny(path, " ?#&") {
			return fmt.Errorf("--path must start with / and be a long random path without spaces, ?, # or &")
		}
	}

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
		return fmt.Errorf("xhttp-direct needs MODE=cloudflare (this node is MODE=%q)", mode)
	}
	env[state.KeyXHTTPDirectHost] = host
	env[state.KeyXHTTPDirectPath] = path
	users, err := usersFromCurrentXray()
	if err != nil {
		return err
	}
	rendered, err := templates.RenderXrayCloudflareOpts(users, env[state.KeyDomain], xrayDNSServersFromEnv(env), xrayCloudflareOptsFromEnv(env))
	if err != nil {
		return fmt.Errorf("render xray cloudflare config: %w", err)
	}
	// write → restart → restore → env (see RunXHTTPSet): env must not advertise
	// an inbound the running xray does not have.
	if err := applyXrayConfig(ctx, []byte(rendered), resolveRunner(runner), stderr); err != nil {
		return err
	}
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		return fmt.Errorf("save env: %w", err)
	}
	if err := RegenerateSubscriptionsTo(env[state.KeyDomain], stderr); err != nil {
		return fmt.Errorf("regenerate subscriptions: %w", err)
	}
	if enable {
		fmt.Fprintf(stdout, "xhttp-direct enabled: https://%s%s -> 127.0.0.1:%d (%s)\n", host, path, templates.XHTTPDirectPort, templates.XHTTPDirectMode)
	} else {
		fmt.Fprintln(stdout, "xhttp-direct disabled")
	}
	return nil
}
