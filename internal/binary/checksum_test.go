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

// cloudflared's <asset>.sha256 is sometimes just the digest — accepted only
// through the explicit opt-in entry point.
func TestExpectedSHA256BareDigestIsOptIn(t *testing.T) {
	bare := []byte("  5555555555555555555555555555555555555555555555555555555555555555\n")

	// The default parser must NOT accept it: a lone digest says nothing about
	// which file it belongs to, so a checksums file fetched for one asset could
	// otherwise authorise a different download.
	if _, err := ExpectedSHA256(bare, "cloudflared-linux-amd64"); err == nil {
		t.Fatal("ExpectedSHA256 accepted a nameless digest")
	}

	got, err := ExpectedSHA256BareDigest(bare, "cloudflared-linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "5555555555555555555555555555555555555555555555555555555555555555" {
		t.Fatalf("digest = %s", got)
	}

	// The opt-in form still matches by name when the file names its asset...
	named := []byte(legoChecksums)
	if got, err := ExpectedSHA256BareDigest(named, "lego_v4.17.4_linux_amd64.tar.gz"); err != nil {
		t.Fatal(err)
	} else if got != "3333333333333333333333333333333333333333333333333333333333333333" {
		t.Fatalf("digest = %s", got)
	}
	// ... and still refuses an asset the file does not mention.
	if _, err := ExpectedSHA256BareDigest(named, "cloudflared-linux-amd64"); err == nil {
		t.Fatal("opt-in form accepted an asset with no entry in a multi-asset file")
	}
	// A lone non-hex token is an error, not a silent fallthrough.
	if _, err := ExpectedSHA256BareDigest([]byte("not-a-digest\n"), "cloudflared-linux-amd64"); err == nil {
		t.Fatal("opt-in form accepted a non-hex token")
	}
}

// lego must never accept a nameless digest: its checksums.txt covers every
// release asset, so a bare digest there would be meaningless.
func TestVerifyFileSHA256RejectsBareDigestByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lego_v4.17.4_linux_amd64.tar.gz")
	const body = "tarball"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	bare := []byte(hex.EncodeToString(sum[:]) + "\n")

	if err := VerifyFileSHA256(path, bare); err == nil {
		t.Fatal("VerifyFileSHA256 accepted a nameless digest")
	}
	if err := VerifyFileSHA256BareDigestAllowed(path, bare); err != nil {
		t.Fatalf("opt-in verification rejected a matching bare digest: %v", err)
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

// The layout of Xray-core's Xray-linux-64.zip.dgst (v26.3.27).
const xrayRealDgst = `MD5= ee4e2ff74948a9b464624b1cabc44409
SHA1= b55b06e74e89083b9cedfdecf0d68b579cd2af72
SHA2-256= 23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae
SHA2-512= e8bc40a0687cac184bbe4b5c1f047e69064ccedc489fb25e208889ae287bbf8736dff16b108d68fc00dc33edc8bb53502e47a9698a277f4f51b67b83d899e518
`

func TestExpectedSHA256DgstPicksTheSHA256Line(t *testing.T) {
	got, err := ExpectedSHA256Dgst([]byte(xrayRealDgst), "/tmp/x/Xray-linux-64.zip")
	if err != nil {
		t.Fatal(err)
	}
	if got != "23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae" {
		t.Fatalf("digest = %s", got)
	}
}

func TestExpectedSHA256DgstRejectsMissingOrMalformed(t *testing.T) {
	for name, body := range map[string]string{
		"only weak digests": "MD5= ee4e2ff74948a9b464624b1cabc44409\nSHA1= b55b06e74e89083b9cedfdecf0d68b579cd2af72\n",
		"short digest":      "SHA2-256= 23cd9af9\n",
		"not hex":           "SHA2-256= " + strings.Repeat("z", 64) + "\n",
		"empty":             "",
		// A sha256sum-style line is not a .dgst and must not be read as one.
		"sha256sum format": "23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae  Xray-linux-64.zip\n",
	} {
		if _, err := ExpectedSHA256Dgst([]byte(body), "Xray-linux-64.zip"); err == nil {
			t.Errorf("%s: accepted %q", name, body)
		}
	}
}

func TestVerifyFileSHA256Dgst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Xray-linux-64.zip")
	const body = "zip-bytes"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	if err := VerifyFileSHA256Dgst(path, []byte("MD5= x\nSHA2-256= "+hex.EncodeToString(sum[:])+"\n")); err != nil {
		t.Fatalf("valid archive rejected: %v", err)
	}
	if err := VerifyFileSHA256Dgst(path, []byte(xrayRealDgst)); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("tampered archive: err = %v", err)
	}
}

// Hysteria's hashes.txt names builds as build/<asset>; the entry is matched on
// its base name, and the -avx build (whose name extends ours) must not match.
func TestExpectedSHA256HysteriaHashesLayout(t *testing.T) {
	hashes := []byte("1111111111111111111111111111111111111111111111111111111111111111  build/hysteria-linux-amd64-avx\n" +
		"2222222222222222222222222222222222222222222222222222222222222222  build/hysteria-linux-amd64\n" +
		"3333333333333333333333333333333333333333333333333333333333333333  build/hysteria-linux-arm64\n")
	for asset, want := range map[string]string{
		"hysteria-linux-amd64": "2222222222222222222222222222222222222222222222222222222222222222",
		"hysteria-linux-arm64": "3333333333333333333333333333333333333333333333333333333333333333",
	} {
		got, err := ExpectedSHA256(hashes, "/tmp/d/"+asset)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %s", asset, got, err, want)
		}
	}
}
