package templates

import (
	"strings"
	"testing"
)

// testTunnelUUID is a syntactically valid Cloudflare tunnel id.
const testTunnelUUID = "2f8a1c3e-1111-4222-8333-abcdefabcdef"

func TestRenderCloudflaredWithAdminHappyPath(t *testing.T) {
	out, err := RenderCloudflaredWithAdmin(testTunnelUUID, "cdn-a1b2.rwl.one", "hkg-01.rwl247.dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"tunnel: " + testTunnelUUID,
		"credentials-file: /etc/cfvpn/cloudflared/" + testTunnelUUID + ".json",
		"hostname: cdn-a1b2.rwl.one",
		"path: ^/api/v1/sync",
		"service: http://127.0.0.1:10001",
		"hostname: hkg-01.rwl247.dev",
		"service: http://127.0.0.1:6788",
		"service: http_status:404",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// H8: the cloudflared config is the one text/template-rendered file, and its
// values were interpolated into unquoted YAML. A hostname carrying a newline
// injected an extra ingress rule; cloudflared honours the FIRST match, so the
// injected rule shadowed the real one and published a loopback port (SSH, or
// the agent itself) on a public Cloudflare hostname.
func TestRenderCloudflaredRejectsYAMLInjection(t *testing.T) {
	inject := "evil.example.com\n    service: http://127.0.0.1:22\n  - hostname: x.example.com"

	if out, err := RenderCloudflaredWithAdmin(testTunnelUUID, inject, "hkg-01.rwl247.dev"); err == nil {
		t.Fatalf("domain injection accepted, rendered:\n%s", out)
	}
	if out, err := RenderCloudflaredWithAdmin(testTunnelUUID, "cdn-a1b2.rwl.one", inject); err == nil {
		t.Fatalf("admin host injection accepted, rendered:\n%s", out)
	}
	if out, err := RenderCloudflaredAdmin(testTunnelUUID, inject); err == nil {
		t.Fatalf("admin-only host injection accepted, rendered:\n%s", out)
	}
}

// The tunnel uuid also lands in a filesystem path (credentials-file), so it is
// pinned to the UUID shape and cannot traverse.
func TestRenderCloudflaredRejectsBadTunnelUUID(t *testing.T) {
	for _, bad := range []string{
		"",
		"../../etc/shadow",
		"uuid-1",
		"2f8a1c3e-1111-4222-8333-abcdefabcdef\ningress: []",
	} {
		if _, err := RenderCloudflaredAdmin(bad, "hkg-01.rwl247.dev"); err == nil {
			t.Errorf("RenderCloudflaredAdmin accepted tunnel uuid %q", bad)
		}
		if _, err := RenderCloudflaredWithAdmin(bad, "cdn-a1b2.rwl.one", "hkg-01.rwl247.dev"); err == nil {
			t.Errorf("RenderCloudflaredWithAdmin accepted tunnel uuid %q", bad)
		}
	}
}

func TestRenderCloudflaredRejectsEmptyHost(t *testing.T) {
	if _, err := RenderCloudflaredWithAdmin(testTunnelUUID, "", "hkg-01.rwl247.dev"); err == nil {
		t.Error("empty domain accepted")
	}
	if _, err := RenderCloudflaredWithAdmin(testTunnelUUID, "cdn-a1b2.rwl.one", ""); err == nil {
		t.Error("empty admin host accepted")
	}
}
