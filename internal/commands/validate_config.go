package commands

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// xrayBinary is the xray executable used to parse a candidate config.
var xrayBinary = "xray"

// xrayValidateTimeout bounds the validation exec. `xray run -test` only parses
// and builds the config, so it is fast; the timeout exists so a wedged binary
// cannot hang an install.
const xrayValidateTimeout = 30 * time.Second

// validateXrayConfig checks that xray can actually load config. It is a var so
// tests can stub it — a rendered config with fixture keys is intentionally
// rejected by the real xray.
//
// H12: every path in this tree used to be write-then-restart-then-hope. Nothing
// ever called `xray run -test`, so a config xray refuses was published over the
// live one and only discovered when the restart failed — with the broken file
// already on disk and xray down. Validating a candidate BEFORE the live file is
// replaced turns that into an ordinary error with the node still serving.
//
// A node without the xray binary (a workstation, a container running only the
// CLI) skips validation rather than failing: there is nothing to validate with.
var validateXrayConfig = realValidateXrayConfig

// realValidateXrayConfig is the production implementation behind
// validateXrayConfig. Tests that want the genuine behaviour call it directly.
func realValidateXrayConfig(ctx context.Context, config []byte) error {
	if _, err := exec.LookPath(xrayBinary); err != nil {
		return nil
	}
	f, err := os.CreateTemp("", "cfvpn-xray-check-*.json")
	if err != nil {
		return fmt.Errorf("create temp config for validation: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(config); err != nil {
		f.Close()
		return fmt.Errorf("write temp config for validation: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write temp config for validation: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, xrayValidateTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, xrayBinary, "run", "-test", "-config", f.Name()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("xray rejected the new config (%w): %s", err, lastLine(out))
	}
	return nil
}

// NOTE on hysteria: `hysteria server` exposes no config-check flag (verified
// against `hysteria server --help` on the fleet's build: only -c/--config,
// --log-level, --log-format, --disable-update-check), so there is no equivalent
// pre-flight for hysteria.yaml. Its writers still restore the previous file if
// the restart fails.

// lastLine returns the most informative tail of a command's output: xray prints
// a banner first and the actual complaint last.
func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

// writeXrayConfigChecked validates a candidate xray config and only then
// replaces the live one. On validation failure the file on disk is untouched.
func writeXrayConfigChecked(ctx context.Context, path string, config []byte, mode os.FileMode) error {
	if err := validateXrayConfig(ctx, config); err != nil {
		return err
	}
	return writeAtomicFile(path, config, mode)
}

// WriteXrayConfigChecked is the exported form for cfvpn-agent.
func WriteXrayConfigChecked(ctx context.Context, path string, config []byte, mode os.FileMode) error {
	return writeXrayConfigChecked(ctx, path, config, mode)
}

// writeXrayConfigIfChanged is writeIfChanged for xray configs: identical
// content is a no-op (no validation exec), a changed one is validated first.
func writeXrayConfigIfChanged(ctx context.Context, path string, config []byte, mode os.FileMode) (bool, error) {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, config) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, writeXrayConfigChecked(ctx, path, config, mode)
}
