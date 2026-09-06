package commands

import (
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/paths"
)

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
