package commands

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/xray"
)

func directEnv() map[string]string {
	return map[string]string{
		state.KeyMode:           "direct",
		state.KeyDomain:         "cdn-a1b2.rwl.one",
		state.KeyRealityPub:     "XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl",
		state.KeyRealityShortID: "d3cbbc0b4c5bc5f9",
		state.KeyRealitySNI:     "www.apple.com",
		state.KeyRealityPriv:    "priv",
		state.KeyHy2Host:        "hy2-c3d4.rwl.one",
		state.KeyHy2Port:        "24430",
		state.KeyHy2ObfsPW:      "kQ3x",
	}
}

// The exact strings the Worker (panel/worker/src/lib/subscription.ts) produces
// for these inputs.
const (
	wantRealityURI = "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-a1b2.rwl.one:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=www.apple.com&pbk=XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl&sid=d3cbbc0b4c5bc5f9&fp=chrome#alice-Reality"
	wantHy2URI     = "hysteria2://alice:Zm9vYmFy_-abc@hy2-c3d4.rwl.one:24430/?obfs=salamander&obfs-password=kQ3x&sni=hy2-c3d4.rwl.one&insecure=0#alice-HY2"
	wantHTTPUpURI  = "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-a1b2.rwl.one:443?encryption=none&security=tls&type=httpupgrade&host=cdn-a1b2.rwl.one&path=%2Fapi%2Fv1%2Fsync&sni=cdn-a1b2.rwl.one#alice-HTTPUpgrade"
)

const testUUID = "2f8a1c3e-1111-4222-8333-abcdefabcdef"

func TestBuildUserURIsDirectRealityMatchesWorker(t *testing.T) {
	var warn bytes.Buffer
	got := buildUserURIs("alice", testUUID, "cdn-a1b2.rwl.one", "Zm9vYmFy_-abc", directEnv(), &warn)
	want := []string{wantRealityURI, wantHy2URI}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d drifted from the Worker\n got: %s\nwant: %s", i, got[i], want[i])
		}
	}
	if warn.Len() != 0 {
		t.Errorf("unexpected warning: %s", warn.String())
	}
}

// H10: a direct node with any Reality field missing must emit NOTHING for
// VLESS. The old code fell through to an HTTPUpgrade URI, which points a TLS
// handshake at a REALITY listener whose dest is www.apple.com — the client gets
// Apple's certificate for the VPN hostname and fails.
func TestBuildUserURIsDirectWithoutFullRealityEmitsNoVLESS(t *testing.T) {
	for _, missing := range []string{state.KeyRealityPub, state.KeyRealityShortID, state.KeyRealitySNI} {
		env := directEnv()
		env[missing] = ""
		var warn bytes.Buffer
		got := buildUserURIs("alice", testUUID, "cdn-a1b2.rwl.one", "Zm9vYmFy_-abc", env, &warn)
		for _, uri := range got {
			if strings.HasPrefix(uri, "vless://") {
				t.Errorf("missing %s: emitted a VLESS URI anyway: %s", missing, uri)
			}
		}
		if len(got) != 1 || got[0] != wantHy2URI {
			t.Errorf("missing %s: expected only the HY2 line, got %#v", missing, got)
		}
		if !strings.Contains(warn.String(), "Reality params are incomplete") {
			t.Errorf("missing %s: expected a warning, got %q", missing, warn.String())
		}
	}
}

func TestBuildUserURIsCloudflareMode(t *testing.T) {
	env := map[string]string{
		state.KeyMode:      "cloudflare",
		state.KeyHy2Host:   "hy2-c3d4.rwl.one",
		state.KeyHy2Port:   "24430",
		state.KeyHy2ObfsPW: "kQ3x",
	}
	var warn bytes.Buffer
	got := buildUserURIs("alice", testUUID, "cdn-a1b2.rwl.one", "Zm9vYmFy_-abc", env, &warn)
	want := []string{wantHTTPUpURI, wantHy2URI}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("line %d\n got: %v\nwant: %s", i, got, want[i])
		}
	}
}

// The transport path is taken from XHTTP_PATH when the node sets it, like the
// Worker's `r.xhttp_path ?? "/api/v1/sync"`.
// The Worker's branch is three-way: direct ⇒ Reality or nothing, cloudflare ⇒
// HTTPUpgrade, anything else ⇒ nothing. An empty or unrecognised MODE used to
// fall through to HTTPUpgrade here, describing a transport the node does not
// serve.
func TestBuildUserURIsUnknownModeEmitsNoVLESS(t *testing.T) {
	for _, mode := range []string{"", "  ", "legacy", "ws", "DIRECT"} {
		env := map[string]string{
			state.KeyMode:      mode,
			state.KeyHy2Host:   "hy2-c3d4.rwl.one",
			state.KeyHy2Port:   "24430",
			state.KeyHy2ObfsPW: "kQ3x",
		}
		var warn bytes.Buffer
		got := buildUserURIs("alice", testUUID, "cdn-a1b2.rwl.one", "Zm9vYmFy_-abc", env, &warn)
		for _, uri := range got {
			if strings.HasPrefix(uri, "vless://") {
				t.Errorf("MODE=%q: emitted a VLESS URI anyway: %s", mode, uri)
			}
		}
		// HY2 is independent of the VLESS transport and must still be offered.
		if len(got) != 1 || got[0] != wantHy2URI {
			t.Errorf("MODE=%q: expected only the HY2 line, got %#v", mode, got)
		}
		if !strings.Contains(warn.String(), "is not \"direct\" or \"cloudflare\"") {
			t.Errorf("MODE=%q: expected a warning, got %q", mode, warn.String())
		}
	}
}

func TestBuildUserURIsUsesXHTTPPathOverride(t *testing.T) {
	env := map[string]string{state.KeyMode: "cloudflare", state.KeyXHTTPPath: "/cdn-cgi/trace"}
	got := buildUserURIs("alice", testUUID, "cdn-a1b2.rwl.one", "", env, nil)
	if len(got) != 1 || !strings.Contains(got[0], "path=%2Fcdn-cgi%2Ftrace") {
		t.Fatalf("got %#v", got)
	}
}

func TestBuildUserURIsNoHy2WhenNodeIncomplete(t *testing.T) {
	cases := map[string]map[string]string{
		"no host":    {state.KeyMode: "cloudflare"},
		"no port":    {state.KeyMode: "cloudflare", state.KeyHy2Host: "h.example.com", state.KeyHy2ObfsPW: "o"},
		"no obfs pw": {state.KeyMode: "cloudflare", state.KeyHy2Host: "h.example.com", state.KeyHy2Port: "24430"},
		"bad port":   {state.KeyMode: "cloudflare", state.KeyHy2Host: "h.example.com", state.KeyHy2Port: "nope", state.KeyHy2ObfsPW: "o"},
	}
	for name, env := range cases {
		got := buildUserURIs("alice", testUUID, "cdn-a1b2.rwl.one", "pw", env, nil)
		for _, uri := range got {
			if strings.HasPrefix(uri, "hysteria2://") {
				t.Errorf("%s: emitted an HY2 URI anyway: %s", name, uri)
			}
		}
	}
}

func TestBuildUserURIsNoHy2WithoutUserPassword(t *testing.T) {
	var warn bytes.Buffer
	got := buildUserURIs("alice", testUUID, "cdn-a1b2.rwl.one", "", directEnv(), &warn)
	if len(got) != 1 || got[0] != wantRealityURI {
		t.Fatalf("got %#v", got)
	}
	if !strings.Contains(warn.String(), "no password in the hysteria config") {
		t.Errorf("expected a warning, got %q", warn.String())
	}
}

// M-G5 + HY2 in gen-sub: `cfvpnctl add-user` must provision the user in
// hysteria too and print an HY2 line, otherwise the agent's next sync mints a
// Hy2 password that does not match D1.
func TestRunAddUserProvisionsHysteriaAndEmitsHy2Line(t *testing.T) {
	cfgPath, subDir := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestEnv(t, directEnv())

	r := &stubRunner{}
	var out, errBuf bytes.Buffer
	if err := RunAddUser(context.Background(), UserInputs{Name: "bob", Domain: "cdn-a1b2.rwl.one"}, r, &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	hy2, err := hy2PasswordsByName()
	if err != nil {
		t.Fatal(err)
	}
	if hy2["bob"] == "" {
		t.Fatalf("bob was not added to the hysteria config: %#v", hy2)
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("subscription is not base64: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected VLESS + HY2 lines, got:\n%s", raw)
	}
	if !strings.HasPrefix(lines[0], "vless://") || !strings.Contains(lines[0], "security=reality") {
		t.Errorf("line 0 = %s", lines[0])
	}
	if !strings.HasPrefix(lines[1], "hysteria2://bob:"+hy2["bob"]+"@hy2-c3d4.rwl.one:24430/") {
		t.Errorf("line 1 = %s", lines[1])
	}

	// The on-disk subscription file must carry the same payload.
	fileRaw, err := os.ReadFile(filepath.Join(subDir, "bob.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(fileRaw)) != strings.TrimSpace(out.String()) {
		t.Errorf("subscription file differs from stdout")
	}
}

// A broken direct node must not hand out a URI that cannot work.
func TestRunGenSubDirectWithoutRealityEmitsNothing(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	cfg := xray.NewBaseConfig("alice", "uuid-1", "pass-1")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	env := directEnv()
	env[state.KeyRealitySNI] = ""
	delete(env, state.KeyHy2Host)
	writeTestEnv(t, env)

	var out, errBuf bytes.Buffer
	if err := RunGenSub(context.Background(), UserInputs{Name: "alice", Domain: "cdn-a1b2.rwl.one"}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("expected no URI for a broken direct node, got %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "Reality params are incomplete") {
		t.Fatalf("expected a warning on stderr, got %q", errBuf.String())
	}
}

func TestRunGenSubDirectEmitsRealityAndHy2(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	cfg := xray.NewBaseConfig("alice", testUUID, "pass-1")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestEnv(t, directEnv())
	// withTempPaths seeds the hysteria config with alice: alice-hy2pw.

	var out, errBuf bytes.Buffer
	if err := RunGenSub(context.Background(), UserInputs{Name: "alice", Domain: "cdn-a1b2.rwl.one"}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %q", out.String())
	}
	if lines[0] != wantRealityURI {
		t.Errorf("VLESS line drifted\n got: %s\nwant: %s", lines[0], wantRealityURI)
	}
	wantHy2 := "hysteria2://alice:alice-hy2pw@hy2-c3d4.rwl.one:24430/?obfs=salamander&obfs-password=kQ3x&sni=hy2-c3d4.rwl.one&insecure=0#alice-HY2"
	if lines[1] != wantHy2 {
		t.Errorf("HY2 line drifted\n got: %s\nwant: %s", lines[1], wantHy2)
	}
}

func TestRegenerateSubscriptionsWritesBothLines(t *testing.T) {
	cfgPath, subDir := withTempPaths(t)
	cfg := xray.NewBaseConfig("alice", testUUID, "pass-1")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestEnv(t, directEnv())

	var warn bytes.Buffer
	if err := RegenerateSubscriptionsTo("cdn-a1b2.rwl.one", &warn); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(subDir, "alice.txt"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(decoded), "\n")
	if len(lines) != 2 || lines[0] != wantRealityURI || !strings.HasPrefix(lines[1], "hysteria2://alice:alice-hy2pw@") {
		t.Fatalf("unexpected subscription payload:\n%s", decoded)
	}
}
