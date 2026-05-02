package commands

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
)

// systemdUnitDir is where cfvpn's systemd unit files are written. Tests
// override this to redirect to a temp directory.
var systemdUnitDir = "/etc/systemd/system"

// IsHealthyCode reports whether a response code is considered healthy.
//   - 101: WebSocket upgrade accepted (HTTPUpgrade probe with proper headers).
//
// Anything else is unhealthy. Earlier xray versions returned 400/426 on a
// plain GET, but xray 26.x's HTTPUpgrade transport closes the connection
// without writing a status line, so a real upgrade handshake is the only
// reliable signal — the operator-side probe sends one explicitly.
func IsHealthyCode(code int) bool { return code == 101 }

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
//   - Reality direct nodes: TCP connect to <DOMAIN>:443 (HTTPS is camouflaged).
//   - cloudflare-mode nodes: TLS handshake to <DOMAIN>:443, then a real
//     WebSocket-style upgrade request to the VLESS path; expect HTTP 101.
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
	code, err := probeHTTPSUpgrade(ctx, domain, templates.VLESSPath)
	if err != nil {
		return err
	}
	if IsHealthyCode(code) {
		fmt.Fprintf(stdout, "OK code=%d\n", code)
		return nil
	}
	return fmt.Errorf("FAIL code=%d", code)
}

// probeHTTPSUpgrade dials TLS to host:443, sends a minimal WebSocket-style
// upgrade request to path, and returns the parsed HTTP status code. Mirrors
// the agent-side raw-TCP probe (cmd/cfvpn-agent: probeHTTPUpgrade) but tunnels
// through TLS so it works against the public hostname (which on cloudflare-
// mode nodes routes via cloudflared into xray's HTTPUpgrade inbound).
func probeHTTPSUpgrade(ctx context.Context, host, path string) (int, error) {
	d := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{ServerName: host},
	}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	deadline := time.Now().Add(10 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return 0, err
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return 0, err
	}
	parts := strings.SplitN(strings.TrimRight(line, "\r\n"), " ", 3)
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid status line: %q", line)
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid status code %q: %w", parts[1], err)
	}
	return code, nil
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
