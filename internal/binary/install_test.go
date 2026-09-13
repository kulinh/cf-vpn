package binary

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type FakeRunner struct{ Calls [][]string }

func (f *FakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	return nil
}

// downloadFaker stands in for the shell: on the first (download) invocation it
// drops the files the real curl commands would leave in the work dir.
type downloadFaker struct {
	Calls [][]string
	// files to create in the work dir, keyed by name, on the download call.
	files map[string]string
	// onDownload, when set, overrides files for a case-specific layout.
	onDownload func(dir string) error
	// onInstall, when set, inspects the directory handed to the install call.
	onInstall func(dir string) error
	err       error
}

func (d *downloadFaker) Run(_ context.Context, name string, args ...string) error {
	d.Calls = append(d.Calls, append([]string{name}, args...))
	if d.err != nil {
		return d.err
	}
	// The work dir is the first positional argument after $0.
	if len(args) < 4 {
		return nil
	}
	dir := args[3]
	if len(d.Calls) != 1 {
		if len(d.Calls) == 2 && d.onInstall != nil {
			return d.onInstall(dir)
		}
		return nil // the install call: nothing to fake
	}
	if d.onDownload != nil {
		return d.onDownload(dir)
	}
	for name, content := range d.files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestEnsureXraySkipsWhenAlreadyPresent(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureXray(context.Background(), fake, true); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("expected no install calls when already installed")
	}
}

// zipOf builds an in-memory zip archive holding files.
func zipOf(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// xrayDgst renders an Xray-core .dgst file in the upstream layout.
func xrayDgst(body string) string {
	return "MD5= ee4e2ff74948a9b464624b1cabc44409\n" +
		"SHA1= b55b06e74e89083b9cedfdecf0d68b579cd2af72\n" +
		"SHA2-256= " + sha256Hex(body) + "\n" +
		"SHA2-512= e8bc40a0687cac184bbe4b5c1f047e69064ccedc489fb25e208889ae287bbf87\n"
}

var xrayArchive = map[string]string{
	"xray":        "fake-xray-binary",
	"geoip.dat":   "fake-geoip",
	"geosite.dat": "fake-geosite",
	"LICENSE":     "license",
	"README.md":   "readme",
}

// M5: the release archive is verified against its .dgst, the binary and both
// dat files are extracted, and only then is anything installed. The retired
// install-release.sh must not be fetched at all.
func TestEnsureXrayVerifiesExtractsThenInstalls(t *testing.T) {
	archive := zipOf(t, xrayArchive)
	var installed map[string]string
	fake := &downloadFaker{
		files: map[string]string{
			"Xray-linux-64.zip":      archive,
			"Xray-linux-64.zip.dgst": xrayDgst(archive),
		},
		onInstall: func(dir string) error {
			installed = map[string]string{}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return err
			}
			for _, e := range entries {
				b, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err != nil {
					return err
				}
				installed[e.Name()] = string(b)
			}
			return nil
		},
	}
	if err := EnsureXray(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("expected download + install calls, got %#v", fake.Calls)
	}
	download := strings.Join(fake.Calls[0], " ")
	for _, want := range []string{
		"https://github.com/XTLS/Xray-core/releases/latest/download",
		"Xray-linux-64.zip",
		".dgst",
	} {
		if !strings.Contains(download, want) {
			t.Errorf("download call missing %q: %s", want, download)
		}
	}
	if strings.Contains(download, "install-release.sh") || strings.Contains(download, "Xray-install") {
		t.Errorf("the unverified install script must not be fetched: %s", download)
	}
	want := map[string]string{"xray": "fake-xray-binary", "geoip.dat": "fake-geoip", "geosite.dat": "fake-geosite"}
	if !reflect.DeepEqual(installed, want) {
		t.Errorf("install dir held %v, want exactly %v", installed, want)
	}
	install := strings.Join(fake.Calls[1], " ")
	for _, want := range []string{"/usr/local/bin/xray", XrayAssetDir, "geoip.dat", "geosite.dat", "mv -f"} {
		if !strings.Contains(install, want) {
			t.Errorf("install call missing %q: %s", want, install)
		}
	}
}

func TestEnsureXrayRefusesOnChecksumMismatch(t *testing.T) {
	fake := &downloadFaker{files: map[string]string{
		"Xray-linux-64.zip":      zipOf(t, xrayArchive),
		"Xray-linux-64.zip.dgst": xrayDgst("the-real-archive"),
	}}
	err := EnsureXray(context.Background(), fake, false)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err = %v, want a sha256 mismatch", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("install ran despite the mismatch: %#v", fake.Calls)
	}
}

func TestEnsureXrayRefusesWithoutSHA256InDgst(t *testing.T) {
	archive := zipOf(t, xrayArchive)
	fake := &downloadFaker{files: map[string]string{
		"Xray-linux-64.zip":      archive,
		"Xray-linux-64.zip.dgst": "MD5= ee4e2ff74948a9b464624b1cabc44409\n",
	}}
	err := EnsureXray(context.Background(), fake, false)
	if err == nil || !strings.Contains(err.Error(), "no SHA2-256 entry") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("install ran without a digest: %#v", fake.Calls)
	}
}

// The routing block needs geoip.dat; an archive without the dat files must not
// half-install a binary that cannot load its config.
func TestEnsureXrayRefusesArchiveMissingDatFiles(t *testing.T) {
	archive := zipOf(t, map[string]string{"xray": "bin", "geoip.dat": "g"})
	fake := &downloadFaker{files: map[string]string{
		"Xray-linux-64.zip":      archive,
		"Xray-linux-64.zip.dgst": xrayDgst(archive),
	}}
	err := EnsureXray(context.Background(), fake, false)
	if err == nil || !strings.Contains(err.Error(), "geosite.dat not found") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("install ran with an incomplete archive: %#v", fake.Calls)
	}
}

// Entry names are matched, never used as paths.
func TestExtractZipFilesIgnoresPathEntries(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "a.zip")
	if err := os.WriteFile(zipPath, []byte(zipOf(t, map[string]string{"../xray": "evil", "sub/geoip.dat": "evil"})), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractZipFiles(zipPath, out, []string{"xray"}); err == nil {
		t.Fatal("a traversal entry satisfied a top-level name")
	}
	if _, err := os.Stat(filepath.Join(dir, "xray")); !os.IsNotExist(err) {
		t.Fatalf("archive wrote outside the destination: %v", err)
	}
}

func TestEnsureCloudflaredSkipsWhenAlreadyPresent(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureCloudflared(context.Background(), fake, true); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("expected no install calls")
	}
}

// H4: the binary is installed only when its sha256 matches the release's
// checksum file entry.
func TestEnsureCloudflaredVerifiesThenInstalls(t *testing.T) {
	const body = "fake-cloudflared-binary"
	fake := &downloadFaker{files: map[string]string{
		"cloudflared-linux-amd64": body,
		"checksums.txt":           sha256Hex(body) + "  cloudflared-linux-amd64\n",
	}}
	if err := EnsureCloudflared(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("expected download + install calls, got %#v", fake.Calls)
	}
	download := strings.Join(fake.Calls[0], " ")
	if !strings.Contains(download, "cloudflared-linux-amd64") {
		t.Errorf("download call does not fetch the release asset: %s", download)
	}
	if strings.Contains(download, "--ignore-missing") || strings.Contains(download, "sha256sum -c") {
		t.Errorf("verification must happen in Go, not via sha256sum -c: %s", download)
	}
	install := strings.Join(fake.Calls[1], " ")
	if !strings.Contains(install, "install -m 755") || !strings.Contains(install, "/usr/local/bin/cloudflared") {
		t.Errorf("install call = %s", install)
	}
}

// The old flow reported success while verifying nothing. A tampered download
// must abort before the install call.
func TestEnsureCloudflaredRefusesOnChecksumMismatch(t *testing.T) {
	fake := &downloadFaker{files: map[string]string{
		"cloudflared-linux-amd64": "tampered-binary",
		"checksums.txt":           sha256Hex("the-real-binary") + "  cloudflared-linux-amd64\n",
	}}
	err := EnsureCloudflared(context.Background(), fake, false)
	if err == nil {
		t.Fatal("installed a binary whose checksum did not match")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("install ran despite the mismatch: %#v", fake.Calls)
	}
}

// A checksums file that does not mention the downloaded file must fail — this
// is exactly what `--ignore-missing` used to swallow.
func TestEnsureCloudflaredRefusesWhenChecksumEntryMissing(t *testing.T) {
	fake := &downloadFaker{files: map[string]string{
		"cloudflared-linux-amd64": "body",
		"checksums.txt":           sha256Hex("body") + "  some-other-file\n",
	}}
	err := EnsureCloudflared(context.Background(), fake, false)
	if err == nil {
		t.Fatal("installed a binary with no checksum entry")
	}
	if !strings.Contains(err.Error(), "no sha256 entry") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("install ran despite the missing entry: %#v", fake.Calls)
	}
}

// cloudflared publishes <asset>.sha256 holding just the digest.
func TestEnsureCloudflaredAcceptsBareDigestChecksumFile(t *testing.T) {
	const body = "fake-cloudflared-binary"
	fake := &downloadFaker{files: map[string]string{
		"cloudflared-linux-amd64": body,
		"checksums.txt":           sha256Hex(body) + "\n",
	}}
	if err := EnsureCloudflared(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("expected download + install, got %#v", fake.Calls)
	}
}

func TestEnsureHysteriaSkipsInstallerButDisablesDefaultUnit(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureHysteria(context.Background(), fake, true); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 disable call when binary already installed, got %d: %#v", len(fake.Calls), fake.Calls)
	}
	if !strings.Contains(fake.Calls[0][2], "disable --now hysteria-server.service") {
		t.Fatalf("expected disable command, got %#v", fake.Calls[0])
	}
}

// hysteriaHashes renders hashes.txt in the upstream layout: every build, with a
// build/ prefix, including the -avx variant whose name extends ours.
func hysteriaHashes(arch, body string) string {
	return sha256Hex("avx") + "  build/hysteria-linux-" + arch + "-avx\n" +
		sha256Hex(body) + "  build/hysteria-linux-" + arch + "\n" +
		sha256Hex("windows") + "  build/hysteria-windows-amd64.exe\n"
}

// M5: the release binary is verified against hashes.txt before install, and
// the installer's default units are still disabled afterwards.
func TestEnsureHysteriaVerifiesInstallsThenDisablesDefaultUnit(t *testing.T) {
	const body = "fake-hysteria-binary"
	fake := &downloadFaker{files: map[string]string{
		"hysteria-linux-amd64": body,
		"hashes.txt":           hysteriaHashes("amd64", body),
	}}
	if err := EnsureHysteria(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 3 {
		t.Fatalf("expected download + install + disable, got %d: %#v", len(fake.Calls), fake.Calls)
	}
	download := strings.Join(fake.Calls[0], " ")
	for _, want := range []string{"https://github.com/apernet/hysteria/releases/latest/download", "hysteria-linux-amd64", "hashes.txt"} {
		if !strings.Contains(download, want) {
			t.Errorf("download call missing %q: %s", want, download)
		}
	}
	if strings.Contains(download, "get.hy2.sh") {
		t.Errorf("the unverified install script must not be fetched: %s", download)
	}
	install := strings.Join(fake.Calls[1], " ")
	if !strings.Contains(install, "/usr/local/bin/hysteria") || !strings.Contains(install, "hysteria-linux-amd64") {
		t.Errorf("install call = %s", install)
	}
	if !strings.Contains(strings.Join(fake.Calls[2], " "), "disable --now hysteria-server.service") {
		t.Fatalf("expected disable command, got %#v", fake.Calls[2])
	}
}

// A tampered binary must stop before install; the -avx line with the matching
// digest of a different file must not be accepted either.
func TestEnsureHysteriaRefusesOnChecksumMismatch(t *testing.T) {
	fake := &downloadFaker{files: map[string]string{
		"hysteria-linux-amd64": "avx",
		"hashes.txt":           hysteriaHashes("amd64", "the-real-binary"),
	}}
	err := EnsureHysteria(context.Background(), fake, false)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err = %v, want a sha256 mismatch", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("install or disable ran despite the mismatch: %#v", fake.Calls)
	}
}

func TestEnsureHysteriaRefusesWhenHashEntryMissing(t *testing.T) {
	fake := &downloadFaker{files: map[string]string{
		"hysteria-linux-amd64": "body",
		"hashes.txt":           sha256Hex("body") + "  build/hysteria-linux-amd64-avx\n",
	}}
	err := EnsureHysteria(context.Background(), fake, false)
	if err == nil || !strings.Contains(err.Error(), "no sha256 entry") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("install ran despite the missing entry: %#v", fake.Calls)
	}
}

func TestEnsureLegoSkipsWhenAlreadyPresent(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureLego(context.Background(), fake, true); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("expected no install calls")
	}
}

func TestEnsureLegoVerifiesTheReleaseNamedAsset(t *testing.T) {
	const asset = "lego_v4.17.4_linux_amd64.tar.gz"
	const body = "fake-lego-tarball"
	fake := &downloadFaker{files: map[string]string{
		asset:           body,
		"asset_name":    asset,
		"checksums.txt": sha256Hex("other") + "  lego_v4.17.4_darwin_amd64.tar.gz\n" + sha256Hex(body) + "  " + asset + "\n",
	}}
	if err := EnsureLego(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("expected download + install, got %#v", fake.Calls)
	}
	download := strings.Join(fake.Calls[0], " ")
	for _, want := range []string{
		"https://api.github.com/repos/go-acme/lego/releases/latest",
		"linux_amd64.tar.gz",
		"checksums.txt",
	} {
		if !strings.Contains(download, want) {
			t.Errorf("download call missing %q: %s", want, download)
		}
	}
	if strings.Contains(download, "--ignore-missing") {
		t.Errorf("--ignore-missing makes verification meaningless: %s", download)
	}
	install := strings.Join(fake.Calls[1], " ")
	if !strings.Contains(install, "/usr/local/bin/lego") || !strings.Contains(install, asset) {
		t.Errorf("install call = %s", install)
	}
}

func TestEnsureLegoRefusesOnChecksumMismatch(t *testing.T) {
	const asset = "lego_v4.17.4_linux_amd64.tar.gz"
	fake := &downloadFaker{files: map[string]string{
		asset:           "tampered",
		"asset_name":    asset,
		"checksums.txt": sha256Hex("genuine") + "  " + asset + "\n",
	}}
	if err := EnsureLego(context.Background(), fake, false); err == nil {
		t.Fatal("installed a lego tarball whose checksum did not match")
	} else if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("install ran despite the mismatch: %#v", fake.Calls)
	}
}

// Ampere A1 (OCI free tier) and any other arm64 box must fetch the arm64
// assets; the names are derived from the CPU arch, never hard-coded.
func TestReleaseAssetsFollowTheCPUArch(t *testing.T) {
	defer func(prev string) { goarch = prev }(goarch)

	goarch = "arm64"
	const body = "fake-arm64-cloudflared"
	fake := &downloadFaker{files: map[string]string{
		"cloudflared-linux-arm64": body,
		"checksums.txt":           sha256Hex(body) + "  cloudflared-linux-arm64\n",
	}}
	if err := EnsureCloudflared(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	download := strings.Join(fake.Calls[0], " ")
	if !strings.Contains(download, "cloudflared-linux-arm64") || strings.Contains(download, "amd64") {
		t.Errorf("arm64 host must fetch the arm64 asset only: %s", download)
	}
	if install := strings.Join(fake.Calls[1], " "); !strings.Contains(install, "cloudflared-linux-arm64") {
		t.Errorf("install call = %s", install)
	}

	const legoAsset = "lego_v5.4.1_linux_arm64.tar.gz"
	legoFake := &downloadFaker{files: map[string]string{
		legoAsset:       "fake-lego",
		"asset_name":    legoAsset,
		"checksums.txt": sha256Hex("fake-lego") + "  " + legoAsset + "\n",
	}}
	if err := EnsureLego(context.Background(), legoFake, false); err != nil {
		t.Fatal(err)
	}
	if d := strings.Join(legoFake.Calls[0], " "); !strings.Contains(d, "linux_arm64.tar.gz") {
		t.Errorf("lego download must look for the arm64 tarball: %s", d)
	}

	xrayZip := zipOf(t, xrayArchive)
	xrayFake := &downloadFaker{files: map[string]string{
		"Xray-linux-arm64-v8a.zip":      xrayZip,
		"Xray-linux-arm64-v8a.zip.dgst": xrayDgst(xrayZip),
	}}
	if err := EnsureXray(context.Background(), xrayFake, false); err != nil {
		t.Fatal(err)
	}
	if d := strings.Join(xrayFake.Calls[0], " "); !strings.Contains(d, "Xray-linux-arm64-v8a.zip") || strings.Contains(d, "Xray-linux-64.zip") {
		t.Errorf("arm64 host must fetch the arm64-v8a Xray archive only: %s", d)
	}

	hyFake := &downloadFaker{files: map[string]string{
		"hysteria-linux-arm64": "fake-hy",
		"hashes.txt":           hysteriaHashes("arm64", "fake-hy"),
	}}
	if err := EnsureHysteria(context.Background(), hyFake, false); err != nil {
		t.Fatal(err)
	}
	if d := strings.Join(hyFake.Calls[0], " "); !strings.Contains(d, "hysteria-linux-arm64") || strings.Contains(d, "amd64") {
		t.Errorf("arm64 host must fetch the arm64 hysteria binary only: %s", d)
	}

	goarch = "riscv64"
	if err := EnsureXray(context.Background(), &downloadFaker{}, false); err == nil || !strings.Contains(err.Error(), "riscv64") {
		t.Fatalf("xray on an unsupported arch must fail before downloading, got %v", err)
	}
	if err := EnsureHysteria(context.Background(), &downloadFaker{}, false); err == nil || !strings.Contains(err.Error(), "riscv64") {
		t.Fatalf("hysteria on an unsupported arch must fail before downloading, got %v", err)
	}
	err := EnsureCloudflared(context.Background(), &downloadFaker{}, false)
	if err == nil || !strings.Contains(err.Error(), "riscv64") {
		t.Fatalf("an arch without release assets must fail clearly, got %v", err)
	}
	if err := EnsureLego(context.Background(), &downloadFaker{}, false); err == nil {
		t.Fatal("lego on an unsupported arch must fail before downloading")
	}
}
