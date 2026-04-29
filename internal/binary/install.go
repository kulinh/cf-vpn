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

func EnsureHysteria(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if !alreadyInstalled {
		if err := r.Run(ctx, "bash", "-c", "curl -fsSL https://get.hy2.sh | bash"); err != nil {
			return fmt.Errorf("install hysteria: %w", err)
		}
	}
	disable := `systemctl disable --now hysteria-server.service 2>/dev/null || true; ` +
		`systemctl disable hysteria-server@.service 2>/dev/null || true`
	if err := r.Run(ctx, "bash", "-c", disable); err != nil {
		return fmt.Errorf("disable installer hysteria-server units: %w", err)
	}
	return nil
}

func EnsureLego(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if alreadyInstalled {
		return nil
	}
	cmd := `set -euo pipefail
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
asset_url=$(curl -fsSL https://api.github.com/repos/go-acme/lego/releases/latest | grep '"browser_download_url"' | grep 'linux_amd64.tar.gz"' | head -n1 | cut -d '"' -f4)
if [ -z "$asset_url" ]; then
  echo "lego linux_amd64.tar.gz asset not found" >&2
  exit 1
fi
curl -fsSL "$asset_url" -o "$workdir/lego.tar.gz"
tar -xzf "$workdir/lego.tar.gz" -C "$workdir" lego
install -m 755 "$workdir/lego" /usr/local/bin/lego
chmod 755 /usr/local/bin/lego`
	if err := r.Run(ctx, "bash", "-lc", cmd); err != nil {
		return fmt.Errorf("install lego: %w", err)
	}
	return nil
}
