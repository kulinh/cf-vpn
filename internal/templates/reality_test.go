package templates

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func normJSON(t *testing.T, s string) string {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("unmarshal: %v\ninput: %s", err, s)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}

func TestRenderXrayDirectReality(t *testing.T) {
	in := XrayDirectRealityInputs{
		Users:       []XrayUser{{Name: "alice", UUID: "uuid-alice"}},
		PrivateKey:  "priv-x25519",
		ShortIDs:    []string{"d3cbbc0b4c5bc5f9"},
		Dest:        "www.microsoft.com:443",
		ServerNames: []string{"www.microsoft.com"},
	}
	out, err := RenderXrayDirectReality(in)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		`"flow": "xtls-rprx-vision"`,
		`"security": "reality"`,
		`"dest": "www.microsoft.com:443"`,
		`"privateKey": "priv-x25519"`,
		`"shortIds"`,
		"d3cbbc0b4c5bc5f9",
		"alice@vpn",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRenderXrayDirectRealityValidation(t *testing.T) {
	base := XrayDirectRealityInputs{
		Users:       []XrayUser{{Name: "alice", UUID: "uuid-alice"}},
		PrivateKey:  "priv-x25519",
		ShortIDs:    []string{"d3cbbc0b4c5bc5f9"},
		Dest:        "www.microsoft.com:443",
		ServerNames: []string{"www.microsoft.com"},
	}

	cases := []struct {
		name string
		mut  func(*XrayDirectRealityInputs)
	}{
		{"empty PrivateKey", func(in *XrayDirectRealityInputs) { in.PrivateKey = "" }},
		{"empty Dest", func(in *XrayDirectRealityInputs) { in.Dest = "" }},
		{"empty ServerNames", func(in *XrayDirectRealityInputs) { in.ServerNames = nil }},
		{"empty ShortIDs", func(in *XrayDirectRealityInputs) { in.ShortIDs = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mut(&in)
			if _, err := RenderXrayDirectReality(in); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestRenderXrayDirectRealityMatchesJPY02(t *testing.T) {
	golden, err := os.ReadFile("testdata/jpy02_reality_golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	in := XrayDirectRealityInputs{
		Users:       []XrayUser{{Name: "kulinh", UUID: "b3252cd7-d1c5-4f7f-b257-ef750ac838c9"}},
		PrivateKey:  "QPTgOMQeFazVzKeLaHafW89CpJN6mQoPZl9Lsi66I34",
		ShortIDs:    []string{"d3cbbc0b4c5bc5f9"},
		Dest:        "www.microsoft.com:443",
		ServerNames: []string{"www.microsoft.com"},
	}

	out, err := RenderXrayDirectReality(in)
	if err != nil {
		t.Fatal(err)
	}

	got := normJSON(t, out)
	want := normJSON(t, string(golden))

	if got != want {
		t.Fatalf("renderer output does not match golden file.\n\nGOT:\n%s\n\nWANT:\n%s", got, want)
	}
}

// h3Inbound digs the H3 inbound out of a rendered direct-mode config, or
// returns nil. Asserting on the parsed structure rather than on substrings is
// deliberate here: the two facts that matter (alpn is exactly ["h3"], and the
// H3 clients carry no flow) are both about shape, and strings.Contains cannot
// tell "alpn":["h3"] from "alpn":["h3","h2"].
func h3Inbound(t *testing.T, rendered string) map[string]any {
	t.Helper()
	var cfg struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(rendered), &cfg); err != nil {
		t.Fatalf("unmarshal rendered config: %v", err)
	}
	for _, in := range cfg.Inbounds {
		if in["tag"] == "vless-xhttp-h3" {
			return in
		}
	}
	return nil
}

func realityInputsWithH3() XrayDirectRealityInputs {
	return XrayDirectRealityInputs{
		Users:       []XrayUser{{Name: "kulinh", UUID: "uuid-kulinh"}},
		PrivateKey:  "priv-x25519",
		ShortIDs:    []string{"2441ae2d78da98bb"},
		Dest:        "www.sony.jp:443",
		ServerNames: []string{"www.sony.jp"},
		H3Host:      "quic-b55170f3.dongnat247.com",
		H3Path:      "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10",
		H3Cert:      "/etc/cfvpn/hysteria/cert.pem",
		H3Key:       "/etc/cfvpn/hysteria/key.pem",
	}
}

func TestRenderXrayDirectRealityH3InboundShape(t *testing.T) {
	out, err := RenderXrayDirectReality(realityInputsWithH3())
	if err != nil {
		t.Fatal(err)
	}
	in := h3Inbound(t, out)
	if in == nil {
		t.Fatalf("no vless-xhttp-h3 inbound in output:\n%s", out)
	}
	if got := in["port"]; got != float64(XHTTPH3Port) {
		t.Errorf("port = %v, want %d", got, XHTTPH3Port)
	}
	if got := in["listen"]; got != "0.0.0.0" {
		t.Errorf("listen = %v, want 0.0.0.0", got)
	}
	ss, _ := in["streamSettings"].(map[string]any)
	if got := ss["network"]; got != "xhttp" {
		t.Errorf("network = %v, want xhttp", got)
	}
	if got := ss["security"]; got != "tls" {
		t.Errorf("security = %v, want tls", got)
	}
	xs, _ := ss["xhttpSettings"].(map[string]any)
	if got := xs["mode"]; got != XHTTPH3Mode {
		t.Errorf("mode = %v, want %s", got, XHTTPH3Mode)
	}
	if got := xs["path"]; got != "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10" {
		t.Errorf("path = %v", got)
	}
	if got := xs["host"]; got != "quic-b55170f3.dongnat247.com" {
		t.Errorf("host = %v", got)
	}
}

// The single-element alpn is what makes xray listen on UDP with quic-go.
// Adding "h2" here (a natural-looking "improvement") silently moves the
// inbound back to TCP, where port 443 is already REALITY's.
func TestRenderXrayDirectRealityH3ALPNIsExactlyH3(t *testing.T) {
	out, err := RenderXrayDirectReality(realityInputsWithH3())
	if err != nil {
		t.Fatal(err)
	}
	ss, _ := h3Inbound(t, out)["streamSettings"].(map[string]any)
	tls, _ := ss["tlsSettings"].(map[string]any)
	alpn, _ := tls["alpn"].([]any)
	if len(alpn) != 1 || alpn[0] != "h3" {
		t.Fatalf("alpn = %v, want exactly [h3]", alpn)
	}
	certs, _ := tls["certificates"].([]any)
	if len(certs) != 1 {
		t.Fatalf("certificates = %v, want exactly one", certs)
	}
	c, _ := certs[0].(map[string]any)
	if c["certificateFile"] != "/etc/cfvpn/hysteria/cert.pem" || c["keyFile"] != "/etc/cfvpn/hysteria/key.pem" {
		t.Fatalf("certificate paths = %v", c)
	}
}

// xtls-rprx-vision only works over raw TCP. Reusing the REALITY client list
// here (the obvious implementation) would put flow on the H3 clients and xray
// rejects the connection.
func TestRenderXrayDirectRealityH3ClientsHaveNoFlow(t *testing.T) {
	out, err := RenderXrayDirectReality(realityInputsWithH3())
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := h3Inbound(t, out)["settings"].(map[string]any)
	clients, _ := settings["clients"].([]any)
	if len(clients) != 1 {
		t.Fatalf("clients = %v, want one", clients)
	}
	c, _ := clients[0].(map[string]any)
	if _, ok := c["flow"]; ok {
		t.Fatalf("H3 client must not carry flow, got %v", c)
	}
	if c["email"] != "kulinh@vpn" || c["id"] != "uuid-kulinh" {
		t.Fatalf("client = %v", c)
	}
}

// Every node in the fleet that has not opted in must render exactly what it
// renders today — the golden file above is the guard, this is the direct
// statement of the same rule.
func TestRenderXrayDirectRealityNoH3InboundWhenUnset(t *testing.T) {
	in := realityInputsWithH3()
	in.H3Host, in.H3Path, in.H3Cert, in.H3Key = "", "", "", ""
	out, err := RenderXrayDirectReality(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := h3Inbound(t, out); got != nil {
		t.Fatalf("H3 inbound rendered without H3Host: %v", got)
	}
}

// A half-configured H3 route is the dangerous case: it would render an inbound
// with an empty path or an empty certificate path, xray would refuse the
// config, and the caller's restart would fail on a node that was working a
// second ago. Reject it at render time instead.
func TestRenderXrayDirectRealityH3Validation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*XrayDirectRealityInputs)
	}{
		{"host without path", func(in *XrayDirectRealityInputs) { in.H3Path = "" }},
		{"path without host", func(in *XrayDirectRealityInputs) { in.H3Host = "" }},
		{"host without cert", func(in *XrayDirectRealityInputs) { in.H3Cert = "" }},
		{"host without key", func(in *XrayDirectRealityInputs) { in.H3Key = "" }},
		{"path missing leading slash", func(in *XrayDirectRealityInputs) { in.H3Path = "no-slash" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := realityInputsWithH3()
			tc.mut(&in)
			if _, err := RenderXrayDirectReality(in); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}
