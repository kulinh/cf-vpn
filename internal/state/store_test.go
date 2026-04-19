package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfvpn.env")

	s := map[string]string{"DOMAIN": "vpn.example.com", "TUNNEL_UUID": "abc"}
	if err := SaveAtomic(path, s, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if loaded["DOMAIN"] != "vpn.example.com" || loaded["TUNNEL_UUID"] != "abc" {
		t.Fatalf("unexpected loaded map: %#v", loaded)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected mode: %o", info.Mode().Perm())
	}
}
