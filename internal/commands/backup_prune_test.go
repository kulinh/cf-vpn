package commands

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func mkBackupDirs(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func dirNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// M-G9: every upgrade copies /etc/cfvpn (CF_API_TOKEN, AGENT_SHARED_SECRET,
// REALITY_PRIVATE_KEY, HY2_OBFS_PW, hysteria key.pem) into
// /etc/cfvpn.backup-<unixtime> and nothing ever removed the old ones.
func TestPruneConfigBackupsKeepsNewest(t *testing.T) {
	root := t.TempDir()
	mkBackupDirs(t, root,
		"cfvpn.backup-1777262001",
		"cfvpn.backup-1777430896",
		"cfvpn.backup-1781356987",
		"cfvpn.backup-1790000000",
		"cfvpn.backup-1795000000",
	)
	if err := pruneConfigBackups(root, 3); err != nil {
		t.Fatal(err)
	}
	got := dirNames(t, root)
	want := []string{"cfvpn.backup-1781356987", "cfvpn.backup-1790000000", "cfvpn.backup-1795000000"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("kept %v, want %v", got, want)
	}
}

// Timestamps must order numerically: lexically "cfvpn.backup-9" sorts after
// "cfvpn.backup-10".
func TestPruneConfigBackupsOrdersNumerically(t *testing.T) {
	root := t.TempDir()
	mkBackupDirs(t, root, "cfvpn.backup-9", "cfvpn.backup-10", "cfvpn.backup-11", "cfvpn.backup-8")
	if err := pruneConfigBackups(root, 2); err != nil {
		t.Fatal(err)
	}
	got := dirNames(t, root)
	want := []string{"cfvpn.backup-10", "cfvpn.backup-11"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("kept %v, want %v", got, want)
	}
}

// Anything that is not a cfvpn backup directory must survive untouched — the
// backup root is /etc.
func TestPruneConfigBackupsLeavesUnrelatedEntriesAlone(t *testing.T) {
	root := t.TempDir()
	mkBackupDirs(t, root,
		"cfvpn.backup-1", "cfvpn.backup-2", "cfvpn.backup-3", "cfvpn.backup-4",
		"cfvpn", "systemd", "cfvpn.backup-notanumber",
	)
	if err := os.WriteFile(filepath.Join(root, "cfvpn.backup-5"), []byte("a file, not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pruneConfigBackups(root, 2); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(dirNames(t, root), ",")
	for _, keep := range []string{"cfvpn,", "systemd", "cfvpn.backup-notanumber", "cfvpn.backup-5"} {
		if !strings.Contains(got, strings.TrimSuffix(keep, ",")) {
			t.Errorf("unrelated entry %q was removed; left: %s", keep, got)
		}
	}
	for _, gone := range []string{"cfvpn.backup-1", "cfvpn.backup-2"} {
		if strings.Contains(got, gone) {
			t.Errorf("%s should have been pruned; left: %s", gone, got)
		}
	}
}

func TestPruneConfigBackupsNoopCases(t *testing.T) {
	root := t.TempDir()
	mkBackupDirs(t, root, "cfvpn.backup-1", "cfvpn.backup-2")
	if err := pruneConfigBackups(root, 3); err != nil {
		t.Fatal(err)
	}
	if len(dirNames(t, root)) != 2 {
		t.Fatalf("pruned below the keep count: %v", dirNames(t, root))
	}
	if err := pruneConfigBackups(filepath.Join(root, "missing"), 3); err != nil {
		t.Fatalf("a missing backup root must not be an error: %v", err)
	}
}
