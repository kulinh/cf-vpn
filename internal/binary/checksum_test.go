package binary

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A real lego checksums.txt: many lines, one per release asset.
const legoChecksums = `1111111111111111111111111111111111111111111111111111111111111111  lego_v4.17.4_darwin_amd64.tar.gz
2222222222222222222222222222222222222222222222222222222222222222  lego_v4.17.4_linux_arm64.tar.gz
3333333333333333333333333333333333333333333333333333333333333333  lego_v4.17.4_linux_amd64.tar.gz
`

func TestExpectedSHA256FindsTheRightAsset(t *testing.T) {
	got, err := ExpectedSHA256([]byte(legoChecksums), "/tmp/x/lego_v4.17.4_linux_amd64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got != "3333333333333333333333333333333333333333333333333333333333333333" {
		t.Fatalf("digest = %s", got)
	}
}

func TestExpectedSHA256HandlesBinaryModeMarker(t *testing.T) {
	body := []byte("4444444444444444444444444444444444444444444444444444444444444444 *cloudflared-linux-amd64\n")
	got, err := ExpectedSHA256(body, "cloudflared-linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "4444444444444444444444444444444444444444444444444444444444444444" {
		t.Fatalf("digest = %s", got)
	}
}

// cloudflared's <asset>.sha256 is sometimes just the digest.
func TestExpectedSHA256AcceptsBareDigest(t *testing.T) {
	got, err := ExpectedSHA256([]byte("  5555555555555555555555555555555555555555555555555555555555555555\n"), "cloudflared-linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "5555555555555555555555555555555555555555555555555555555555555555" {
		t.Fatalf("digest = %s", got)
	}
}

// The whole point of H4: an asset with no entry must be an error, never a pass.
func TestExpectedSHA256MissingEntryIsAnError(t *testing.T) {
	if _, err := ExpectedSHA256([]byte(legoChecksums), "cloudflared-linux-amd64"); err == nil {
		t.Fatal("expected an error for a file with no checksum entry")
	} else if !strings.Contains(err.Error(), "no sha256 entry") {
		t.Fatalf("err = %v", err)
	}
	if _, err := ExpectedSHA256(nil, "anything"); err == nil {
		t.Fatal("expected an error for an empty checksums file")
	}
}

func TestExpectedSHA256RejectsNonHexDigest(t *testing.T) {
	body := []byte("not-a-digest  cloudflared-linux-amd64\n")
	if _, err := ExpectedSHA256(body, "cloudflared-linux-amd64"); err == nil {
		t.Fatal("expected an error for a non-sha256 digest")
	}
}

func TestVerifyFileSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cloudflared-linux-amd64")
	const body = "binary bytes"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	good := []byte(hex.EncodeToString(sum[:]) + "  cloudflared-linux-amd64\n")
	if err := VerifyFileSHA256(path, good); err != nil {
		t.Fatalf("valid file rejected: %v", err)
	}

	bad := []byte("0000000000000000000000000000000000000000000000000000000000000000  cloudflared-linux-amd64\n")
	if err := VerifyFileSHA256(path, bad); err == nil {
		t.Fatal("tampered file accepted")
	} else if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err = %v", err)
	}

	if err := VerifyFileSHA256(filepath.Join(dir, "missing"), good); err == nil {
		t.Fatal("missing file accepted")
	}
}
