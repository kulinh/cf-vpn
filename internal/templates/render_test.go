package templates

import (
	"encoding/json"
	"strings"
	"testing"
)

func inboundByTag(t *testing.T, inbounds []any, tag string) map[string]any {
	t.Helper()
	for _, inbound := range inbounds {
		m := inbound.(map[string]any)
		if m["tag"] == tag {
			return m
		}
	}
	t.Fatalf("missing inbound %s in %v", tag, inbounds)
	return nil
}

func TestRenderCloudflaredAdmin(t *testing.T) {
	out, err := RenderCloudflaredAdmin("uuid-1", "admin.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tunnel: uuid-1") {
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

func TestRenderXrayDirectIncludesTLSAndCerts(t *testing.T) {
	in := XrayDirectInputs{
		Users: []XrayUser{{Name: "alice", UUID: "u-1"}},
		Certs: []XrayCert{{Zone: "example.com", CertFile: "/etc/cfvpn/certs/example.com/fullchain.pem", KeyFile: "/etc/cfvpn/certs/example.com/privkey.pem"}},
	}
	out, err := RenderXrayDirect(in)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	inbounds, ok := cfg["inbounds"].([]any)
	if !ok || len(inbounds) != 1 {
		t.Fatalf("expected 1 public VLESS inbound: %s", out)
	}
	if inbounds[0].(map[string]any)["tag"] != "vless-ws" {
		t.Fatalf("vless-ws must remain first for user management: %v", inbounds[0])
	}

	vless := inboundByTag(t, inbounds, "vless-ws")
	if vless["listen"] != "0.0.0.0" || vless["port"].(float64) != 443 {
		t.Fatalf("bad vless inbound address: %v", vless)
	}
	vlessStream := vless["streamSettings"].(map[string]any)
	if vlessStream["network"] != "ws" || vlessStream["security"] != "tls" || vlessStream["wsSettings"].(map[string]any)["path"] != "/api/v1/sync" {
		t.Fatalf("bad vless stream settings: %v", vlessStream)
	}
	vlessSettings := vless["settings"].(map[string]any)
	if vlessSettings["decryption"] != "none" {
		t.Fatalf("expected vless decryption=none, got %v", vlessSettings["decryption"])
	}

	for _, inbound := range inbounds {
		if inbound.(map[string]any)["tag"] == "trojan-ws" {
			t.Fatalf("trojan-ws inbound must not be rendered: %v", inbound)
		}
	}

	tls := vlessStream["tlsSettings"].(map[string]any)
	certs := tls["certificates"].([]any)
	if len(certs) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(certs))
	}
	cert0 := certs[0].(map[string]any)
	if cert0["certificateFile"] != "/etc/cfvpn/certs/example.com/fullchain.pem" {
		t.Fatalf("wrong cert path: %v", cert0)
	}
	if cert0["keyFile"] != "/etc/cfvpn/certs/example.com/privkey.pem" {
		t.Fatalf("wrong key path: %v", cert0)
	}
	if _, ok := vlessSettings["fallbacks"]; ok {
		t.Fatalf("direct VLESS inbound must not render fallbacks: %v", vlessSettings)
	}
	if strings.Contains(out, "127.0.0.1") || strings.Contains(out, "10001") || strings.Contains(out, "tls-fallback") {
		t.Fatalf("direct mode must not render localhost fallback shim: %s", out)
	}
}

func TestRenderXrayDirectMultipleZones(t *testing.T) {
	in := XrayDirectInputs{
		Users: []XrayUser{{Name: "alice", UUID: "u-1"}},
		Certs: []XrayCert{{Zone: "a.com", CertFile: "/c/a/fc.pem", KeyFile: "/c/a/k.pem"}, {Zone: "b.com", CertFile: "/c/b/fc.pem", KeyFile: "/c/b/k.pem"}},
	}
	out, err := RenderXrayDirect(in)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	json.Unmarshal([]byte(out), &cfg)
	inbounds := cfg["inbounds"].([]any)
	stream := inboundByTag(t, inbounds, "vless-ws")["streamSettings"].(map[string]any)
	tls := stream["tlsSettings"].(map[string]any)
	certs := tls["certificates"].([]any)
	if len(certs) != 2 {
		t.Fatalf("expected 2 certs, got %d", len(certs))
	}
}

func TestRenderXrayDirectIncludesAllUsers(t *testing.T) {
	in := XrayDirectInputs{
		Users: []XrayUser{{Name: "alice", UUID: "u-a"}, {Name: "bob", UUID: "u-b"}},
		Certs: []XrayCert{{Zone: "a.com", CertFile: "/c/fc.pem", KeyFile: "/c/k.pem"}},
	}
	out, err := RenderXrayDirect(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"u-a", "u-b", "alice@vpn", "bob@vpn"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output", want)
		}
	}
	for _, notWant := range []string{"p-a", "p-b", "trojan-ws", "/trojan"} {
		if strings.Contains(out, notWant) {
			t.Fatalf("unexpected %q in output: %s", notWant, out)
		}
	}
}

func TestRenderXrayDirectZeroUsersRendersEmptyVLESSClientsArray(t *testing.T) {
	out, err := RenderXrayDirect(XrayDirectInputs{
		Certs: []XrayCert{{Zone: "a.com", CertFile: "/c/fc.pem", KeyFile: "/c/k.pem"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	inbounds := cfg["inbounds"].([]any)
	vless := inboundByTag(t, inbounds, "vless-ws")
	settings := vless["settings"].(map[string]any)
	clients, ok := settings["clients"].([]any)
	if !ok {
		t.Fatalf("expected clients to be an array, got %T (%v)", settings["clients"], settings["clients"])
	}
	if len(clients) != 0 {
		t.Fatalf("expected empty clients array, got %v", clients)
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
