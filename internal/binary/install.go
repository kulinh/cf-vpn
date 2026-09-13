// internal/binary/install.go
package binary

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// goarch is the CPU architecture the release assets are picked for. A package
// variable (not runtime.GOARCH inline) so tests can exercise the arm64 names on
// an amd64 runner. Both upstreams publish linux/amd64 and linux/arm64 builds
// under these exact suffixes; anything else has no asset to fetch.
var goarch = runtime.GOARCH

// assetArch maps the Go arch to the suffix cloudflared, lego and Hysteria use in
// their release file names (they happen to agree). Xray names its archives
// differently, see xrayAsset.
func assetArch() (string, error) {
	switch goarch {
	case "amd64", "arm64":
		return goarch, nil
	}
	return "", fmt.Errorf("no linux/%s release assets for cloudflared, lego and hysteria (supported: amd64, arm64)", goarch)
}

// xrayAsset is the Xray-core release archive for the CPU arch.
func xrayAsset() (string, error) {
	switch goarch {
	case "amd64":
		return "Xray-linux-64.zip", nil
	case "arm64":
		return "Xray-linux-arm64-v8a.zip", nil
	}
	return "", fmt.Errorf("no linux/%s Xray release asset (supported: amd64, arm64)", goarch)
}

// XrayAssetDir receives geoip.dat and geosite.dat. It must match the
// XRAY_LOCATION_ASSET line in systemd.XrayService() — the path the retired
// Xray-install script used, so existing nodes keep working unchanged.
const XrayAssetDir = "/usr/local/share/xray"

// xrayArchiveFiles are the files taken out of the Xray release archive. The
// routing block references geoip:private, so the dat files are not optional.
var xrayArchiveFiles = []string{"xray", "geoip.dat", "geosite.dat"}

// maxExtractedFile bounds each file taken out of a release archive. The archive
// is checksum-verified before extraction; this only stops a corrupt one from
// filling the disk. Current Xray files are at most ~40 MiB.
const maxExtractedFile = 512 << 20

type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

func Exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// EnsureXray downloads the latest Xray-core release archive, verifies it
// against the release's own .dgst file in Go, and installs the binary plus the
// geoip/geosite data files.
//
// M5: this used to run XTLS/Xray-install's install-release.sh straight from
// the main branch with no integrity check at all. Everything else that script
// did (its own xray.service, /usr/local/etc/xray, /var/log/xray, the nobody
// user) is unused here: cfvpn writes and runs its own cfvpn-xray.service as
// root against /etc/cfvpn/xray/config.json.
//
// The asset and its .dgst are both fetched through releases/latest. A release
// published between the two requests yields a mismatch, which fails closed.
func EnsureXray(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if alreadyInstalled {
		return nil
	}
	asset, err := xrayAsset()
	if err != nil {
		return fmt.Errorf("xray: %w", err)
	}
	dir, err := os.MkdirTemp("", "cfvpn-xray-*")
	if err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	defer os.RemoveAll(dir)

	download := `set -euo pipefail
workdir="$1"
asset="$2"
cd "$workdir"
base=https://github.com/XTLS/Xray-core/releases/latest/download
curl -fsSL --retry 3 --max-time 300 -o "$asset" "$base/$asset"
curl -fsSL --retry 3 --max-time 30 -o "$asset.dgst" "$base/$asset.dgst"`
	if err := r.Run(ctx, "bash", "-lc", download, "cfvpn-install", dir, asset); err != nil {
		return fmt.Errorf("download xray: %w", err)
	}

	dgst, err := os.ReadFile(filepath.Join(dir, asset+".dgst"))
	if err != nil {
		return fmt.Errorf("read xray digest file: %w", err)
	}
	if err := VerifyFileSHA256Dgst(filepath.Join(dir, asset), dgst); err != nil {
		return fmt.Errorf("verify xray: %w", err)
	}

	// Extract in Go: unzip is not part of a minimal Debian/Ubuntu image.
	extracted := filepath.Join(dir, "extracted")
	if err := os.Mkdir(extracted, 0o700); err != nil {
		return fmt.Errorf("create extract dir: %w", err)
	}
	if err := extractZipFiles(filepath.Join(dir, asset), extracted, xrayArchiveFiles); err != nil {
		return fmt.Errorf("extract xray: %w", err)
	}

	// Each file lands under a temporary name and is renamed over the old one:
	// with --binaries the xray being replaced is running, and writing into a
	// busy executable fails with ETXTBSY.
	install := `set -euo pipefail
src="$1"
assets="$2"
install -d -m 755 "$assets"
for f in geoip.dat geosite.dat; do
  install -m 644 "$src/$f" "$assets/.$f.cfvpn-new"
  mv -f "$assets/.$f.cfvpn-new" "$assets/$f"
done
install -m 755 "$src/xray" /usr/local/bin/.xray.cfvpn-new
mv -f /usr/local/bin/.xray.cfvpn-new /usr/local/bin/xray`
	if err := r.Run(ctx, "bash", "-lc", install, "cfvpn-install", extracted, XrayAssetDir); err != nil {
		return fmt.Errorf("install xray: %w", err)
	}
	return nil
}

// extractZipFiles copies the named top-level entries of a zip archive into
// destDir. Entry names are matched exactly and never used as paths, so an
// archive cannot write outside destDir. Every name must be present.
func extractZipFiles(zipPath, destDir string, names []string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(zipPath), err)
	}
	defer zr.Close()

	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	for _, name := range names {
		f, ok := byName[name]
		if !ok {
			return fmt.Errorf("%s not found in %s", name, filepath.Base(zipPath))
		}
		if err := extractZipFile(f, filepath.Join(destDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(f *zip.File, dest string) error {
	if f.UncompressedSize64 > maxExtractedFile {
		return fmt.Errorf("%s is %d bytes, over the %d byte limit", f.Name, f.UncompressedSize64, maxExtractedFile)
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open %s in archive: %w", f.Name, err)
	}
	defer rc.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	// The size header is not trusted: read at most one byte past the limit.
	n, err := io.Copy(out, io.LimitReader(rc, maxExtractedFile+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("extract %s: %w", f.Name, err)
	}
	if n > maxExtractedFile {
		return fmt.Errorf("%s exceeds the %d byte limit", f.Name, maxExtractedFile)
	}
	return nil
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
	arch, err := assetArch()
	if err != nil {
		return fmt.Errorf("cloudflared: %w", err)
	}
	asset := "cloudflared-linux-" + arch
	dir, err := os.MkdirTemp("", "cfvpn-cloudflared-*")
	if err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	defer os.RemoveAll(dir)

	// The asset name is passed as a positional argument, never interpolated
	// into the script text.
	download := `set -euo pipefail
workdir="$1"
asset="$2"
cd "$workdir"
curl -fsSL --retry 3 --max-time 120 -o "$asset" \
  "https://github.com/cloudflare/cloudflared/releases/latest/download/$asset"
# The trailing "|| true" matters: under set -euo pipefail a non-matching grep,
# or the SIGPIPE from head closing the pipe early, aborts the script right here
# and makes the "refusing to install unverified binary" check below unreachable.
check_url=$(curl -fsSL https://api.github.com/repos/cloudflare/cloudflared/releases/latest \
  | grep '"browser_download_url"' | grep "$asset.sha256\"" | head -n1 | cut -d '"' -f4 || true)
if [ -z "$check_url" ]; then
  echo "cloudflared checksum URL not found; refusing to install unverified binary" >&2
  exit 1
fi
curl -fsSL --max-time 30 "$check_url" -o checksums.txt`
	if err := r.Run(ctx, "bash", "-lc", download, "cfvpn-install", dir, asset); err != nil {
		return fmt.Errorf("download cloudflared: %w", err)
	}

	checksums, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return fmt.Errorf("read cloudflared checksums: %w", err)
	}
	// cloudflared's checksum file is fetched per asset and often holds nothing
	// but the digest, so this caller opts into the bare-digest form. lego's
	// checksums.txt lists every asset and must match by name.
	if err := VerifyFileSHA256BareDigestAllowed(filepath.Join(dir, asset), checksums); err != nil {
		return fmt.Errorf("verify cloudflared: %w", err)
	}

	install := `set -euo pipefail
install -m 755 "$1/$2" /usr/local/bin/cloudflared`
	if err := r.Run(ctx, "bash", "-lc", install, "cfvpn-install", dir, asset); err != nil {
		return fmt.Errorf("install cloudflared: %w", err)
	}
	return nil
}

// EnsureHysteria downloads the latest Hysteria release binary, verifies it
// against the release's hashes.txt in Go, and installs it.
//
// M5: this used to run get.hy2.sh with no integrity check. That script also
// created a hysteria user, /etc/hysteria and hysteria-server units; none of
// them is used — cfvpn-hysteria.service runs /usr/local/bin/hysteria as root
// with /etc/cfvpn/hysteria/config.yaml. The disable step below still runs on
// every call, for nodes that were provisioned through the script.
func EnsureHysteria(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if !alreadyInstalled {
		if err := installHysteria(ctx, r); err != nil {
			return err
		}
	}
	disable := `systemctl disable --now hysteria-server.service 2>/dev/null || true; ` +
		`systemctl disable hysteria-server@.service 2>/dev/null || true`
	if err := r.Run(ctx, "bash", "-c", disable); err != nil {
		return fmt.Errorf("disable installer hysteria-server units: %w", err)
	}
	return nil
}

func installHysteria(ctx context.Context, r Runner) error {
	arch, err := assetArch()
	if err != nil {
		return fmt.Errorf("hysteria: %w", err)
	}
	asset := "hysteria-linux-" + arch
	dir, err := os.MkdirTemp("", "cfvpn-hysteria-*")
	if err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	defer os.RemoveAll(dir)

	download := `set -euo pipefail
workdir="$1"
asset="$2"
cd "$workdir"
base=https://github.com/apernet/hysteria/releases/latest/download
curl -fsSL --retry 3 --max-time 120 -o "$asset" "$base/$asset"
curl -fsSL --retry 3 --max-time 30 -o hashes.txt "$base/hashes.txt"`
	if err := r.Run(ctx, "bash", "-lc", download, "cfvpn-install", dir, asset); err != nil {
		return fmt.Errorf("download hysteria: %w", err)
	}

	hashes, err := os.ReadFile(filepath.Join(dir, "hashes.txt"))
	if err != nil {
		return fmt.Errorf("read hysteria hashes: %w", err)
	}
	// hashes.txt lists every build as "<sha256>  build/hysteria-<os>-<arch>";
	// ExpectedSHA256 matches on the base name, so "-avx" variants never match.
	if err := VerifyFileSHA256(filepath.Join(dir, asset), hashes); err != nil {
		return fmt.Errorf("verify hysteria: %w", err)
	}

	// Rename over the old binary for the same ETXTBSY reason as xray.
	install := `set -euo pipefail
install -m 755 "$1/$2" /usr/local/bin/.hysteria.cfvpn-new
mv -f /usr/local/bin/.hysteria.cfvpn-new /usr/local/bin/hysteria`
	if err := r.Run(ctx, "bash", "-lc", install, "cfvpn-install", dir, asset); err != nil {
		return fmt.Errorf("install hysteria: %w", err)
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
	arch, err := assetArch()
	if err != nil {
		return fmt.Errorf("lego: %w", err)
	}
	suffix := "linux_" + arch + ".tar.gz"
	dir, err := os.MkdirTemp("", "cfvpn-lego-*")
	if err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	defer os.RemoveAll(dir)

	download := `set -euo pipefail
workdir="$1"
suffix="$2"
cd "$workdir"
release_json=$(curl -fsSL https://api.github.com/repos/go-acme/lego/releases/latest)
# Trailing "|| true" for the same reason as in EnsureCloudflared: a missing
# asset must reach the explicit check below, not kill the script mid-pipeline.
asset_url=$(echo "$release_json" | grep '"browser_download_url"' | grep "$suffix\"" | head -n1 | cut -d '"' -f4 || true)
checksum_url=$(echo "$release_json" | grep '"browser_download_url"' | grep 'checksums.txt"' | head -n1 | cut -d '"' -f4 || true)
if [ -z "$asset_url" ]; then
  echo "lego $suffix asset not found" >&2
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
	if err := r.Run(ctx, "bash", "-lc", download, "cfvpn-install", dir, suffix); err != nil {
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
