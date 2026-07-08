package templates

import (
	"strings"
	"testing"
)

func TestRenderXrayCloudflareHTTPUpgrade(t *testing.T) {
	out, err := RenderXrayCloudflareHTTPUpgrade(
		[]XrayUser{{Name: "alice", UUID: "uuid-a"}},
		"vpn.example.com",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"network": "httpupgrade"`,
		`"httpupgradeSettings"`,
		`"path": "/api/v1/sync"`,
		`"host": "vpn.example.com"`,
		`"listen": "127.0.0.1"`,
		`"port": 10001`,
		`"alice@vpn"`,
		`"uuid-a"`,
		// default international DoH resolvers when none supplied
		`"https://1.1.1.1/dns-query"`,
		`"https://9.9.9.9/dns-query"`,
		`"dns-out"`,
		`"port": 53`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderXrayCloudflareHTTPUpgradeCustomDNS(t *testing.T) {
	out, err := RenderXrayCloudflareHTTPUpgrade(
		[]XrayUser{{Name: "alice", UUID: "uuid-a"}},
		"vpn.example.com",
		[]string{"https://223.5.5.5/dns-query"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"https://223.5.5.5/dns-query"`) {
		t.Errorf("custom DNS server missing in:\n%s", out)
	}
	if strings.Contains(out, `"https://1.1.1.1/dns-query"`) {
		t.Errorf("default DoH must not appear when custom DNS supplied:\n%s", out)
	}
}

func TestRenderXrayCloudflareHTTPUpgradeMultipleUsers(t *testing.T) {
	users := []XrayUser{
		{Name: "alice", UUID: "uuid-a"},
		{Name: "bob", UUID: "uuid-b"},
	}
	out, err := RenderXrayCloudflareHTTPUpgrade(users, "vpn.example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alice@vpn") {
		t.Error("missing alice")
	}
	if !strings.Contains(out, "bob@vpn") {
		t.Error("missing bob")
	}
}
