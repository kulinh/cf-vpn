// internal/systemd/manager.go
package systemd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if len(trimmed) > 256 {
			trimmed = trimmed[:256] + "..."
		}
		return fmt.Errorf("%s %v failed: %w (%s)", name, args, err, trimmed)
	}
	return nil
}

func DaemonReload(ctx context.Context, r Runner) error {
	return r.Run(ctx, "systemctl", "daemon-reload")
}

func EnableNow(ctx context.Context, r Runner, unit string) error {
	return r.Run(ctx, "systemctl", "enable", "--now", unit)
}

// DisableNow stops the unit and removes it from the boot set in one call
// (`systemctl disable --now`), the inverse of EnableNow.
func DisableNow(ctx context.Context, r Runner, unit string) error {
	return r.Run(ctx, "systemctl", "disable", "--now", unit)
}

func Restart(ctx context.Context, r Runner, unit string) error {
	return r.Run(ctx, "systemctl", "restart", unit)
}

func Reload(ctx context.Context, r Runner, unit string) error {
	return r.Run(ctx, "systemctl", "reload", unit)
}

func IsActive(ctx context.Context, r Runner, unit string) error {
	return r.Run(ctx, "systemctl", "is-active", "--quiet", unit)
}
