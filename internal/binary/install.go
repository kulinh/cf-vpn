// internal/binary/install.go
package binary

import (
	"context"
	"fmt"
	"os/exec"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

func Exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func EnsureXray(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if alreadyInstalled {
		return nil
	}
	return r.Run(ctx, "bash", "-lc", "curl -fsSL https://github.com/XTLS/Xray-install/raw/main/install-release.sh | bash")
}

func EnsureCloudflared(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if alreadyInstalled {
		return nil
	}
	cmd := "curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o /usr/local/bin/cloudflared && chmod +x /usr/local/bin/cloudflared"
	if err := r.Run(ctx, "bash", "-lc", cmd); err != nil {
		return fmt.Errorf("install cloudflared: %w", err)
	}
	return nil
}
