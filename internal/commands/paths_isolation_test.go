package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain redirects every mutable node path and every system-touching seam to
// a per-run temp directory BEFORE any test executes.
//
// This package's tests are routinely run as root on a node (that is where the
// repo lives), so a helper that forgets one override does not fail a test — it
// silently reconfigures the live node. That is not hypothetical: withUpgradeSeams
// once forgot hysteriaConfigPath and the fixtures replaced a node's real
// hysteria config (port, obfs password, user set), damage that would only have
// surfaced at the next hysteria restart.
//
// Defaults belong here rather than in each helper: a per-test helper that
// forgets one var then merely inherits a safe temp path instead of /etc.
// Helpers still redirect and seed their own content — they just no longer carry
// the whole safety burden.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cfvpn-commands-test-*")
	if err != nil {
		panic(err)
	}

	// Node config paths.
	envFilePath = filepath.Join(dir, "cfvpn", "cfvpn.env")
	xrayConfigPath = filepath.Join(dir, "cfvpn", "xray", "config.json")
	cloudflaredConfig = filepath.Join(dir, "cfvpn", "cloudflared", "config.yml")
	cloudflaredCredDir = filepath.Join(dir, "cfvpn", "cloudflared")
	hysteriaConfigPath = filepath.Join(dir, "cfvpn", "hysteria", "config.yaml")
	hysteriaCertDir = filepath.Join(dir, "cfvpn", "hysteria")
	subscriptionDir = filepath.Join(dir, "subscriptions")
	systemdUnitDir = filepath.Join(dir, "systemd")
	sysctlConfPath = filepath.Join(dir, "sysctl.d", "90-cfvpn.conf")

	// System-touching seams.
	runTuneCommand = func(context.Context, string, ...string) ([]byte, error) { return []byte("bbr\n"), nil }
	// Fixture Reality keys are correctly rejected by a real xray; tests that
	// exercise the pre-flight call realValidateXrayConfig directly.
	validateXrayConfig = func(context.Context, []byte) error { return nil }

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// nodePathVars is every package-level path a test could write through.
func nodePathVars() map[string]string {
	return map[string]string{
		"envFilePath":        envFilePath,
		"xrayConfigPath":     xrayConfigPath,
		"cloudflaredConfig":  cloudflaredConfig,
		"cloudflaredCredDir": cloudflaredCredDir,
		"hysteriaConfigPath": hysteriaConfigPath,
		"hysteriaCertDir":    hysteriaCertDir,
		"subscriptionDir":    subscriptionDir,
		"systemdUnitDir":     systemdUnitDir,
		"sysctlConfPath":     sysctlConfPath,
	}
}

// No test may run with a node path pointing into the live system. Asserting the
// negative ("not under /etc") rather than an exact value keeps this true whether
// the value comes from TestMain's defaults or a per-test helper's temp dir.
func TestNodePathVarsNeverPointAtTheLiveSystem(t *testing.T) {
	for name, path := range nodePathVars() {
		if path == "" {
			t.Errorf("%s is empty", name)
			continue
		}
		for _, forbidden := range []string{"/etc/", "/var/lib/cfvpn", "/usr/", "/lib/"} {
			if strings.HasPrefix(path, forbidden) {
				t.Errorf("%s = %q points at the live system (%s); TestMain must redirect it",
					name, path, forbidden)
			}
		}
	}
}

// A helper that redirects the node paths must redirect ALL of them; this test
// documents the full set so a new path var is not forgotten.
func TestSeamHelperRedirectsEveryNodePath(t *testing.T) {
	dir := withUpgradeSeams(t)
	for _, name := range []string{
		"envFilePath", "xrayConfigPath", "cloudflaredConfig",
		"subscriptionDir", "hysteriaConfigPath", "systemdUnitDir",
	} {
		got := nodePathVars()[name]
		if !strings.HasPrefix(got, dir) {
			t.Errorf("%s = %q, want a path under the test temp dir %q", name, got, dir)
		}
	}
}
