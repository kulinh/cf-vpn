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

// --- Golden strings ---------------------------------------------------------
//
// Every `want` below was produced by running the Worker's own builders
// (panel/worker/src/lib/subscription.ts, functions copied verbatim into node)
// on the same inputs. If one of these fails, the Go and Worker wire formats
// have drifted and a user provisioned from the node CLI gets a different URI
// than the same user provisioned from the panel.

func TestGoldenRealityURIMatchesWorker(t *testing.T) {
	got := BuildVLESSRealityURI(
		"alice",
		"2f8a1c3e-1111-4222-8333-abcdefabcdef",
		"cdn-a1b2.rwl.one",
		"www.apple.com",
		"XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl",
		"d3cbbc0b4c5bc5f9",
	)
	want := "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-a1b2.rwl.one:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=www.apple.com&pbk=XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl&sid=d3cbbc0b4c5bc5f9&fp=chrome#alice-Reality"
	if got != want {
		t.Fatalf("reality URI drifted from Worker\n got: %s\nwant: %s", got, want)
	}
}

// A raw base64 pubkey (+ / =) and an "@" in the tag must escape exactly like
// encodeURIComponent, not like url.QueryEscape or url.PathEscape.
func TestGoldenRealityURIEscapesLikeWorker(t *testing.T) {
	got := BuildVLESSRealityURI(
		"alice@hkg-01",
		"2f8a1c3e-1111-4222-8333-abcdefabcdef",
		"cdn-a1b2.rwl.one",
		"www.apple.com",
		"pbk+/=x",
		"sid",
	)
	want := "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-a1b2.rwl.one:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=www.apple.com&pbk=pbk%2B%2F%3Dx&sid=sid&fp=chrome#alice%40hkg-01-Reality"
	if got != want {
		t.Fatalf("reality URI drifted from Worker\n got: %s\nwant: %s", got, want)
	}
}

func TestGoldenHTTPUpgradeURIMatchesWorker(t *testing.T) {
	got := BuildVLESSHTTPUpgradeURI(
		"alice",
		"2f8a1c3e-1111-4222-8333-abcdefabcdef",
		"cdn-a1b2.rwl.one",
		"/api/v1/sync",
	)
	want := "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-a1b2.rwl.one:443?encryption=none&security=tls&type=httpupgrade&host=cdn-a1b2.rwl.one&path=%2Fapi%2Fv1%2Fsync&sni=cdn-a1b2.rwl.one#alice-HTTPUpgrade"
	if got != want {
		t.Fatalf("httpupgrade URI drifted from Worker\n got: %s\nwant: %s", got, want)
	}
}

// M-S1: a path with query-ish characters used to pass through unescaped and
// truncate the URI at the client. Each segment is encoded, then joined by %2F.
func TestGoldenHTTPUpgradeURIEscapesFullPath(t *testing.T) {
	got := BuildVLESSHTTPUpgradeURI(
		"alice@hkg-01",
		"2f8a1c3e-1111-4222-8333-abcdefabcdef",
		"cdn-a1b2.rwl.one",
		"/api/v1/sync?ed=2048",
	)
	want := "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-a1b2.rwl.one:443?encryption=none&security=tls&type=httpupgrade&host=cdn-a1b2.rwl.one&path=%2Fapi%2Fv1%2Fsync%3Fed%3D2048&sni=cdn-a1b2.rwl.one#alice%40hkg-01-HTTPUpgrade"
	if got != want {
		t.Fatalf("httpupgrade URI drifted from Worker\n got: %s\nwant: %s", got, want)
	}
}

func TestGoldenHy2URIMatchesWorker(t *testing.T) {
	got := BuildHy2URI("alice@hkg-01", "alice", "Zm9vYmFy_-abc", "hy2-c3d4.rwl.one", 24430, "kQ3x")
	want := "hysteria2://alice:Zm9vYmFy_-abc@hy2-c3d4.rwl.one:24430/?obfs=salamander&obfs-password=kQ3x&sni=hy2-c3d4.rwl.one&insecure=0#alice%40hkg-01-HY2"
	if got != want {
		t.Fatalf("hy2 URI drifted from Worker\n got: %s\nwant: %s", got, want)
	}
}

// The password may hold ":" "@" "/" "+" and spaces — escaping them wrong makes
// the client parse a different host or password.
func TestGoldenHy2URIEscapesLikeWorker(t *testing.T) {
	got := BuildHy2URI("alice", "alice", "p@ss w/rd:1+2", "hy2-c3d4.rwl.one", 24430, "obfs_PW-1~2*3'4(5)!6")
	want := "hysteria2://alice:p%40ss%20w%2Frd%3A1%2B2@hy2-c3d4.rwl.one:24430/?obfs=salamander&obfs-password=obfs_PW-1~2*3'4(5)!6&sni=hy2-c3d4.rwl.one&insecure=0#alice-HY2"
	if got != want {
		t.Fatalf("hy2 URI drifted from Worker\n got: %s\nwant: %s", got, want)
	}
}

func TestEncodeURIComponentMatchesJS(t *testing.T) {
	// Left side is the input character, right side what JS encodeURIComponent
	// returns for it (captured from node).
	cases := map[string]string{
		"ü": "%C3%BC",
		"€": "%E2%82%AC",
		" ": "%20",
		"/": "%2F",
		"?": "%3F",
		"&": "%26",
		"#": "%23",
		"=": "%3D",
		"+": "%2B",
		":": "%3A",
		"@": "%40",
		"$": "%24",
		",": "%2C",
		";": "%3B",
		"!": "!",
		"~": "~",
		"*": "*",
		"'": "'",
		"(": "(",
		")": ")",
		"-": "-",
		"_": "_",
		".": ".",
	}
	for in, want := range cases {
		if got := EncodeURIComponent(in); got != want {
			t.Errorf("EncodeURIComponent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildSubscriptionB64JoinsWithNewline(t *testing.T) {
	got := BuildSubscriptionB64("a", "b")
	// base64("a\nb")
	if got != "YQpi" {
		t.Fatalf("got %q, want %q", got, "YQpi")
	}
}
