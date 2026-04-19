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

func TestSaveAtomicOverwriteKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfvpn.env")

	if err := SaveAtomic(path, map[string]string{"A": "1"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveAtomic(path, map[string]string{"A": "2", "B": "3"}, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "2" || got["B"] != "3" {
		t.Fatalf("unexpected values: %#v", got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode drifted: %o", info.Mode().Perm())
	}
	// Temp file should not linger.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file leaked: %v", err)
	}
}
