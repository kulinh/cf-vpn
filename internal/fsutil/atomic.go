// Package fsutil holds the one crash-atomic file write shared by every cfvpn
// config writer (env file, xray config, hysteria config, cloudflared config,
// subscription files, systemd units).
//
// Every writer used to hand-roll the same "write path+.tmp, Sync, Close,
// Rename" dance. That had two holes:
//
//   - a FIXED temp name (path + ".tmp"): two concurrent writers share it, so
//     writer B's O_TRUNC can land between writer A's Write and Rename and
//     publish a truncated config through an API called "atomic";
//   - no fsync of the parent directory: on ext4 data=ordered a rename is only
//     durable once the directory entry is flushed, so a power cut right after
//     Rename can leave the file missing or empty — exactly the "crash halfway
//     bricks the node" case the pattern exists to prevent.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// syncFile is the fsync seam. It exists so tests can prove that a failing
// fsync is reported rather than swallowed — there is no portable way to make a
// real fsync fail on demand.
var syncFile = func(f *os.File) error { return f.Sync() }

// WriteFile atomically writes data to path with the given mode.
//
// It creates a uniquely-named temp file in the target directory, chmods it to
// mode explicitly (os.CreateTemp is 0600 & ^umask, and an existing target's
// permissions must never be inherited), fsyncs the data, renames over the
// target, and finally fsyncs the parent directory so the rename itself is
// durable. On any failure the temp file is removed and the previous contents of
// path are left untouched.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".cfvpn-tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := syncFile(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	tmp = "" // renamed away; the deferred Remove must not touch the target
	return SyncDir(dir)
}

// SyncDir fsyncs a directory so entries created or renamed inside it survive a
// crash. Both the open and the fsync errors are returned: swallowing them would
// report a durable write that is not durable, which is the whole failure mode
// this helper exists to close.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s for fsync: %w", dir, err)
	}
	if err := syncFile(d); err != nil {
		d.Close()
		return fmt.Errorf("fsync %s: %w", dir, err)
	}
	return d.Close()
}
