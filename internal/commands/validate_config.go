package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kulinh/cf-vpn/internal/fsutil"
	"github.com/kulinh/cf-vpn/internal/systemd"
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
	cmd := exec.CommandContext(ctx, xrayBinary, "run", "-test", "-config", f.Name())
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+xrayAssetDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("xray rejected the new config (%w): %s", err, lastLine(out))
	}
	return nil
}

// defaultXrayAssetDir is where the Xray-install script puts geoip.dat and
// geosite.dat. It must match the Environment= line in systemd.XrayService()
// (internal/systemd/units.go), which is what the running service uses.
const defaultXrayAssetDir = "/usr/local/share/xray"

// xrayAssetDir returns the asset directory the validation run should use.
//
// The pre-flight must fail for the same reasons the service would and for no
// others. Our routing block references "geoip:private", so on any build that
// resolves assets while parsing, an exec inheriting a different (or empty)
// XRAY_LOCATION_ASSET would report "geoip.dat not found" for a config the node
// runs perfectly — a false negative that blocks every install, upgrade, rotate
// and user mutation. (xray 26.3 defers that load to runtime, so it does not bite
// today; pinning the value keeps the pre-flight honest across versions.)
//
// Two sources only, in order: this process's own environment (whatever the
// operator exported for cfvpnctl, or cfvpn-agent inherited from its unit's
// EnvironmentFile), then defaultXrayAssetDir. cfvpn-xray.service's own
// Environment= line is NOT read back — it is mirrored by defaultXrayAssetDir and
// a test pins the two together, so an operator who changes the unit must change
// that constant as well.
func xrayAssetDir() string {
	if dir := strings.TrimSpace(os.Getenv("XRAY_LOCATION_ASSET")); dir != "" {
		return dir
	}
	return defaultXrayAssetDir
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

// applyXrayConfig is the full publish cycle every xray writer owes the node:
// validate the candidate, replace the live file, restart xray, and — if the
// restart fails — put the previous bytes back and restart on those, so the node
// is never left with a config xray refuses AND xray down.
//
// Diagnostics from the restore path go to warn (the restart error itself is
// what the caller gets back).
func applyXrayConfig(ctx context.Context, config []byte, runner systemd.Runner, warn io.Writer) error {
	previous, previousErr := os.ReadFile(xrayConfigPath)
	if err := writeXrayConfigChecked(ctx, xrayConfigPath, config, 0o600); err != nil {
		// A DurabilityError means the rename already published the new config;
		// only its crash-durability is unproven. Returning here would leave xray
		// running the OLD config with the NEW one on disk — a divergence that the
		// next restart from anywhere resolves silently and in favour of the file
		// nobody restarted onto. Carry on and restart; report it as a warning.
		if !fsutil.IsDurability(err) {
			return err
		}
		warnf(warn, "warning: %v; continuing with the restart because the new config is live, "+
			"but a crash in the next moments could lose the rename", err)
	}
	if err := systemd.Restart(ctx, runner, xrayServiceUnit); err != nil {
		if previousErr == nil {
			if rerr := writeAtomicFile(xrayConfigPath, previous, 0o600); rerr != nil {
				warnf(warn, "warning: restore previous xray config failed: %v", rerr)
			} else if rerr := systemd.Restart(ctx, runner, xrayServiceUnit); rerr != nil {
				warnf(warn, "warning: restart %s on the restored config failed: %v", xrayServiceUnit, rerr)
			}
		}
		return fmt.Errorf("restart %s: %w", xrayServiceUnit, err)
	}
	return nil
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
