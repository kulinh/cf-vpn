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

// WithH3FromEnv copies the node's XHTTP-over-H3 route onto direct-mode render
// inputs. EVERY call site of templates.RenderXrayDirectReality must go through
// it — a path that renders without it deletes the inbound from the running
// config the next time that path executes, and the user only finds out when
// the route stops answering.
//
// A node with no route, or a half-configured one, comes back untouched: the
// renderer rejects a partial H3 set, and a typo in cfvpn.env should not turn
// into a failed restart on an otherwise healthy node.
//
// The certificate is Hysteria2's — the H3 route is served on the same hostname
// — and xray hot-reloads certificates from disk on its own, so renewal needs
// no extra wiring here.
func WithH3FromEnv(in templates.XrayDirectRealityInputs, env map[string]string) templates.XrayDirectRealityInputs {
	host := strings.TrimSpace(env[state.KeyXHTTPH3Host])
	path := strings.TrimSpace(env[state.KeyXHTTPH3Path])
	if host == "" || path == "" {
		return in
	}
	in.H3Host = host
	in.H3Path = path
	in.H3Cert, in.H3Key = HysteriaCertPaths()
	return in
}

// RunXHTTPH3Set enables (host+path) or disables (both empty) the XHTTP-over-H3
// route on a direct-mode node: persists XHTTP_H3_HOST/PATH, re-renders xray
// with the extra inbound on UDP templates.XHTTPH3Port and restarts it, then
// regenerates the node-side subscription files.
//
// The certificate is Hysteria2's, so the host given here must be the one HY2's
// cert was issued for (they share the hostname by design) — there is no second
// ACME order and nothing extra to run at renewal, because xray reloads
// certificates from disk on its own.
func RunXHTTPH3Set(ctx context.Context, host, path string, runner systemd.Runner, stdout, stderr io.Writer) error {
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
		// Same rule as the direct XHTTP route: the path is the only thing
		// standing between a probe and a 200, so it must be long and random.
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
	if mode := env[state.KeyMode]; mode != "direct" {
		return fmt.Errorf("xhttp-h3 needs MODE=direct (this node is MODE=%q)", mode)
	}
	realityParams, ok := loadRealityFromEnv(env)
	if !ok {
		return fmt.Errorf("node is MODE=direct but has no Reality params in %s", envFilePath)
	}
	env[state.KeyXHTTPH3Host] = host
	env[state.KeyXHTTPH3Path] = path
	users, err := usersFromCurrentXray()
	if err != nil {
		return err
	}
	rendered, err := templates.RenderXrayDirectReality(WithH3FromEnv(templates.XrayDirectRealityInputs{
		Users:       users,
		PrivateKey:  realityParams.PrivateKey,
		ShortIDs:    []string{realityParams.ShortID},
		Dest:        realityParams.Dest,
		ServerNames: []string{realityParams.SNI},
		DNSServers:  xrayDNSServersFromEnv(env),
	}, env))
	if err != nil {
		return fmt.Errorf("render xray reality config: %w", err)
	}
	// write → restart → restore → env (see RunXHTTPSet): the env file must
	// never advertise an inbound the running xray does not have, or the next
	// gen-sub hands clients a dead route.
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
		fmt.Fprintf(stdout, "xhttp-h3 enabled: https://%s%s on UDP :%d (%s, alpn h3)\n", host, path, templates.XHTTPH3Port, templates.XHTTPH3Mode)
		fmt.Fprintf(stdout, "remember to open UDP %d in the firewall (and, on OCI, in the VCN security list)\n", templates.XHTTPH3Port)
	} else {
		fmt.Fprintln(stdout, "xhttp-h3 disabled")
	}
	return nil
}
