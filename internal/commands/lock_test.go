package commands

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireConfigLockSerializesAndReleases(t *testing.T) {
	withTempPaths(t)

	unlock, err := AcquireConfigLock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(xrayConfigPath), configLockName)); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
	unlock()

	unlock2, err := AcquireConfigLock(context.Background())
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	unlock2()
}

// M-G3: a busy lock must fail fast with ErrLockBusy instead of blocking in the
// flock syscall (which parks an OS thread per waiter and ignores ctx, so panel
// retries would walk the agent into "too many threads").
func TestAcquireConfigLockReturnsErrLockBusy(t *testing.T) {
	withTempPaths(t)
	lockPath := filepath.Join(filepath.Dir(xrayConfigPath), configLockName)

	// Hold the lock from another process: flock is per open file description,
	// so a second holder inside this process would be indistinguishable from
	// the deadlock we forbid.
	holder := exec.Command("flock", "-x", lockPath, "sleep", "30")
	if err := holder.Start(); err != nil {
		t.Skipf("flock(1) not available: %v", err)
	}
	defer func() {
		_ = holder.Process.Kill()
		_, _ = holder.Process.Wait()
	}()
	waitUntilLocked(t, lockPath)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	unlock, err := AcquireConfigLock(ctx)
	if err == nil {
		unlock()
		t.Fatal("acquired a lock held by another process")
	}
	if !errors.Is(err, ErrLockBusy) {
		t.Fatalf("err = %v, want ErrLockBusy", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("waited %v: the acquisition blocked instead of polling the context", elapsed)
	}
}

func TestAcquireConfigLockHonoursCancelledContext(t *testing.T) {
	withTempPaths(t)
	lockPath := filepath.Join(filepath.Dir(xrayConfigPath), configLockName)

	holder := exec.Command("flock", "-x", lockPath, "sleep", "30")
	if err := holder.Start(); err != nil {
		t.Skipf("flock(1) not available: %v", err)
	}
	defer func() {
		_ = holder.Process.Kill()
		_, _ = holder.Process.Wait()
	}()
	waitUntilLocked(t, lockPath)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if unlock, err := AcquireConfigLock(ctx); err == nil {
		unlock()
		t.Fatal("expected a cancelled context to abort the acquisition")
	} else if !errors.Is(err, ErrLockBusy) {
		t.Fatalf("err = %v, want ErrLockBusy", err)
	}
}

// waitUntilLocked waits for the external flock(1) holder to actually hold the
// lock, so the test does not race its startup.
func waitUntilLocked(t *testing.T, lockPath string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := exec.Command("flock", "-n", "-x", lockPath, "true").Run(); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Skip("could not observe the external flock holder taking the lock")
}
