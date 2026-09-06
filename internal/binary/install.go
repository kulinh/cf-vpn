// internal/binary/install.go
package binary

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// EnsureCloudflared downloads cloudflared into a scratch directory, verifies
// its SHA256 in Go, and only then installs it.
//
// H4: the previous version saved the download as "cloudflared" and ran
// `sha256sum -c cloudflared.sha256 --ignore-missing`, but the release checksum
// names the asset "cloudflared-linux-amd64". Nothing matched, sha256sum exited
// 1 with "no file was verified", and `set -euo pipefail` aborted the whole
// step — a fresh node could never install cloudflared. Verification now happens
// in this process, against the file that was actually downloaded, and a missing
// entry is a hard failure instead of a pass.
func EnsureCloudflared(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if alreadyInstalled {
		return nil
	}
	dir, err := os.MkdirTemp("", "cfvpn-cloudflared-*")
	if err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	defer os.RemoveAll(dir)

	download := `set -euo pipefail
workdir="$1"
cd "$workdir"
curl -fsSL --retry 3 --max-time 120 -o cloudflared-linux-amd64 \
  https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64
check_url=$(curl -fsSL https://api.github.com/repos/cloudflare/cloudflared/releases/latest \
  | grep '"browser_download_url"' | grep 'cloudflared-linux-amd64.sha256"' | head -n1 | cut -d '"' -f4)
if [ -z "$check_url" ]; then
  echo "cloudflared checksum URL not found; refusing to install unverified binary" >&2
  exit 1
fi
curl -fsSL --max-time 30 "$check_url" -o checksums.txt`
	if err := r.Run(ctx, "bash", "-lc", download, "cfvpn-install", dir); err != nil {
		return fmt.Errorf("download cloudflared: %w", err)
	}

	checksums, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return fmt.Errorf("read cloudflared checksums: %w", err)
	}
	if err := VerifyFileSHA256(filepath.Join(dir, "cloudflared-linux-amd64"), checksums); err != nil {
		return fmt.Errorf("verify cloudflared: %w", err)
	}

	install := `set -euo pipefail
install -m 755 "$1/cloudflared-linux-amd64" /usr/local/bin/cloudflared`
	if err := r.Run(ctx, "bash", "-lc", install, "cfvpn-install", dir); err != nil {
		return fmt.Errorf("install cloudflared: %w", err)
	}
	return nil
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

// EnsureLego downloads the latest lego release, verifies its SHA256 in Go, and
// installs it. Same H4 defect as cloudflared: the asset was saved as
// "lego.tar.gz" while checksums.txt names it "lego_vX.Y.Z_linux_amd64.tar.gz",
// so `sha256sum -c --ignore-missing` verified nothing.
func EnsureLego(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if alreadyInstalled {
		return nil
	}
	dir, err := os.MkdirTemp("", "cfvpn-lego-*")
	if err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	defer os.RemoveAll(dir)

	download := `set -euo pipefail
workdir="$1"
cd "$workdir"
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
asset_name=$(basename "$asset_url")
# Keep the release's own file name: it is the name checksums.txt lists.
curl -fsSL --retry 3 --max-time 120 "$asset_url" -o "$asset_name"
curl -fsSL --max-time 30 "$checksum_url" -o checksums.txt
printf '%s' "$asset_name" > asset_name`
	if err := r.Run(ctx, "bash", "-lc", download, "cfvpn-install", dir); err != nil {
		return fmt.Errorf("download lego: %w", err)
	}

	rawName, err := os.ReadFile(filepath.Join(dir, "asset_name"))
	if err != nil {
		return fmt.Errorf("read lego asset name: %w", err)
	}
	assetName := filepath.Base(strings.TrimSpace(string(rawName)))
	if assetName == "" || assetName == "." || assetName == string(filepath.Separator) {
		return fmt.Errorf("lego asset name is empty")
	}
	checksums, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return fmt.Errorf("read lego checksums: %w", err)
	}
	if err := VerifyFileSHA256(filepath.Join(dir, assetName), checksums); err != nil {
		return fmt.Errorf("verify lego: %w", err)
	}

	install := `set -euo pipefail
workdir="$1"
asset_name="$2"
tar -xzf "$workdir/$asset_name" -C "$workdir" lego
install -m 755 "$workdir/lego" /usr/local/bin/lego`
	if err := r.Run(ctx, "bash", "-lc", install, "cfvpn-install", dir, assetName); err != nil {
		return fmt.Errorf("install lego: %w", err)
	}
	return nil
}
