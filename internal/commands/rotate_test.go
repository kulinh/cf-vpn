package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/xray"
)

type fakeCF struct {
	createCalled int
	lastName     string
	newID        string
	creds        []byte
	zones        map[string]string
	upserts      [][3]string
	deleted      []string
	failCreate   bool
}

func (f *fakeCF) GetZoneID(_ context.Context, d string) (string, error) {
	if z, ok := f.zones[d]; ok {
		return z, nil
	}
	return "", errors.New("zone not found")
}

func (f *fakeCF) CreateTunnel(_ context.Context, name string) (string, []byte, error) {
	f.createCalled++
	f.lastName = name
	if f.failCreate {
		return "", nil, errors.New("boom")
	}
	return f.newID, f.creds, nil
}

func (f *fakeCF) UpsertCNAME(_ context.Context, z, n, t string) error {
	f.upserts = append(f.upserts, [3]string{z, n, t})
	return nil
}

func (f *fakeCF) DeleteTunnel(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func TestValidateRotateDomains(t *testing.T) {
	if err := ValidateRotateDomains("", "b"); err == nil {
		t.Fatal("want err on empty old")
	}
	if err := ValidateRotateDomains("a", "a"); err == nil {
		t.Fatal("want err on same")
	}
	if err := ValidateRotateDomains("a", "b"); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestRunRotateDomainHappyPath(t *testing.T) {
	dir := t.TempDir()
	oldEnv, oldCred, oldCfg, oldXray, oldSub := envFilePath, cloudflaredCredDir, cloudflaredConfig, xrayConfigPath, subscriptionDir
	envFilePath = filepath.Join(dir, "cfvpn.env")
	cloudflaredCredDir = filepath.Join(dir, "creds")
	cloudflaredConfig = filepath.Join(dir, "cloudflared.yml")
	xrayConfigPath = filepath.Join(dir, "xray.json")
	subscriptionDir = filepath.Join(dir, "subs")
	t.Cleanup(func() {
		envFilePath, cloudflaredCredDir, cloudflaredConfig, xrayConfigPath, subscriptionDir = oldEnv, oldCred, oldCfg, oldXray, oldSub
	})

	if err := state.SaveAtomic(envFilePath, map[string]string{"DOMAIN": "old.com", "TUNNEL_UUID": "old-uuid"}, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	if err := xray.AddUser(&cfg, "alice", "u-a", "p-a"); err != nil {
		t.Fatal(err)
	}
	if err := xray.SaveAtomic(xrayConfigPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	cf := &fakeCF{newID: "new-uuid", creds: []byte(`{"a":1}`), zones: map[string]string{"new.com": "z1"}}
	runner := &stubRunner{}
	var out, errBuf bytes.Buffer
	err := RunRotateDomain(context.Background(), RotateInputs{
		NewDomain: "new.com", OldDomain: "old.com", OldTunnel: "old-uuid",
		CFAPIToken: "t", CFAccountID: "a",
	}, RotateDeps{CF: cf, Runner: runner}, &out, &errBuf)
	if err != nil {
		t.Fatal(err)
	}

	if cf.createCalled != 1 {
		t.Fatalf("expected 1 create call")
	}
	if cf.lastName != "cfvpn-new.com" {
		t.Fatalf("unexpected tunnel name: %q", cf.lastName)
	}
	if len(cf.upserts) != 1 {
		t.Fatalf("expected 1 dns upsert, got %d", len(cf.upserts))
	}
	if cf.upserts[0] != [3]string{"z1", "new.com", "new-uuid.cfargotunnel.com"} {
		t.Fatalf("unexpected upsert payload: %#v", cf.upserts[0])
	}
	if _, err := os.Stat(filepath.Join(cloudflaredCredDir, "new-uuid.json")); err != nil {
		t.Fatalf("creds file missing: %v", err)
	}
	if _, err := os.Stat(cloudflaredConfig); err != nil {
		t.Fatalf("cloudflared config missing: %v", err)
	}
	raw, err := os.ReadFile(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("DOMAIN=new.com")) {
		t.Fatalf("env not updated: %s", raw)
	}
	if !bytes.Contains(raw, []byte("TUNNEL_UUID=new-uuid")) {
		t.Fatalf("env TUNNEL_UUID not updated: %s", raw)
	}
	if runner.calls != 2 {
		t.Fatalf("expected 2 restarts, got %d", runner.calls)
	}
	for _, n := range []string{"user1", "alice"} {
		if _, err := os.Stat(filepath.Join(subscriptionDir, n+".txt")); err != nil {
			t.Fatalf("subscription %s missing: %v", n, err)
		}
	}
	_ = json.Marshal
}

func TestRunRotateCleanup(t *testing.T) {
	dir := t.TempDir()
	oldCred := cloudflaredCredDir
	cloudflaredCredDir = dir
	t.Cleanup(func() { cloudflaredCredDir = oldCred })

	credFile := filepath.Join(dir, "old-id.json")
	if err := os.WriteFile(credFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cf := &fakeCF{}
	var out, errBuf bytes.Buffer
	if err := RunRotateCleanup(context.Background(), "old-id", RotateDeps{CF: cf, Runner: &stubRunner{}}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if len(cf.deleted) != 1 || cf.deleted[0] != "old-id" {
		t.Fatalf("expected 1 delete, got %#v", cf.deleted)
	}
	if _, err := os.Stat(credFile); !os.IsNotExist(err) {
		t.Fatal("creds file should be removed")
	}
}
