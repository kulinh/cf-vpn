package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/fsutil"
	"github.com/kulinh/cf-vpn/internal/systemd"
)

// H12: a config xray refuses must never reach the live file.
func TestWriteXrayConfigCheckedKeepsOldConfigOnValidationFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"good":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	old := validateXrayConfig
	validateXrayConfig = func(context.Context, []byte) error { return fmt.Errorf("xray rejected the new config") }
	t.Cleanup(func() { validateXrayConfig = old })

	err := writeXrayConfigChecked(context.Background(), path, []byte(`{"bad":true}`), 0o600)
	if err == nil {
		t.Fatal("expected the write to fail")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != `{"good":true}` {
		t.Fatalf("live config was replaced with the rejected one: %s", raw)
	}
}

func TestWriteXrayConfigCheckedWritesWhenValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	old := validateXrayConfig
	var seen []byte
	validateXrayConfig = func(_ context.Context, cfg []byte) error {
		seen = append([]byte(nil), cfg...)
		return nil
	}
	t.Cleanup(func() { validateXrayConfig = old })

	if err := writeXrayConfigChecked(context.Background(), path, []byte(`{"ok":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(seen, []byte(`{"ok":1}`)) {
		t.Fatalf("validator saw %q", seen)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != `{"ok":1}` {
		t.Fatalf("config = %s", raw)
	}
}

// Identical content must not pay for an exec, and must not report a change.
func TestWriteXrayConfigIfChangedSkipsIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"ok":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	old := validateXrayConfig
	calls := 0
	validateXrayConfig = func(context.Context, []byte) error { calls++; return nil }
	t.Cleanup(func() { validateXrayConfig = old })

	changed, err := writeXrayConfigIfChanged(context.Background(), path, []byte(`{"ok":1}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("reported a change for identical content")
	}
	if calls != 0 {
		t.Errorf("validated %d times for identical content, want 0", calls)
	}

	changed, err = writeXrayConfigIfChanged(context.Background(), path, []byte(`{"ok":2}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || calls != 1 {
		t.Errorf("changed=%v calls=%d, want true/1", changed, calls)
	}
}

// The real validator: exercised only where an xray binary exists.
func TestValidateXrayConfigAgainstRealBinary(t *testing.T) {
	if _, err := exec.LookPath(xrayBinary); err != nil {
		t.Skipf("xray not installed: %v", err)
	}
	good := []byte(`{"inbounds":[{"port":10001,"listen":"127.0.0.1","protocol":"vless",` +
		`"settings":{"clients":[],"decryption":"none"}}],"outbounds":[{"protocol":"freedom"}]}`)
	if err := realValidateXrayConfig(context.Background(), good); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []byte(`{"inbounds":[{"protocol":"nope"}]}`)
	err := realValidateXrayConfig(context.Background(), bad)
	if err == nil {
		t.Fatal("invalid config accepted")
	}
	if !strings.Contains(err.Error(), "xray rejected the new config") {
		t.Fatalf("err = %v", err)
	}
}

// Nodes (and CI images) without xray must not be blocked by the pre-flight.
func TestValidateXrayConfigSkipsWhenBinaryMissing(t *testing.T) {
	old := xrayBinary
	xrayBinary = "cfvpn-xray-does-not-exist"
	t.Cleanup(func() { xrayBinary = old })
	if err := realValidateXrayConfig(context.Background(), []byte(`{"nonsense":`)); err != nil {
		t.Fatalf("expected a skip, got %v", err)
	}
}

// A post-rename durability failure means the new config IS live. Aborting there
// would leave xray running the old config with the new one on disk, to be
// adopted silently by the next restart from anywhere — so applyXrayConfig must
// carry on, restart, and report the problem as a warning. The warning itself
// now comes from writeXrayConfigChecked (the lower layer that decided the
// error is survivable), so it lands on stderr rather than applyXrayConfig's own
// warn sink.
func TestApplyXrayConfigContinuesAfterDurabilityError(t *testing.T) {
	withTempPaths(t)
	if err := os.WriteFile(xrayConfigPath, []byte(`{"previous":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	oldWrite := writeAtomicFile
	writeAtomicFile = func(path string, content []byte, mode os.FileMode) error {
		if err := oldWrite(path, content, mode); err != nil {
			return err
		}
		// The real writer's post-rename failure: content published, directory
		// entry not flushed.
		return &fsutil.DurabilityError{Path: path, Err: errors.New("simulated fsync failure")}
	}
	t.Cleanup(func() { writeAtomicFile = oldWrite })

	r := &userRestartRunner{}
	var warn bytes.Buffer
	stderrText := captureStderr(t, func() {
		if err := applyXrayConfig(context.Background(), []byte(`{"candidate":true}`), r, &warn); err != nil {
			t.Fatalf("applyXrayConfig aborted on a published-but-unflushed write: %v", err)
		}
	})

	raw, _ := os.ReadFile(xrayConfigPath)
	if string(raw) != `{"candidate":true}` {
		t.Fatalf("live config = %s, want the new content", raw)
	}
	if !strings.Contains(r.joined(), "restart cfvpn-xray.service") {
		t.Fatalf("xray was not restarted onto the published config:\n%s", r.joined())
	}
	if !strings.Contains(stderrText, "simulated fsync failure") {
		t.Errorf("the durability problem was not surfaced: %q", stderrText)
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns what was
// written to it. Used for the handful of warnings that writeXrayConfigChecked
// emits directly to stderr because it has no warn sink of its own.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	os.Stderr = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// A failure BEFORE the rename publishes nothing, so it must still abort — no
// restart, live file untouched.
func TestApplyXrayConfigAbortsOnPreRenameWriteFailure(t *testing.T) {
	withTempPaths(t)
	if err := os.WriteFile(xrayConfigPath, []byte(`{"previous":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	oldWrite := writeAtomicFile
	writeAtomicFile = func(string, []byte, os.FileMode) error {
		return errors.New("disk full before rename")
	}
	t.Cleanup(func() { writeAtomicFile = oldWrite })

	r := &userRestartRunner{}
	var warn bytes.Buffer
	err := applyXrayConfig(context.Background(), []byte(`{"candidate":true}`), r, &warn)
	if err == nil {
		t.Fatal("applyXrayConfig ignored a pre-rename write failure")
	}
	raw, _ := os.ReadFile(xrayConfigPath)
	if string(raw) != `{"previous":true}` {
		t.Fatalf("live config = %s, want the previous content", raw)
	}
	if strings.Contains(r.joined(), "restart cfvpn-xray.service") {
		t.Fatalf("xray was restarted despite nothing being published:\n%s", r.joined())
	}
}

// The validation exec must run with the same XRAY_LOCATION_ASSET the service
// uses, so the pre-flight cannot fail for an asset-path reason the running node
// would not have.
func TestValidateXrayConfigPassesAssetDir(t *testing.T) {
	dir := t.TempDir()
	captured := filepath.Join(dir, "captured")
	script := filepath.Join(dir, "fake-xray")
	body := "#!/bin/sh\nprintf '%s' \"$XRAY_LOCATION_ASSET\" > " + captured + "\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	oldBin := xrayBinary
	xrayBinary = script
	t.Cleanup(func() { xrayBinary = oldBin })

	t.Setenv("XRAY_LOCATION_ASSET", "")
	if err := realValidateXrayConfig(context.Background(), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != defaultXrayAssetDir {
		t.Errorf("XRAY_LOCATION_ASSET = %q, want the service default %q", got, defaultXrayAssetDir)
	}

	// An operator override wins — the service would inherit it too.
	t.Setenv("XRAY_LOCATION_ASSET", "/opt/xray-assets")
	if err := realValidateXrayConfig(context.Background(), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(captured)
	if string(got) != "/opt/xray-assets" {
		t.Errorf("XRAY_LOCATION_ASSET = %q, want the override", got)
	}
}

func TestXrayAssetDirMatchesTheUnitFile(t *testing.T) {
	// systemd.XrayService() is what the running node uses; a drift between the
	// two would make the pre-flight validate against different assets.
	unit := systemd.XrayService(xrayConfigPath)
	if !strings.Contains(unit, "Environment=XRAY_LOCATION_ASSET="+defaultXrayAssetDir) {
		t.Fatalf("cfvpn-xray.service does not set XRAY_LOCATION_ASSET=%s:\n%s", defaultXrayAssetDir, unit)
	}
}

func TestLastLineReturnsTheComplaint(t *testing.T) {
	out := []byte("Xray 26.3.27 banner\nA unified platform.\nFailed to start: invalid privateKey\n\n")
	if got := lastLine(out); got != "Failed to start: invalid privateKey" {
		t.Fatalf("lastLine = %q", got)
	}
	if got := lastLine(nil); got != "" {
		t.Fatalf("lastLine(nil) = %q", got)
	}
}
