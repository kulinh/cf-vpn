package commands

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
)

// systemdUnitDir is where cfvpn's systemd unit files are written. Tests
// override this to redirect to a temp directory.
var systemdUnitDir = "/etc/systemd/system"

// IsHealthyCode reports whether a response code from a bare GET against the
// VLESS path is considered healthy. Xray returns 400 / 426 for non-websocket
// requests; anything else (e.g. 502 from cloudflared when xray is down) is
// unhealthy.
func IsHealthyCode(code int) bool { return code == 400 || code == 426 }

// IsRealityMode reports whether the env describes a Reality direct node.
// Reality nodes do not expose a real TLS endpoint on :443 (the handshake is
// camouflaged via Reality dest), so HTTPS probes will always fail and the
// healthcheck must fall back to a TCP connect.
func IsRealityMode(env map[string]string) bool {
	return env["MODE"] == "direct" &&
		env[state.KeyRealityPriv] != "" &&
		env[state.KeyRealityShortID] != ""
}

// RunHealthcheckRun probes the VPN endpoint and prints OK / FAIL.
//   - Reality direct nodes: TCP connect to <DOMAIN>:443 (HTTPS is camouflaged)
//   - everything else:      HTTPS GET <DOMAIN>/api/v1/sync, expect 400/426
//
// Returns a non-nil error on transport failure or an unhealthy response.
func RunHealthcheckRun(ctx context.Context, env map[string]string, stdout io.Writer) error {
	domain := env["DOMAIN"]
	if domain == "" {
		return fmt.Errorf("DOMAIN is empty")
	}
	if IsRealityMode(env) {
		d := net.Dialer{Timeout: 10 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(domain, "443"))
		if err != nil {
			return err
		}
		conn.Close()
		fmt.Fprintln(stdout, "OK reality tcp=open")
		return nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+templates.VLESSPath, nil)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if IsHealthyCode(resp.StatusCode) {
		fmt.Fprintf(stdout, "OK code=%d\n", resp.StatusCode)
		return nil
	}
	return fmt.Errorf("FAIL code=%d", resp.StatusCode)
}

// RunHealthcheckInstall writes the cfvpn-healthcheck.service and
// cfvpn-healthcheck.timer unit files, reloads systemd, and enables the timer.
func RunHealthcheckInstall(ctx context.Context, runner systemd.Runner, stdout io.Writer) error {
	r := resolveRunner(runner)

	servicePath := filepath.Join(systemdUnitDir, "cfvpn-healthcheck.service")
	timerPath := filepath.Join(systemdUnitDir, "cfvpn-healthcheck.timer")

	if err := writeAtomicFile(servicePath, []byte(systemd.HealthcheckService()), 0o644); err != nil {
		return fmt.Errorf("write healthcheck.service: %w", err)
	}
	if err := writeAtomicFile(timerPath, []byte(systemd.HealthcheckTimer()), 0o644); err != nil {
		return fmt.Errorf("write healthcheck.timer: %w", err)
	}

	if err := systemd.DaemonReload(ctx, r); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := systemd.EnableNow(ctx, r, "cfvpn-healthcheck.timer"); err != nil {
		return fmt.Errorf("enable cfvpn-healthcheck.timer: %w", err)
	}

	fmt.Fprintln(stdout, "healthcheck timer installed")
	return nil
}
