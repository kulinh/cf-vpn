package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kulinh/cf-vpn/internal/tailscale"
)

const derpTestPolicy = `// keep me
{
	"grants": [{"src": ["*"], "dst": ["*"], "ip": ["*"]}],
	"derpMap": {
		"OmitDefaultRegions": false,
		"Regions": {
			"900": {"RegionID": 900, "RegionCode": "hkg", "RegionName": "HKG-01",
				"Nodes": [{"Name": "900a", "RegionID": 900, "HostName": "derp-a.example.net", "DERPPort": 8443, "STUNPort": 3478}]},
		},
	},
}
`

type fakeDerpAPI struct {
	policy    []byte
	etag      string
	validated int
	sets      int
}

func (f *fakeDerpAPI) GetPolicy(context.Context) ([]byte, string, error) {
	return f.policy, f.etag, nil
}
func (f *fakeDerpAPI) ValidatePolicy(context.Context, []byte) error { f.validated++; return nil }
func (f *fakeDerpAPI) SetPolicy(_ context.Context, p []byte, ifMatch string) ([]byte, error) {
	if ifMatch != f.etag {
		return nil, os.ErrInvalid
	}
	f.sets++
	f.policy = p
	f.etag = f.etag + "'"
	return p, nil
}

func withDerpBackupDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := derpBackupDir
	derpBackupDir = dir
	t.Cleanup(func() { derpBackupDir = old })
	return dir
}

func TestChinaModeOnOffRoundTripsAndSnapshots(t *testing.T) {
	dir := withDerpBackupDir(t)
	api := &fakeDerpAPI{policy: []byte(derpTestPolicy), etag: `"e1"`}
	now := time.Date(2026, 9, 12, 1, 2, 3, 0, time.UTC)
	zero := time.Duration(0)
	deps := DerpDeps{API: api, Netcheck: func(context.Context) ([]byte, error) { return []byte("netcheck-output\n"), nil }, Now: func() time.Time { return now }, Settle: &zero}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out bytes.Buffer
	if err := RunDerpChinaMode(ctx, true, deps, &out, &out); err != nil {
		t.Fatal(err)
	}
	s, _ := tailscale.Summarize(api.policy)
	if !s.OmitDefaultRegions || api.sets != 1 || api.validated != 1 {
		t.Fatalf("on: summary=%+v sets=%d validated=%d", s, api.sets, api.validated)
	}
	if !strings.Contains(out.String(), "netcheck-output") {
		t.Fatalf("netcheck output must be printed:\n%s", out.String())
	}
	before, err := os.ReadFile(filepath.Join(dir, "20260912T010203Z.before.json"))
	if err != nil || !strings.Contains(string(before), "// keep me") {
		t.Fatalf("before snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "20260912T010203Z.after.json")); err != nil {
		t.Fatalf("after snapshot: %v", err)
	}
	info, _ := os.Stat(filepath.Join(dir, "20260912T010203Z.before.json"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode %v, want 600", info.Mode().Perm())
	}

	now = now.Add(time.Minute)
	if err := RunDerpChinaMode(ctx, false, deps, &out, &out); err != nil {
		t.Fatal(err)
	}
	a, _ := tailscale.Canonical([]byte(derpTestPolicy))
	b, _ := tailscale.Canonical(api.policy)
	if !bytes.Equal(a, b) {
		t.Fatalf("off must restore the original policy:\n%s\n%s", a, b)
	}
	if !strings.Contains(string(api.policy), "// keep me") {
		t.Fatal("comments must survive")
	}
}

func TestChinaModeIdempotentSkipsWrite(t *testing.T) {
	withDerpBackupDir(t)
	api := &fakeDerpAPI{policy: []byte(derpTestPolicy), etag: `"e1"`}
	zero := time.Duration(0)
	deps := DerpDeps{API: api, Netcheck: func(context.Context) ([]byte, error) { return nil, nil }, Settle: &zero}
	var out bytes.Buffer
	if err := RunDerpChinaMode(context.Background(), false, deps, &out, &out); err != nil {
		t.Fatal(err)
	}
	if api.sets != 0 || !strings.Contains(out.String(), "already in the requested state") {
		t.Fatalf("expected no write, sets=%d out=%s", api.sets, out.String())
	}
}

func TestRegionAddRemoveAndShow(t *testing.T) {
	withDerpBackupDir(t)
	api := &fakeDerpAPI{policy: []byte(derpTestPolicy), etag: `"e1"`}
	deps := DerpDeps{API: api}
	var out bytes.Buffer
	r := tailscale.Region{ID: 901, Code: "jpy", Name: "JPY-01", HostName: "derp-b.example.net", DERPPort: 8443, STUNPort: 3478}
	if err := RunDerpRegionAdd(context.Background(), r, deps, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := RunDerpShow(context.Background(), deps, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "region 901 jpy (JPY-01): derp-b.example.net derp=8443 stun=3478") || !strings.Contains(out.String(), "china-mode off") {
		t.Fatalf("show:\n%s", out.String())
	}
	if err := RunDerpRegionRemove(context.Background(), 901, deps, &out); err != nil {
		t.Fatal(err)
	}
	a, _ := tailscale.Canonical([]byte(derpTestPolicy))
	b, _ := tailscale.Canonical(api.policy)
	if !bytes.Equal(a, b) {
		t.Fatal("add+remove must round-trip")
	}
}

func TestDerpMissingOAuthEnvIsExplained(t *testing.T) {
	old := derpOAuthEnvPath
	derpOAuthEnvPath = filepath.Join(t.TempDir(), "missing.env")
	t.Cleanup(func() { derpOAuthEnvPath = old })
	err := RunDerpShow(context.Background(), DerpDeps{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "TS_OAUTH_CLIENT_ID") {
		t.Fatalf("expected a hint about the env file, got %v", err)
	}
}
