package templates

import (
	"strings"
	"testing"
)

func TestRenderCloudflaredAdmin(t *testing.T) {
	out, err := RenderCloudflaredAdmin(testTunnelUUID, "admin.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tunnel: "+testTunnelUUID) {
		t.Fatalf("missing tunnel line: %s", out)
	}
	if !strings.Contains(out, "hostname: admin.example.com") {
		t.Fatalf("missing admin hostname: %s", out)
	}
	if !strings.Contains(out, "http://127.0.0.1:6788") {
		t.Fatalf("expected agent at 127.0.0.1:6788: %s", out)
	}
	if strings.Contains(out, "/api/v1/sync") || strings.Contains(out, "/trojan") {
		t.Fatalf("data plane ingress must not appear: %s", out)
	}
}

func TestRenderHysteriaConfig(t *testing.T) {
	out, err := RenderHysteriaConfig(HysteriaInputs{
		Listen:   ":443",
		TLSCert:  "/cert.pem",
		TLSKey:   "/key.pem",
		ObfsPW:   "obfs-secret",
		UpMbps:   100,
		DownMbps: 200,
		Users:    []HysteriaUser{{Name: "alice", Password: "hy2-pw"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"listen: \":443\"", "cert: \"/cert.pem\"", "password: \"obfs-secret\"", "\"alice\": \"hy2-pw\""} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in output: %s", want, s)
		}
	}
}
