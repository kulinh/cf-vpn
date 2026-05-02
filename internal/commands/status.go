package commands

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/xray"
)

// RunStatus prints a compact status summary: unit activity, env values,
// one probe, and the current user count. Transient errors are surfaced as
// "(unavailable: ...)" lines; the command does not fail the whole run for
// any single section.
func RunStatus(ctx context.Context, runner systemd.Runner, stdout io.Writer) error {
	r := resolveRunner(runner)

	for _, unit := range []string{"cfvpn-xray.service", "cfvpn-cloudflared.service"} {
		if err := systemd.IsActive(ctx, r, unit); err == nil {
			fmt.Fprintf(stdout, "%s: active\n", unit)
		} else {
			fmt.Fprintf(stdout, "%s: inactive\n", unit)
		}
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		fmt.Fprintf(stdout, "DOMAIN: (unavailable: %v)\n", err)
	} else {
		adminTunnel := env["ADMIN_TUNNEL_UUID"]
		if adminTunnel == "" {
			adminTunnel = env["TUNNEL_UUID"]
		}
		fmt.Fprintf(stdout, "DOMAIN: %s\n", env["DOMAIN"])
		fmt.Fprintf(stdout, "ADMIN_HOST: %s\n", env["ADMIN_HOST"])
		fmt.Fprintf(stdout, "ADMIN_TUNNEL_UUID: %s\n", adminTunnel)
	}

	domain := ""
	if env != nil {
		domain = env["DOMAIN"]
	}
	if domain == "" {
		fmt.Fprintln(stdout, "probe: skipped (DOMAIN not set)")
	} else if env != nil && IsRealityMode(env) {
		// Reality direct nodes camouflage TLS via dest=, so an HTTPS probe
		// always errors on the handshake. TCP connect is the success signal.
		d := net.Dialer{Timeout: 10 * time.Second}
		conn, perr := d.DialContext(ctx, "tcp", net.JoinHostPort(domain, "443"))
		if perr != nil {
			fmt.Fprintf(stdout, "probe: error: %v\n", perr)
		} else {
			conn.Close()
			fmt.Fprintln(stdout, "probe: reality tcp=open")
		}
	} else {
		// Cloudflare-mode probe: HTTPUpgrade handshake through cloudflared. xray
		// 26.x closes plain GETs, so a real upgrade request is the only signal.
		code, perr := probeHTTPSUpgrade(ctx, domain, templates.VLESSPath)
		if perr != nil {
			fmt.Fprintf(stdout, "probe: error: %v\n", perr)
		} else {
			fmt.Fprintf(stdout, "probe: code=%d\n", code)
		}
	}

	cfg, xerr := xray.Load(xrayConfigPath)
	if xerr != nil {
		fmt.Fprintf(stdout, "users: (unavailable: %v)\n", xerr)
	} else {
		fmt.Fprintf(stdout, "users: %d\n", xray.CountUsers(cfg))
	}

	return nil
}
