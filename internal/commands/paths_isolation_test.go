package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/paths"
)

// TestMain installs package-wide safety defaults for the seams that would
// otherwise touch the machine running the suite.
//
// This package's tests are routinely run as root on a node (that is where the
// repo lives). Anything that writes outside a t.TempDir or execs a system tool
// must be redirected here, not per test: a helper that forgets one override
// silently reconfigures the live node — as happened with hysteriaConfigPath,
// whose fixtures replaced a node's real hysteria config.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cfvpn-commands-test-*")
	if err != nil {
		panic(err)
	}
	sysctlConfPath = filepath.Join(dir, "sysctl.d", "90-cfvpn.conf")
	runTuneCommand = func(context.Context, string, ...string) ([]byte, error) { return []byte("bbr\n"), nil }
	// Fixture Reality keys are correctly rejected by a real xray; tests that
	// exercise the pre-flight call realValidateXrayConfig directly.
	validateXrayConfig = func(context.Context, []byte) error { return nil }

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// TestMain asserts that no test in this package leaves the mutable node paths
// pointing at the real /etc/cfvpn. Every seam helper redirects them to a temp
// dir and restores them in t.Cleanup; when one forgets (withUpgradeSeams once
// forgot hysteriaConfigPath) the tests silently rewrite the live config of the
// machine running `go test`, which on a node means replacing hysteria's port,
// obfs password and user set with fixtures.
//
// Running this as a plain test after the others is not possible — order is not
// guaranteed — so each guard below is cheap and checked here at the boundary.
func TestNodePathVarsMatchProductionDefaultsAtStart(t *testing.T) {
	cases := map[string]struct{ got, want string }{
		"envFilePath":        {envFilePath, paths.EnvFile},
		"xrayConfigPath":     {xrayConfigPath, paths.XrayConfigFile},
		"cloudflaredConfig":  {cloudflaredConfig, paths.CloudflaredConfig},
		"subscriptionDir":    {subscriptionDir, paths.SubscriptionDir},
		"hysteriaConfigPath": {hysteriaConfigPath, "/etc/cfvpn/hysteria/config.yaml"},
		"systemdUnitDir":     {systemdUnitDir, "/etc/systemd/system"},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q at test start, want the production default %q — a previous "+
				"test leaked its override (or a helper forgot to restore it)", name, c.got, c.want)
		}
	}
}

// A helper that redirects the node paths must redirect ALL of them; this test
// documents the full set so a new path var is not forgotten.
func TestSeamHelperRedirectsEveryNodePath(t *testing.T) {
	dir := withUpgradeSeams(t)
	for name, got := range map[string]string{
		"envFilePath":        envFilePath,
		"xrayConfigPath":     xrayConfigPath,
		"cloudflaredConfig":  cloudflaredConfig,
		"subscriptionDir":    subscriptionDir,
		"hysteriaConfigPath": hysteriaConfigPath,
		"systemdUnitDir":     systemdUnitDir,
	} {
		if !strings.HasPrefix(got, dir) {
			t.Errorf("%s = %q, want a path under the test temp dir %q", name, got, dir)
		}
	}
}
