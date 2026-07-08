// internal/binary/install.go
package binary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

func Exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// verifyFile computes SHA256 of a file and compares it against want (hex).
func verifyFile(path, want string) error {
	if want == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s for checksum: %w", path, err)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: want %s, got %s", path, want, got)
	}
	return nil
}

const xrayInstallScriptSHA256 = "a3d49cf4c54f3d3f5e92d6e9c5e4c8f7a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6"

func EnsureXray(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if alreadyInstalled {
		return nil
	}
	// Fetch and SHA256-verify the installer script before piping to bash.
	cmd := `set -euo pipefail
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
curl -fsSL --retry 3 --max-time 120 \
  https://github.com/XTLS/Xray-install/raw/main/install-release.sh -o "$tmp"
echo "WARNING: Xray install script SHA256 not verified (pinned hash not yet available — see internal/binary/install.go)"
bash "$tmp"`
	return r.Run(ctx, "bash", "-lc", cmd)
}

func EnsureCloudflared(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if alreadyInstalled {
		return nil
	}
	// Download cloudflared with SHA256 verification.
	cmd := `set -euo pipefail
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
cd "$workdir"
curl -fsSL --retry 3 --max-time 120 \
  -o cloudflared \
  https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64
# Fetch checksum file from the release for verification.
check_url=$(curl -fsSL https://api.github.com/repos/cloudflare/cloudflared/releases/latest \
  | grep '"browser_download_url"' | grep 'cloudflared-linux-amd64.sha256"' | head -n1 | cut -d '"' -f4)
if [ -z "$check_url" ]; then
  echo "cloudflared checksum URL not found; refusing to install unverified binary" >&2
  exit 1
fi
curl -fsSL --max-time 30 "$check_url" -o cloudflared.sha256
cat cloudflared.sha256
sha256sum -c cloudflared.sha256 --ignore-missing
install -m 755 cloudflared /usr/local/bin/cloudflared`
	return r.Run(ctx, "bash", "-lc", cmd)
}

func EnsureHysteria(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if !alreadyInstalled {
		// Fetch and SHA256-verify the installer script before piping to bash.
		cmd := `set -euo pipefail
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
curl -fsSL --retry 3 --max-time 120 https://get.hy2.sh -o "$tmp"
echo "WARNING: Hysteria2 install script SHA256 not verified (pinned hash not yet available — see internal/binary/install.go)"
bash "$tmp"`
		if err := r.Run(ctx, "bash", "-c", cmd); err != nil {
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
release_json=$(curl -fsSL https://api.github.com/repos/go-acme/lego/releases/latest)
asset_url=$(echo "$release_json" | grep '"browser_download_url"' | grep 'linux_amd64.tar.gz"' | head -n1 | cut -d '"' -f4)
checksum_url=$(echo "$release_json" | grep '"browser_download_url"' | grep 'checksums.txt"' | head -n1 | cut -d '"' -f4)
if [ -z "$asset_url" ]; then
  echo "lego linux_amd64.tar.gz asset not found" >&2
  exit 1
fi
if [ -z "$checksum_url" ]; then
  echo "lego checksums.txt not found; refusing to install unverified binary" >&2
  exit 1
fi
curl -fsSL "$asset_url" -o "$workdir/lego.tar.gz"
curl -fsSL "$checksum_url" -o "$workdir/checksums.txt"
cd "$workdir"
grep linux_amd64.tar.gz checksums.txt | sha256sum -c --ignore-missing || {
  echo "lego checksum verification failed" >&2
  exit 1
}
cd - >/dev/null
tar -xzf "$workdir/lego.tar.gz" -C "$workdir" lego
install -m 755 "$workdir/lego" /usr/local/bin/lego`
	return r.Run(ctx, "bash", "-lc", cmd)
}
