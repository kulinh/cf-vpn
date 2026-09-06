package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// configLockName is the advisory lock file that serializes every
// read-modify-write of the node's xray/hysteria config. It lives next to the
// xray config so it moves with xrayConfigPath in tests and points at
// /etc/cfvpn/xray/.config.lock in production.
const configLockName = ".config.lock"

// ErrLockBusy is returned when the config lock is held elsewhere for longer
// than the caller's deadline. The agent maps it to 503 lock_busy.
var ErrLockBusy = errors.New("config lock is busy")

// lockPollInterval is how often a waiting caller retries the non-blocking
// flock.
var lockPollInterval = 100 * time.Millisecond

// defaultLockWait bounds a caller that passes a context without a deadline
// (every agent HTTP handler: net/http gives request contexts no timeout). It
// must stay well under the panel's own request timeout so the panel sees a
// clean 503 instead of a hung connection.
const defaultLockWait = 20 * time.Second

// AcquireConfigLock takes an exclusive OS file lock (flock) and returns a
// release func. It serializes the agent's concurrent HTTP handlers AND the
// cfvpnctl CLI against each other, so two config writers can't clobber one
// another (lost update, or exceeding MaxUsers). The lock is advisory but
// honored by every cfvpn writer that calls this.
//
// M-G3: the lock is taken with LOCK_EX|LOCK_NB and retried against ctx rather
// than blocking in the syscall. A blocking flock parks an OS thread per waiter
// (the Go runtime spawns one per blocked syscall) and ignores context
// cancellation entirely, so once `cfvpnctl install` started holding this lock
// for minutes — which it does now, see H11 — every retried POST /admin/v1/sync
// from the panel would pile up another wedged thread until the agent hit the
// 10,000-thread limit and panicked.
//
// Callers MUST defer the returned release func and MUST NOT nest calls: flock
// is per open file description, so a second acquisition inside the first
// deadlocks against itself. Internal helpers that run under a lock the caller
// already holds are named *Locked.
func AcquireConfigLock(ctx context.Context) (func(), error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > defaultLockWait {
		deadline = time.Now().Add(defaultLockWait)
	}

	lockPath := filepath.Join(filepath.Dir(xrayConfigPath), configLockName)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}

	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("flock %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("%w: %s held by another cfvpn writer", ErrLockBusy, lockPath)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("%w: %v", ErrLockBusy, ctx.Err())
		case <-time.After(lockPollInterval):
		}
	}
}
