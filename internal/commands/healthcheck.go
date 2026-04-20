package commands

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/kulinh/cf-vpn/internal/systemd"
)

// systemdUnitDir is where cfvpn's systemd unit files are written. Tests
// override this to redirect to a temp directory.
var systemdUnitDir = "/etc/systemd/system"

// IsHealthyCode reports whether a response code from a bare GET against the
// VLESS path is considered healthy. Xray returns 400 / 426 for non-websocket
// requests; anything else (e.g. 502 from cloudflared when xray is down) is
// unhealthy.
func IsHealthyCode(code int) bool { return code == 400 || code == 426 }

// RunHealthcheckRun probes https://<domain>/vless and prints OK / FAIL.
// Returns a non-nil error on transport failure or an unhealthy response code.
func RunHealthcheckRun(ctx context.Context, domain string, stdout io.Writer) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/vless", nil)
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
