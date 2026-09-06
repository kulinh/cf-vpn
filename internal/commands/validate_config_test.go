package commands

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	if err := validateXrayConfig(context.Background(), good); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []byte(`{"inbounds":[{"protocol":"nope"}]}`)
	err := validateXrayConfig(context.Background(), bad)
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
	if err := validateXrayConfig(context.Background(), []byte(`{"nonsense":`)); err != nil {
		t.Fatalf("expected a skip, got %v", err)
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
