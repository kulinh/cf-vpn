package binary

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExpectedSHA256 finds the digest for filename in a sha256sum-format checksums
// file. Lines look like:
//
//	<64 hex>  cloudflared-linux-amd64
//	<64 hex> *lego_v4.17.4_linux_amd64.tar.gz     (binary-mode marker)
//
// Only the base name is compared, because release files list bare names while
// callers hold a full path.
//
// H4: the shell used `sha256sum -c --ignore-missing`, which reports success
// when NOTHING matched — and nothing ever did, since cloudflared was downloaded
// as "cloudflared" while the checksums file names "cloudflared-linux-amd64".
// Under `set -euo pipefail` that aborted EnsureCloudflared outright, so a fresh
// node could never install cloudflared; had the names ever lined up,
// --ignore-missing would have made the verification meaningless anyway.
func ExpectedSHA256(checksums []byte, filename string) (string, error) {
	want := filepath.Base(filename)

	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		digest := strings.ToLower(fields[0])
		name := strings.TrimPrefix(strings.Join(fields[1:], " "), "*")
		if filepath.Base(name) != want {
			continue
		}
		if len(digest) != 64 {
			return "", fmt.Errorf("checksum for %s is not a sha256 digest: %q", want, fields[0])
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return "", fmt.Errorf("checksum for %s is not hex: %q", want, fields[0])
		}
		return digest, nil
	}
	return "", fmt.Errorf("no sha256 entry for %s in the checksums file", want)
}

// ExpectedSHA256BareDigest reads a checksum file that holds nothing but the
// digest, the form cloudflared publishes as <asset>.sha256. A file naming its
// asset is still accepted and matched by name.
//
// This is deliberately a SEPARATE entry point rather than a fallback inside
// ExpectedSHA256: accepting "a lone 64-hex token belongs to whatever file you
// happen to be verifying" for every caller would let a checksums file fetched
// for one asset silently authorise a different one. Only the caller that knows
// its checksum URL is per-asset may opt in.
func ExpectedSHA256BareDigest(checksums []byte, filename string) (string, error) {
	if fields := strings.Fields(string(checksums)); len(fields) == 1 && len(fields[0]) == 64 {
		digest := strings.ToLower(fields[0])
		if _, err := hex.DecodeString(digest); err == nil {
			return digest, nil
		}
		return "", fmt.Errorf("checksum for %s is not hex: %q", filepath.Base(filename), fields[0])
	}
	return ExpectedSHA256(checksums, filename)
}

// ExpectedSHA256Dgst reads the SHA2-256 line of an Xray-core <asset>.dgst file,
// which lists one digest per algorithm and no file name:
//
//	MD5= ee4e2ff74948a9b464624b1cabc44409
//	SHA1= b55b06e74e89083b9cedfdecf0d68b579cd2af72
//	SHA2-256= 23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae
//	SHA2-512= e8bc40a0...
//
// Like the bare-digest form, it can only be trusted by a caller that fetched
// the .dgst for the very asset it is verifying. The SHA-512 line is skipped
// deliberately: one strong digest is enough, and it keeps a single hash path.
func ExpectedSHA256Dgst(dgst []byte, filename string) (string, error) {
	want := filepath.Base(filename)
	for _, line := range strings.Split(string(dgst), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(key) != "SHA2-256" {
			continue
		}
		digest := strings.ToLower(strings.TrimSpace(value))
		if len(digest) != 64 {
			return "", fmt.Errorf("SHA2-256 for %s is not a sha256 digest: %q", want, value)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return "", fmt.Errorf("SHA2-256 for %s is not hex: %q", want, value)
		}
		return digest, nil
	}
	return "", fmt.Errorf("no SHA2-256 entry for %s in the .dgst file", want)
}

// VerifyFileSHA256Dgst is VerifyFileSHA256 for an Xray-core .dgst file.
func VerifyFileSHA256Dgst(path string, dgst []byte) error {
	return verifyFileSHA256(path, dgst, ExpectedSHA256Dgst)
}

// VerifyFileSHA256 hashes path and compares it with the entry for that file in
// the checksums file. A missing entry is an error — the whole point is that the
// download is never installed unverified.
func VerifyFileSHA256(path string, checksums []byte) error {
	return verifyFileSHA256(path, checksums, ExpectedSHA256)
}

// VerifyFileSHA256BareDigestAllowed is VerifyFileSHA256 for a per-asset
// checksum file that may contain only the digest (cloudflared's .sha256).
func VerifyFileSHA256BareDigestAllowed(path string, checksums []byte) error {
	return verifyFileSHA256(path, checksums, ExpectedSHA256BareDigest)
}

func verifyFileSHA256(path string, checksums []byte, expected func([]byte, string) (string, error)) error {
	want, err := expected(checksums, path)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s for verification: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("read %s for verification: %w", path, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("sha256 mismatch for %s: got %s, expected %s", filepath.Base(path), got, want)
	}
	return nil
}
