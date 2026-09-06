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
