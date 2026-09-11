package commands

import (
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/templates"
)

func TestXHTTPEnabledDefaultsOff(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want bool
	}{{"", false}, {"0", false}, {"1", true}, {"true", true}, {" on ", true}, {"no", false}} {
		if got := XHTTPEnabled(map[string]string{"XHTTP_ENABLED": tc.v}); got != tc.want {
			t.Fatalf("XHTTP_ENABLED=%q: got %v", tc.v, got)
		}
	}
}

func TestBuildUserURIsAddsXHTTPLineWhenEnabled(t *testing.T) {
	env := map[string]string{"MODE": "cloudflare", "NODE_ID": "OR-001", "HY2_ENABLED": "0", "XHTTP_ENABLED": "1"}
	lines := buildUserURIs("kulinh", "uuid", "static-df60bd79.duylinh.org", "", env, nil)
	if len(lines) != 2 {
		t.Fatalf("expected HTTPUpgrade + XHTTP, got %v", lines)
	}
	want := "vless://uuid@static-df60bd79.duylinh.org:443?encryption=none&security=tls&type=xhttp&host=static-df60bd79.duylinh.org&path=%2Fapi%2Fv2%2Fstream&mode=packet-up&sni=static-df60bd79.duylinh.org#kulinh%40OR-001-XHTTP"
	if lines[1] != want {
		t.Fatalf("xhttp line:\n got %s\nwant %s", lines[1], want)
	}
	env["XHTTP_ENABLED"] = "0"
	if lines := buildUserURIs("kulinh", "uuid", "d.example", "", env, nil); len(lines) != 1 {
		t.Fatalf("disabled must emit HTTPUpgrade only, got %v", lines)
	}
}

func TestXHTTPTemplatesAgree(t *testing.T) {
	// The xray inbound and the cloudflared ingress must point at the same path/port.
	out, err := templates.RenderXrayCloudflare([]templates.XrayUser{{Name: "a", UUID: "u"}}, "vpn.example.com", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"tag": "vless-xhttp"`, `"port": 10002`, `"network": "xhttp"`, `"path": "/api/v2/stream"`, `"mode": "packet-up"`, `"tag": "vless-httpupgrade"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("xray config missing %s:\n%s", want, out)
		}
	}
	cf, err := templates.RenderCloudflaredWithAdminOpts("2f8a1c3e-1111-4222-8333-abcdefabcdef", "vpn.example.com", "admin.example.com", templates.CloudflaredOptions{XHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cf, "path: ^/api/v2/stream\n    service: http://127.0.0.1:10002\n  - hostname: vpn.example.com\n    path: ^/api/v1/sync") {
		t.Fatalf("xhttp ingress must precede the httpupgrade rule:\n%s", cf)
	}
	off, _ := templates.RenderXrayCloudflare([]templates.XrayUser{{Name: "a", UUID: "u"}}, "vpn.example.com", nil, false)
	if strings.Contains(off, "xhttp") {
		t.Fatal("xhttp inbound must be absent when disabled")
	}
	cfOff, _ := templates.RenderCloudflaredWithAdmin("2f8a1c3e-1111-4222-8333-abcdefabcdef", "vpn.example.com", "admin.example.com", "")
	if strings.Contains(cfOff, "v2/stream") {
		t.Fatal("xhttp ingress must be absent when disabled")
	}
}
