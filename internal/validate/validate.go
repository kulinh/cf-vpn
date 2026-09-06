// Package validate holds the identifier checks applied at every trust boundary
// where an attacker-controlled string would otherwise reach a config file, a
// filesystem path, or a Cloudflare API URL.
//
// Nothing in the Go tree used to validate hostnames: the panel hands the agent
// a "new_host" string, which lands in the env file, in cloudflared's YAML
// ingress, and in a cert request. One newline in it rewrites the env file's
// AGENT_SHARED_SECRET; one newline in the YAML publishes 127.0.0.1:22 through
// a Cloudflare tunnel.
package validate

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// hostLabelRE is one RFC 1123 DNS label: alphanumeric, inner hyphens.
	hostLabelRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	// uuidRE is a canonical 8-4-4-4-12 hex UUID (Cloudflare tunnel ids).
	uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// hexID32RE is a Cloudflare zone / DNS-record id.
	hexID32RE = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// Hostname checks that s is a plain DNS hostname (RFC 1123): 1..253 bytes, dot
// separated labels of at most 63 bytes, no scheme, port, path, whitespace, or
// control character. A trailing dot is rejected so the value is canonical
// wherever it is compared or concatenated.
func Hostname(s string) error {
	if s == "" {
		return fmt.Errorf("hostname is empty")
	}
	if len(s) > 253 {
		return fmt.Errorf("hostname %q is longer than 253 bytes", s)
	}
	if s != strings.TrimSpace(s) {
		return fmt.Errorf("hostname %q has leading or trailing whitespace", s)
	}
	for _, label := range strings.Split(s, ".") {
		if !hostLabelRE.MatchString(label) {
			return fmt.Errorf("hostname %q has invalid label %q", s, label)
		}
	}
	return nil
}

// UUID checks that s is a canonical hex UUID.
func UUID(s string) error {
	if !uuidRE.MatchString(s) {
		return fmt.Errorf("%q is not a UUID", s)
	}
	return nil
}

// HexID32 checks that s is a 32-character lowercase hex identifier, the shape
// of Cloudflare zone ids and DNS record ids.
func HexID32(s string) error {
	if !hexID32RE.MatchString(s) {
		return fmt.Errorf("%q is not a 32-character hex id", s)
	}
	return nil
}
