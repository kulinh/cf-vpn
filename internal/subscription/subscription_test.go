package subscription

import (
	"strings"
	"testing"
)

func TestBuildVLESSRealityURI(t *testing.T) {
	got := BuildVLESSRealityURI("alice", "uuid-a", "node1.example.com",
		"www.microsoft.com", "pubkey-x25519", "d3cbbc0b4c5bc5f9")
	for _, want := range []string{
		"vless://uuid-a@node1.example.com:443",
		"security=reality",
		"flow=xtls-rprx-vision",
		"sni=www.microsoft.com",
		"pbk=pubkey-x25519",
		"sid=d3cbbc0b4c5bc5f9",
		"#alice-Reality",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestBuildVLESSHTTPUpgradeURI(t *testing.T) {
	got := BuildVLESSHTTPUpgradeURI("alice", "uuid-a", "vpn.example.com", "/api/v1/sync")
	for _, want := range []string{
		"vless://uuid-a@vpn.example.com:443",
		"security=tls",
		"type=httpupgrade",
		"path=%2Fapi%2Fv1%2Fsync",
		"sni=vpn.example.com",
		"#alice-HTTPUpgrade",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
