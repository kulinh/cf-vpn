package commands

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/state"
)

// jpy03V6Env is the JPY-03 fixture shared with panel/worker/src/lib/ipv6.test.ts
// ("matches the Go builder byte for byte"); both sides pin goldenV6Lines.
func jpy03V6Env() map[string]string {
	return map[string]string{
		state.KeyMode: "direct", state.KeyNodeID: "JPY-03",
		state.KeyPublicIP: "129.225.185.197", state.KeyPublicIPv6: "2603:c023:19:9800:0:f882:7490:be7a",
		state.KeyRealityPub: "XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl", state.KeyRealityShortID: "2441ae2d78da98bb", state.KeyRealitySNI: "www.sony.jp",
		state.KeyXHTTPH3Host: "quic-b55170f3.dongnat247.com", state.KeyXHTTPH3Path: "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10",
		state.KeyHy2Host: "quic-b55170f3.dongnat247.com", state.KeyHy2Port: "32443", state.KeyHy2ObfsPW: "kQ3x", "HY2_ENABLED": "1",
	}
}

// Order is part of the contract with buildSubscriptionURIs in the Worker:
// Reality, Reality-v6, H3, HY2, HY2-v6.
var goldenV6Lines = []string{
	"vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@129.225.185.197:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=www.sony.jp&pbk=XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl&sid=2441ae2d78da98bb&fp=chrome#JPY-03-Reality",
	"vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@[2603:c023:19:9800:0:f882:7490:be7a]:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=www.sony.jp&pbk=XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl&sid=2441ae2d78da98bb&fp=chrome#JPY-03-Reality-v6",
	"vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@quic-b55170f3.dongnat247.com:443?encryption=none&security=tls&type=xhttp&host=quic-b55170f3.dongnat247.com&path=%2F3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10&mode=stream-one&alpn=h3&sni=quic-b55170f3.dongnat247.com#JPY-03-XHTTP-H3",
	"hysteria2://kulinh:Zm9vYmFy_-abc@129.225.185.197:32443/?obfs=salamander&obfs-password=kQ3x&sni=quic-b55170f3.dongnat247.com&insecure=0#JPY-03-HY2",
	"hysteria2://kulinh:Zm9vYmFy_-abc@[2603:c023:19:9800:0:f882:7490:be7a]:32443/?obfs=salamander&obfs-password=kQ3x&sni=quic-b55170f3.dongnat247.com&insecure=0#JPY-03-HY2-v6",
}

func TestBuildUserURIsIPv6TwinsMatchWorker(t *testing.T) {
	got := buildUserURIs("kulinh", "2f8a1c3e-1111-4222-8333-abcdefabcdef", "edge-64b43148.dongnat247.com", "Zm9vYmFy_-abc", jpy03V6Env(), nil)
	if !reflect.DeepEqual(got, goldenV6Lines) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(goldenV6Lines, "\n"))
	}
}

func TestBuildUserURIsNoTwinsWithoutIPv6(t *testing.T) {
	for _, v := range []string{"", "  ", "129.225.185.197"} {
		env := jpy03V6Env()
		env[state.KeyPublicIPv6] = v
		got := buildUserURIs("kulinh", "u", "d.example", "pw", env, nil)
		if len(got) != 3 {
			t.Fatalf("PUBLIC_IPV6=%q: want 3 lines (no v6 twins), got %v", v, got)
		}
	}
}

func TestBuildUserURIsCloudflareNodeGetsOnlyHy2Twin(t *testing.T) {
	env := map[string]string{
		"MODE": "cloudflare", "NODE_ID": "JPY-01", "PUBLIC_IP": "45.143.131.36", "PUBLIC_IPV6": "2a12:a304:4:8f3::a",
		"HY2_HOST": "h.example", "HY2_PORT": "5331", "HY2_OBFS_PW": "o", "HY2_ENABLED": "1",
	}
	got := buildUserURIs("kulinh", "uuid", "d.example", "pw", env, nil)
	if len(got) != 3 || !strings.HasSuffix(got[1], "#JPY-01-HY2") ||
		got[2] != "hysteria2://kulinh:pw@[2a12:a304:4:8f3::a]:5331/?obfs=salamander&obfs-password=o&sni=h.example&insecure=0#JPY-01-HY2-v6" {
		t.Fatalf("got %v", got)
	}
}

func TestBuildUserURIsNoHy2TwinWithoutUserPassword(t *testing.T) {
	got := buildUserURIs("kulinh", "u", "d.example", "", jpy03V6Env(), nil)
	for _, l := range got {
		if strings.Contains(l, "HY2") {
			t.Fatalf("HY2 line without a password: %v", got)
		}
	}
}

// publicIPv6 gates both twins; only a real IPv6 literal may pass, because the
// value is bracketed into the URI verbatim.
func TestPublicIPv6AcceptsOnlyPlainIPv6Literals(t *testing.T) {
	for in, want := range map[string]string{
		"2603:c023:19:9800:0:f882:7490:be7a": "2603:c023:19:9800:0:f882:7490:be7a",
		"2a12:a304:4:8f3::a":                 "2a12:a304:4:8f3::a",
		"::1":                                "::1",
		"  2001:db8::1\n":                    "2001:db8::1",
		"":                                   "",
		"129.225.185.197":                    "",
		"fe80::1%eth0":                       "",
		"[2001:db8::1]":                      "",
		"a:b":                                "",
		"::ffff:1.2.3.4":                     "",
		"2001:db8::1/64":                     "",
	} {
		if got := publicIPv6(map[string]string{state.KeyPublicIPv6: in}); got != want {
			t.Errorf("publicIPv6(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildUserURIsNoTwinsForMalformedIPv6(t *testing.T) {
	for _, v := range []string{"fe80::1%eth0", "[2001:db8::1]", "a:b", "::ffff:1.2.3.4"} {
		env := jpy03V6Env()
		env[state.KeyPublicIPv6] = v
		got := buildUserURIs("kulinh", "u", "d.example", "pw", env, nil)
		if len(got) != 3 {
			t.Fatalf("PUBLIC_IPV6=%q: want 3 lines (no v6 twins), got %v", v, got)
		}
	}
}

func TestBuildUserURIsUpperCasesNodeID(t *testing.T) {
	env := jpy03V6Env()
	env[state.KeyNodeID] = "jpy-03"
	got := buildUserURIs("kulinh", "2f8a1c3e-1111-4222-8333-abcdefabcdef", "edge-64b43148.dongnat247.com", "Zm9vYmFy_-abc", env, nil)
	if !reflect.DeepEqual(got, goldenV6Lines) {
		t.Fatalf("lowercase NODE_ID must still name routes like the panel (JPY-03-…):\n%s", strings.Join(got, "\n"))
	}
}
