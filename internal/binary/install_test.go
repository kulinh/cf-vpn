package binary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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
	err        error
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

func TestEnsureXrayInvokesInstallerWhenMissing(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureXray(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 || fake.Calls[0][0] != "bash" {
		t.Fatalf("expected bash install call, got %#v", fake.Calls)
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

func TestEnsureHysteriaInvokesInstallerThenDisablesDefaultUnit(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureHysteria(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("expected 2 calls (install + disable), got %d: %#v", len(fake.Calls), fake.Calls)
	}
	if fake.Calls[0][0] != "bash" || fake.Calls[0][1] != "-c" {
		t.Fatalf("expected bash -c install call, got %#v", fake.Calls[0])
	}
	if !strings.Contains(fake.Calls[0][2], "https://get.hy2.sh") {
		t.Fatalf("expected install command to fetch hy2 installer, got %q", fake.Calls[0][2])
	}
	if !strings.Contains(fake.Calls[1][2], "disable --now hysteria-server.service") {
		t.Fatalf("expected disable command, got %#v", fake.Calls[1])
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
