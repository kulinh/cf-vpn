package templates

import (
	"strings"
	"testing"
)

func TestRenderCloudflaredIncludesTunnelAndDomain(t *testing.T) {
	s, err := RenderCloudflared("t-123", "vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "tunnel: t-123") || !strings.Contains(s, "hostname: vpn.example.com") {
		t.Fatalf("unexpected render output: %s", s)
	}
}

func TestRenderXrayIncludesUserAndUUID(t *testing.T) {
	s, err := RenderXray("alice", "uuid-a", "pass-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id": "uuid-a"`, `alice@vpn`, `"password": "pass-a"`, `geoip:private`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in render output", want)
		}
	}
}
