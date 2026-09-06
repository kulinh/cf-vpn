package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failDirFsync makes only the parent-directory fsync fail — the post-rename
// step. The data fsync (a regular file) still runs for real.
func failDirFsync(t *testing.T) {
	t.Helper()
	old := syncFile
	syncFile = func(f *os.File) error {
		info, err := f.Stat()
		if err == nil && info.IsDir() {
			return errors.New("simulated fsync failure")
		}
		return f.Sync()
	}
	t.Cleanup(func() { syncFile = old })
}

// failDataFsync makes the pre-rename data fsync fail: nothing is published.
func failDataFsync(t *testing.T) {
	t.Helper()
	old := syncFile
	syncFile = func(f *os.File) error {
		info, err := f.Stat()
		if err == nil && !info.IsDir() {
			return errors.New("simulated data fsync failure")
		}
		return f.Sync()
	}
	t.Cleanup(func() { syncFile = old })
}

// The parent-directory fsync is what makes the rename durable; if it fails the
// caller must hear about it instead of being told the write succeeded.
func TestSyncDirReportsSyncFailure(t *testing.T) {
	failDirFsync(t)
	dir := t.TempDir()
	if err := SyncDir(dir); err == nil {
		t.Fatal("SyncDir swallowed the fsync failure")
	} else if !strings.Contains(err.Error(), "simulated fsync failure") {
		t.Fatalf("err = %v", err)
	}
}

// A failure BEFORE the rename publishes nothing: the previous file must be
// untouched and the error must NOT be a DurabilityError, because the caller has
// to abort.
func TestWriteFilePreRenameFailureLeavesFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	failDataFsync(t)

	err := WriteFile(path, []byte("candidate"), 0o600)
	if err == nil {
		t.Fatal("WriteFile reported success despite a failed data fsync")
	}
	if IsDurability(err) {
		t.Errorf("pre-rename failure reported as a DurabilityError: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "previous" {
		t.Fatalf("file was modified by a failed write: %q", raw)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file leaked: %v", entries)
	}
}

// A failure AFTER the rename has published the new content is a different
// animal: the file IS the new content, so the caller must be able to tell and
// carry on (restart the service) rather than abort.
func TestWriteFilePostRenameFailureIsADurabilityError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	failDirFsync(t)

	err := WriteFile(path, []byte("candidate"), 0o600)
	if err == nil {
		t.Fatal("WriteFile hid the failed directory fsync")
	}
	if !IsDurability(err) {
		t.Fatalf("err = %v, want a DurabilityError", err)
	}
	var de *DurabilityError
	if !errors.As(err, &de) {
		t.Fatal("errors.As could not extract the DurabilityError")
	}
	if de.Path != path {
		t.Errorf("DurabilityError.Path = %q, want %q", de.Path, path)
	}
	if !strings.Contains(err.Error(), "simulated fsync failure") {
		t.Errorf("the underlying fsync error is not reported: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "candidate" {
		t.Fatalf("content = %q, want the new content — the rename did happen", raw)
	}
}

func TestSyncDirReportsOpenFailure(t *testing.T) {
	if err := SyncDir(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("SyncDir swallowed the open failure")
	}
}

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
