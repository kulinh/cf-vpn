package commands

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kulinh/cf-vpn/internal/state"
)

// Hy2Enabled reports whether this node should run Hysteria2. A missing key
// means enabled, so every node that predates HY2_ENABLED keeps its HY2 until
// an operator turns it off with `cfvpnctl hy2 disable`.
func Hy2Enabled(env map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(env[state.KeyHy2Enabled])) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// RunHy2Set flips HY2_ENABLED and applies it: reconciles systemd units (which
// stops and disables cfvpn-hysteria, or re-enables it), fixes the ufw rule for
// HY2_PORT and regenerates the node-side subscription files so they stop (or
// start) advertising the HY2 endpoint.
//
// Disabling keeps hysteria/config.yaml, the HY2 cert and every HY2_* env key
// in place: `hy2 enable` is a pure reversal, and the panel's user sync keeps
// the hysteria user list current in the background even while it is off.
func RunHy2Set(ctx context.Context, enable bool, deps InstallDeps, stdout, stderr io.Writer) error {
	unlock, err := AcquireConfigLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire config lock: %w", err)
	}
	defer unlock()

	env, err := state.Load(envFilePath)
	if err != nil {
		return fmt.Errorf("load env: %w", err)
	}
	if enable {
		env[state.KeyHy2Enabled] = "1"
	} else {
		env[state.KeyHy2Enabled] = "0"
	}
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		return fmt.Errorf("save env: %w", err)
	}

	runner := resolveRunner(deps.SystemdRunner)
	if err := runReconcileUnitsLocked(ctx, runner, stdout); err != nil {
		return err
	}

	if port := strings.TrimSpace(env[state.KeyHy2Port]); port != "" {
		ufw := deps.UFW
		if ufw == nil {
			ufw = NewExecUFW()
		}
		var uerr error
		if enable {
			uerr = ufw.Allow(ctx, port+"/udp")
		} else {
			uerr = ufw.Delete(ctx, port+"/udp")
		}
		if uerr != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: ufw rule %s/udp: %v\n", port, uerr)
		}
	}

	if err := RegenerateSubscriptionsTo(env[state.KeyDomain], stderr); err != nil {
		return fmt.Errorf("regenerate subscriptions: %w", err)
	}
	if enable {
		fmt.Fprintln(stdout, "hysteria2 enabled")
	} else {
		fmt.Fprintln(stdout, "hysteria2 disabled")
	}
	return nil
}
