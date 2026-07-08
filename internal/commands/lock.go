package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// configLockName is the advisory lock file that serializes every
// read-modify-write of the node's xray/hysteria config. It lives next to the
// xray config so it moves with xrayConfigPath in tests and points at
// /etc/cfvpn/xray/.config.lock in production.
const configLockName = ".config.lock"

// AcquireConfigLock takes an exclusive OS file lock (flock) and returns a
// release func. It serializes the agent's concurrent HTTP handlers AND the
// cfvpnctl CLI against each other, so two "add user" flows can't clobber one
// another's config (lost update, or exceeding MaxUsers). The lock is advisory
// but honored by every cfvpn writer that calls this. Callers MUST defer the
// returned release func and MUST NOT nest calls (that would self-deadlock).
func AcquireConfigLock() (func(), error) {
	lockPath := filepath.Join(filepath.Dir(xrayConfigPath), configLockName)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock %s: %w", lockPath, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
