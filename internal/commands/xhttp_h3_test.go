package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/templates"
)

// Every direct-mode render path must carry the node's H3 route. There are
// seven call sites of RenderXrayDirectReality (install x3, rotate, reality
// rotate, the agent's user sync, and the enable command itself) and a single
// one that forgets drops the inbound the next time that path runs — the agent
// sync being the nastiest, since adding a user from the panel would silently
// delete a working route.
//
// This asserts the invariant at the source level because it is the only thing
// that catches an eighth call site added later.
func TestEveryDirectRealityRenderCarriesH3(t *testing.T) {
	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", ".wrangler", ".worktree-backups":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// The renderer's own package defines the function; it does not call it.
		if strings.Contains(filepath.ToSlash(path), "internal/templates/") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, "RenderXrayDirectReality(") {
				continue
			}
			if strings.Contains(line, "WithH3FromEnv(") {
				continue
			}
			offenders = append(offenders, filepath.ToSlash(path)+":"+itoa(i+1))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("these RenderXrayDirectReality call sites do not pass the node's H3 route "+
			"through WithH3FromEnv, so the H3 inbound disappears when they run:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestWithH3FromEnvFillsCertPathsFromHysteria(t *testing.T) {
	env := map[string]string{
		state.KeyXHTTPH3Host: "quic-b55170f3.dongnat247.com",
		state.KeyXHTTPH3Path: "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10",
	}
	got := WithH3FromEnv(templates.XrayDirectRealityInputs{}, env)
	if got.H3Host != "quic-b55170f3.dongnat247.com" || got.H3Path != "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10" {
		t.Fatalf("host/path not carried: %+v", got)
	}
	wantCert, wantKey := HysteriaCertPaths()
	if got.H3Cert != wantCert || got.H3Key != wantKey {
		t.Fatalf("cert paths = %q/%q, want %q/%q", got.H3Cert, got.H3Key, wantCert, wantKey)
	}
}

// A node with no H3 route must come back untouched, so the renderer produces
// byte-identical output to what it produced before this feature existed.
func TestWithH3FromEnvLeavesInputsAloneWhenUnset(t *testing.T) {
	in := templates.XrayDirectRealityInputs{Dest: "www.sony.jp:443"}
	got := WithH3FromEnv(in, map[string]string{})
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("inputs mutated for a node without an H3 route: %+v", got)
	}
}

// Half-configured must not reach the renderer as a cert-path-only struct; the
// renderer rejects it, which would turn "someone typo'd the env file" into a
// failed restart. Drop it here instead.
func TestWithH3FromEnvIgnoresHalfConfiguredRoute(t *testing.T) {
	in := templates.XrayDirectRealityInputs{Dest: "www.sony.jp:443"}
	got := WithH3FromEnv(in, map[string]string{state.KeyXHTTPH3Host: "quic-b55170f3.dongnat247.com"})
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("half-configured H3 route leaked into render inputs: %+v", got)
	}
}

// seedDirectNode writes a working direct-mode node into the temp paths: the
// env file plus the REALITY-only xray config it would be running today.
func seedDirectNode(t *testing.T) string {
	t.Helper()
	withRotateCloudflareTempPaths(t)
	env := map[string]string{
		state.KeyMode:           "direct",
		state.KeyDomain:         "edge-64b43148.dongnat247.com",
		state.KeyNodeID:         "jpy-03",
		state.KeyPublicIP:       "129.225.185.197",
		state.KeyRealityPriv:    "priv-x25519",
		state.KeyRealityPub:     "pub-x25519",
		state.KeyRealityShortID: "2441ae2d78da98bb",
		state.KeyRealityDest:    "www.sony.jp:443",
		state.KeyRealitySNI:     "www.sony.jp",
	}
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		t.Fatal(err)
	}
	rendered, err := templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
		Users:       []templates.XrayUser{{Name: "kulinh", UUID: "74ccc0e3-d8e0-4132-9497-c2ba20a9efce"}},
		PrivateKey:  "priv-x25519",
		ShortIDs:    []string{"2441ae2d78da98bb"},
		Dest:        "www.sony.jp:443",
		ServerNames: []string{"www.sony.jp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xrayConfigPath, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
	return rendered
}

const testH3Path = "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10"

func TestRunXHTTPH3SetEnablesTheInbound(t *testing.T) {
	seedDirectNode(t)
	var out, errb bytes.Buffer
	if err := RunXHTTPH3Set(context.Background(), "quic-b55170f3.dongnat247.com", testH3Path, &installRecorder{}, &out, &errb); err != nil {
		t.Fatalf("enable: %v (stderr: %s)", err, errb.String())
	}
	env, err := state.Load(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if env[state.KeyXHTTPH3Host] != "quic-b55170f3.dongnat247.com" || env[state.KeyXHTTPH3Path] != testH3Path {
		t.Fatalf("env not persisted: %v", env)
	}
	cfg := readTestFile(t, xrayConfigPath)
	if !strings.Contains(cfg, "vless-xhttp-h3") {
		t.Fatalf("H3 inbound missing from written config:\n%s", cfg)
	}
	if !strings.Contains(cfg, "vless-reality") {
		t.Fatalf("REALITY inbound must survive:\n%s", cfg)
	}
}

func TestRunXHTTPH3SetDisableRemovesTheInbound(t *testing.T) {
	before := seedDirectNode(t)
	ctx := context.Background()
	if err := RunXHTTPH3Set(ctx, "quic-b55170f3.dongnat247.com", testH3Path, &installRecorder{}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := RunXHTTPH3Set(ctx, "", "", &installRecorder{}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	env, err := state.Load(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if env[state.KeyXHTTPH3Host] != "" || env[state.KeyXHTTPH3Path] != "" {
		t.Fatalf("env keys not cleared: %v", env)
	}
	// Disabling must land back on exactly the config the node had before, not
	// merely on "a config without the H3 inbound".
	if got := readTestFile(t, xrayConfigPath); got != before {
		t.Fatalf("disable did not restore the original config.\n got:\n%s\nwant:\n%s", got, before)
	}
}

func TestRunXHTTPH3SetRejectsCloudflareMode(t *testing.T) {
	seedDirectNode(t)
	env, _ := state.Load(envFilePath)
	env[state.KeyMode] = "cloudflare"
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		t.Fatal(err)
	}
	err := RunXHTTPH3Set(context.Background(), "quic-b55170f3.dongnat247.com", testH3Path, &installRecorder{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "MODE=direct") {
		t.Fatalf("expected a MODE=direct refusal, got %v", err)
	}
}

func TestRunXHTTPH3SetRejectsBadArgs(t *testing.T) {
	cases := []struct{ name, host, path string }{
		{"host without path", "quic-b55170f3.dongnat247.com", ""},
		{"path without host", "", testH3Path},
		{"path without leading slash", "quic-b55170f3.dongnat247.com", "no-slash-here-and-long-enough"},
		{"guessable short path", "quic-b55170f3.dongnat247.com", "/stream"},
		{"path with query", "quic-b55170f3.dongnat247.com", "/3e6f9770dcd50c915247c33fd08?x=1"},
		{"not a hostname", "not a hostname", testH3Path},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seedDirectNode(t)
			if err := RunXHTTPH3Set(context.Background(), tc.host, tc.path, &installRecorder{}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
				t.Fatalf("expected an error for %s", tc.name)
			}
		})
	}
}

// The node must never end up advertising a route the running xray does not
// serve, so a failed restart leaves both the env file and the config alone.
func TestRunXHTTPH3SetKeepsEnvAndConfigWhenRestartFails(t *testing.T) {
	before := seedDirectNode(t)
	rec := &installRecorder{fail: "restart cfvpn-xray.service"}
	if err := RunXHTTPH3Set(context.Background(), "quic-b55170f3.dongnat247.com", testH3Path, rec, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected the restart failure to surface")
	}
	env, err := state.Load(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if env[state.KeyXHTTPH3Host] != "" {
		t.Error("XHTTP_H3_HOST must not be persisted when the restart failed")
	}
	if got := readTestFile(t, xrayConfigPath); got != before {
		t.Error("the previous xray config must be restored")
	}
}
