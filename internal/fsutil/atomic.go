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
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// syncFile is the fsync seam. It exists so tests can prove that a failing
// fsync is reported rather than swallowed — there is no portable way to make a
// real fsync fail on demand.
var syncFile = func(f *os.File) error { return f.Sync() }

// DurabilityError reports a failure that happened AFTER the rename, i.e. once
// the new content was already published: only the fsync of the parent directory
// that makes the rename crash-proof did not complete.
//
// This must be distinguishable from a pre-rename failure. The two demand
// opposite reactions: a pre-rename failure means nothing changed, so the caller
// must abort; a DurabilityError means the file on disk IS the new content, so a
// caller that aborts leaves the service running the old config while the new one
// sits on disk — and the next restart, from anywhere, silently switches to a
// config nobody validated against the running state.
//
// The correct reaction is "the write happened: carry on with the restart, but
// say loudly that a crash in the next moments could lose the rename".
type DurabilityError struct {
	Path string
	Err  error
}

func (e *DurabilityError) Error() string {
	return fmt.Sprintf("%s was written and renamed into place, but its directory entry could not be flushed: %v", e.Path, e.Err)
}

func (e *DurabilityError) Unwrap() error { return e.Err }

// IsDurability reports whether err is (or wraps) a DurabilityError — the new
// content is on disk and callers should continue rather than roll back.
func IsDurability(err error) bool {
	var de *DurabilityError
	return errors.As(err, &de)
}

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

	// Past this point the new content is published. A directory-fsync failure is
	// therefore NOT a write failure: it is reported as a DurabilityError so the
	// caller can tell the difference (see the type's doc comment).
	if err := SyncDir(dir); err != nil {
		return &DurabilityError{Path: path, Err: err}
	}
	return nil
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
