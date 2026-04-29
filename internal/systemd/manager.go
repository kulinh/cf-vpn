// internal/systemd/manager.go
package systemd

import (
	"context"
	"fmt"
	"os/exec"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v failed: %w (%s)", name, args, err, string(out))
	}
	return nil
}

func DaemonReload(ctx context.Context, r Runner) error {
	return r.Run(ctx, "systemctl", "daemon-reload")
}

func EnableNow(ctx context.Context, r Runner, unit string) error {
	return r.Run(ctx, "systemctl", "enable", "--now", unit)
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
