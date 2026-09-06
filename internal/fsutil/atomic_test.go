package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileCreatesWithMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "cfg.json")
	if err := WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "hello" {
		t.Fatalf("content = %q", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

// The old writers used os.WriteFile / OpenFile(O_CREATE), which apply perm only
// when creating the file — a key left 0644 by acme.sh stayed 0644 forever.
func TestWriteFileTightensPermsOnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "new" {
		t.Fatalf("content = %q", raw)
	}
}

func TestWriteFileLeavesNoTempBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := WriteFile(path, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".cfvpn-tmp-") || strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file leaked: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Fatalf("unexpected dir contents: %v", entries)
	}
}

// Two writers must not share a temp path (the old `path + ".tmp"` did).
func TestWriteFileUsesUniqueTempNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	// A stale fixed-name temp from an older binary must not be reused/removed
	// in a way that corrupts the write.
	if err := os.WriteFile(path+".tmp", []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("fresh"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "fresh" {
		t.Fatalf("content = %q", raw)
	}
	if _, err := os.Stat(path + ".tmp"); err != nil {
		t.Fatalf("unrelated file was disturbed: %v", err)
	}
}

func TestWriteFileFailsOnUnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode bits do not deny access")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(filepath.Join(locked, "x"), []byte("a"), 0o600); err == nil {
		t.Fatal("expected error writing into a read-only directory")
	}
}
