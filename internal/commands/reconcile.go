package commands

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kulinh/cf-vpn/internal/systemd"
)

// canonicalUnits is the single source of truth for every cfvpn systemd unit on
// a node, keyed by filename and rendered against the current config paths.
// Units are deterministic functions of these constant paths (no state, no
// randomness), so rewriting them is always safe and idempotent. Both install
// and reconcile derive their unit set from here.
func canonicalUnits() map[string]string {
	return map[string]string{
		"cfvpn-xray.service":        systemd.XrayService(xrayConfigPath),
		"cfvpn-cloudflared.service": systemd.CloudflaredService(cloudflaredConfig),
		"cfvpn-agent.service":       systemd.AgentService(),
		"cfvpn-hysteria.service":    systemd.HysteriaService(hysteriaConfigPath),
		"cfvpn-cert-renew.service":  systemd.CertRenewService(),
		"cfvpn-cert-renew.timer":    systemd.CertRenewTimer(),
		"cfvpn-healthcheck.service": systemd.HealthcheckService(),
		"cfvpn-healthcheck.timer":   systemd.HealthcheckTimer(),
	}
}

// longRunningUnits are the daemon services that must be restarted to pick up a
// changed unit file. Oneshot services (cert-renew, healthcheck) are driven by
// timers, so a daemon-reload is enough — restarting them would run the job
// immediately for no reason.
var longRunningUnits = map[string]bool{
	"cfvpn-xray.service":        true,
	"cfvpn-cloudflared.service": true,
	"cfvpn-agent.service":       true,
	"cfvpn-hysteria.service":    true,
}

// reconcileUnits rewrites any canonical unit whose on-disk content differs and
// returns the changed filenames in deterministic (sorted) order. It performs no
// systemd actions — the caller decides whether to daemon-reload and restart.
func reconcileUnits() ([]string, error) {
	var changed []string
	for name, content := range canonicalUnits() {
		didChange, err := writeIfChanged(filepath.Join(systemdUnitDir, name), []byte(content), 0o644)
		if err != nil {
			return changed, fmt.Errorf("write %s: %w", name, err)
		}
		if didChange {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed, nil
}

// RunReconcileUnits brings this node's systemd unit files back in line with the
// canonical templates. It rewrites only drifted units, then daemon-reloads and
// restarts/re-enables exactly those that changed. Safe to run at any time; a
// node already in sync makes no systemd calls.
//
// H11: it restarts xray/hysteria, so it takes the same config lock as every
// other writer. Callers that already hold the lock (RunUpgrade's in-place
// re-render) must use runReconcileUnitsLocked — flock does not nest.
func RunReconcileUnits(ctx context.Context, runner systemd.Runner, stdout io.Writer) error {
	unlock, err := AcquireConfigLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire config lock: %w", err)
	}
	defer unlock()
	return runReconcileUnitsLocked(ctx, runner, stdout)
}

// runReconcileUnitsLocked is RunReconcileUnits for callers already holding the
// config lock.
func runReconcileUnitsLocked(ctx context.Context, runner systemd.Runner, stdout io.Writer) error {
	r := resolveRunner(runner)
	changed, err := reconcileUnits()
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Fprintln(stdout, "systemd units already in sync")
		return nil
	}
	if err := systemd.DaemonReload(ctx, r); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	for _, name := range changed {
		switch {
		case strings.HasSuffix(name, ".timer"):
			if err := systemd.EnableNow(ctx, r, name); err != nil {
				return fmt.Errorf("enable %s: %w", name, err)
			}
		case longRunningUnits[name]:
			if err := systemd.Restart(ctx, r, name); err != nil {
				return fmt.Errorf("restart %s: %w", name, err)
			}
		}
		fmt.Fprintf(stdout, "reconciled %s\n", name)
	}
	return nil
}
