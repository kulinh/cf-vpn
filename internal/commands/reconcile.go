package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
)

// canonicalUnits is the single source of truth for every cfvpn systemd unit on
// a node, keyed by filename and rendered against the current config paths.
// Units are deterministic functions of these constant paths (no state, no
// randomness), so rewriting them is always safe and idempotent. Both install
// and reconcile derive their unit set from here. The only input is whether
// HY2 is enabled for this node (HY2_ENABLED in cfvpn.env).
func canonicalUnits() map[string]string {
	env, err := state.Load(envFilePath)
	if err != nil {
		env = map[string]string{}
	}
	return canonicalUnitsFor(Hy2Enabled(env))
}

// hysteriaUnit is the unit reconcile retires when HY2 is disabled.
const hysteriaUnit = "cfvpn-hysteria.service"

// canonicalUnitsFor is canonicalUnits with the HY2 decision made explicit.
func canonicalUnitsFor(hy2 bool) map[string]string {
	units := map[string]string{
		"cfvpn-xray.service":        systemd.XrayService(xrayConfigPath),
		"cfvpn-cloudflared.service": systemd.CloudflaredService(cloudflaredConfig),
		"cfvpn-agent.service":       systemd.AgentService(),
		"cfvpn-cert-renew.service":  systemd.CertRenewService(),
		"cfvpn-cert-renew.timer":    systemd.CertRenewTimer(),
		"cfvpn-healthcheck.service": systemd.HealthcheckService(),
		"cfvpn-healthcheck.timer":   systemd.HealthcheckTimer(),
	}
	if hy2 {
		units[hysteriaUnit] = systemd.HysteriaService(hysteriaConfigPath)
	}
	return units
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
	retired, err := retireHysteriaUnit(ctx, r)
	if err != nil {
		return err
	}
	if len(changed) == 0 && !retired {
		fmt.Fprintln(stdout, "systemd units already in sync")
		return nil
	}
	if err := systemd.DaemonReload(ctx, r); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if retired {
		fmt.Fprintf(stdout, "reconciled %s (HY2 disabled: stopped, disabled, unit removed)\n", hysteriaUnit)
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

// retireHysteriaUnit stops, disables and removes cfvpn-hysteria.service when
// HY2 is disabled for this node and the unit file is still present. Returns
// true when it did something; enabled nodes and already-retired nodes are
// untouched. The hysteria config and cert stay on disk so `hy2 enable` can
// bring the unit straight back.
func retireHysteriaUnit(ctx context.Context, r systemd.Runner) (bool, error) {
	if _, ok := canonicalUnits()[hysteriaUnit]; ok {
		return false, nil
	}
	path := filepath.Join(systemdUnitDir, hysteriaUnit)
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	if err := systemd.DisableNow(ctx, r, hysteriaUnit); err != nil {
		return false, fmt.Errorf("disable %s: %w", hysteriaUnit, err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("remove %s: %w", path, err)
	}
	return true, nil
}
