package commands

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/xray"
)

func stubKeygen(o xray.GenerateRealityOptions) (xray.RealityParams, error) {
	return xray.RealityParams{PrivateKey: "newpriv", PublicKey: "newpub", ShortID: "aabbccddeeff0011", Dest: o.Dest, SNI: o.SNI}, nil
}

func seedRealityNode(t *testing.T) {
	t.Helper()
	withRotateDirectTempPaths(t)
	saveEnv(t, map[string]string{state.KeyMode: "direct", state.KeyDomain: "d.example", state.KeyPublicIP: "1.2.3.4", state.KeyNodeID: "HAN-01"})
	rendered, err := templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
		Users: []templates.XrayUser{{Name: "kulinh", UUID: "uuid-1"}}, PrivateKey: "test-priv-x25519",
		ShortIDs: []string{"abcd1234"}, Dest: "www.microsoft.com:443", ServerNames: []string{"www.microsoft.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xrayConfigPath, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunRotateRealityWritesNewParams(t *testing.T) {
	seedRealityNode(t)
	rec := &installRecorder{}
	err := RunRotateReality(context.Background(), RotateRealityInputs{Dest: "vtv.vn:443"}, rec, stubKeygen, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := state.Load(envFilePath)
	if env[state.KeyRealityPub] != "newpub" || env[state.KeyRealityPriv] != "newpriv" || env[state.KeyRealityShortID] != "aabbccddeeff0011" ||
		env[state.KeyRealityDest] != "vtv.vn:443" || env[state.KeyRealitySNI] != "vtv.vn" {
		t.Fatalf("env not updated: %v", env)
	}
	cfg, _ := os.ReadFile(xrayConfigPath)
	for _, want := range []string{`"dest": "vtv.vn:443"`, `"newpriv"`, `"aabbccddeeff0011"`, `"vtv.vn"`, `"uuid-1"`} {
		if !strings.Contains(string(cfg), want) {
			t.Fatalf("xray config missing %s:\n%s", want, cfg)
		}
	}
	if rec.countJoined("systemctl restart cfvpn-xray.service") != 1 {
		t.Fatalf("xray restart calls: %s", recorderCalls(rec))
	}
	sub, err := os.ReadFile(subscriptionDir + "/kulinh.txt")
	if err != nil {
		t.Fatalf("subscription not regenerated: %v", err)
	}
	if !strings.Contains(string(sub), "") || len(sub) == 0 {
		t.Fatal("empty subscription")
	}
}

func TestRunRotateRealityRefusesCloudflareMode(t *testing.T) {
	seedRealityNode(t)
	saveEnv(t, map[string]string{state.KeyMode: "cloudflare"})
	err := RunRotateReality(context.Background(), RotateRealityInputs{Dest: "vtv.vn:443"}, &installRecorder{}, stubKeygen, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "direct") {
		t.Fatalf("expected mode error, got %v", err)
	}
}

func TestRunRotateRealityRejectsBadDest(t *testing.T) {
	seedRealityNode(t)
	for _, bad := range []string{"", "vtv.vn", "vtv.vn:443\ningress: x", ":443"} {
		if err := RunRotateReality(context.Background(), RotateRealityInputs{Dest: bad}, &installRecorder{}, stubKeygen, io.Discard, io.Discard); err == nil {
			t.Fatalf("dest %q must be rejected", bad)
		}
	}
}

func TestRunRotateRealityRestoresOldConfigWhenRestartFails(t *testing.T) {
	seedRealityNode(t)
	before, _ := os.ReadFile(xrayConfigPath)
	rec := &installRecorder{fail: "restart cfvpn-xray.service"}
	err := RunRotateReality(context.Background(), RotateRealityInputs{Dest: "vtv.vn:443"}, rec, stubKeygen, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected restart failure")
	}
	after, _ := os.ReadFile(xrayConfigPath)
	if string(after) != string(before) {
		t.Fatal("old xray config must be restored")
	}
	env, _ := state.Load(envFilePath)
	if env[state.KeyRealityPub] != "test-pub-x25519" {
		t.Fatal("env must be untouched when the restart fails")
	}
}
