package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/commands"
)

func TestRunUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	exitCode := Run([]string{"nope"}, &out, &err)
	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code")
	}
	if !strings.Contains(err.String(), "unknown command") {
		t.Fatalf("expected unknown command message, got %q", err.String())
	}
}

func TestRunNoArgs(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	exitCode := Run(nil, &out, &errOut)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(out.String(), "usage") {
		t.Fatalf("expected usage on stdout, got %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", errOut.String())
	}
}

func TestRunHelp(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	exitCode := Run([]string{"help"}, &out, &errOut)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(out.String(), "usage") {
		t.Fatalf("expected usage on stdout, got %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", errOut.String())
	}
}

func TestRunInstallUnknownArgReturns2(t *testing.T) {
	var out, errBuf bytes.Buffer
	exit := Run([]string{"install", "--bogus"}, &out, &errBuf)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if !strings.Contains(errBuf.String(), "usage: cfvpnctl install") {
		t.Fatalf("expected usage, got %q", errBuf.String())
	}
}

func TestRunInstallUpgradeCheckDispatches(t *testing.T) {
	envPath := writeDispatchEnv(t)
	oldEnv, oldBuild, oldCheck := envFile, buildInstallDeps, runUpgradeCheck
	envFile = envPath
	called := false
	buildInstallDeps = func(env map[string]string) commands.InstallDeps {
		if env["CF_API_TOKEN"] != "token" || env["CF_ACCOUNT_ID"] != "acct" {
			t.Fatalf("bad env passed to deps builder: %#v", env)
		}
		return commands.InstallDeps{}
	}
	runUpgradeCheck = func(_ context.Context, in commands.UpgradeInputs, _ commands.InstallDeps, _, _ io.Writer) error {
		called = true
		if in.Mode != "cloudflare" {
			t.Fatalf("mode = %q, want cloudflare", in.Mode)
		}
		return nil
	}
	t.Cleanup(func() { envFile, buildInstallDeps, runUpgradeCheck = oldEnv, oldBuild, oldCheck })
	var out, errBuf bytes.Buffer
	exit := Run([]string{"install", "--upgrade", "--check", "--mode", "cloudflare"}, &out, &errBuf)
	if exit != 0 || !called {
		t.Fatalf("exit=%d called=%v stderr=%q", exit, called, errBuf.String())
	}
}

func TestRunInstallUpgradeDefaultsToDirect(t *testing.T) {
	envPath := writeDispatchEnv(t)
	oldEnv, oldBuild, oldRun := envFile, buildInstallDeps, runUpgrade
	envFile = envPath
	called := false
	buildInstallDeps = func(map[string]string) commands.InstallDeps { return commands.InstallDeps{} }
	runUpgrade = func(_ context.Context, in commands.UpgradeInputs, _ commands.InstallDeps, _, _ io.Writer) (commands.UpgradeResult, error) {
		called = true
		if in.Mode != "direct" {
			t.Fatalf("mode = %q, want direct", in.Mode)
		}
		return commands.UpgradeResult{}, nil
	}
	t.Cleanup(func() { envFile, buildInstallDeps, runUpgrade = oldEnv, oldBuild, oldRun })
	var out, errBuf bytes.Buffer
	exit := Run([]string{"install", "--upgrade"}, &out, &errBuf)
	if exit != 0 || !called {
		t.Fatalf("exit=%d called=%v stderr=%q", exit, called, errBuf.String())
	}
}

func TestRunUpgradeAliasDispatches(t *testing.T) {
	envPath := writeDispatchEnv(t)
	oldEnv, oldBuild, oldRun := envFile, buildInstallDeps, runUpgrade
	envFile = envPath
	called := false
	buildInstallDeps = func(env map[string]string) commands.InstallDeps {
		if env["CF_API_TOKEN"] != "token" || env["CF_ACCOUNT_ID"] != "acct" {
			t.Fatalf("bad env passed to deps builder: %#v", env)
		}
		return commands.InstallDeps{}
	}
	runUpgrade = func(_ context.Context, in commands.UpgradeInputs, _ commands.InstallDeps, _, _ io.Writer) (commands.UpgradeResult, error) {
		called = true
		if in.Mode != "cloudflare" {
			t.Fatalf("mode = %q, want cloudflare", in.Mode)
		}
		return commands.UpgradeResult{}, nil
	}
	t.Cleanup(func() { envFile, buildInstallDeps, runUpgrade = oldEnv, oldBuild, oldRun })
	var out, errBuf bytes.Buffer
	exit := Run([]string{"upgrade", "--mode", "cloudflare"}, &out, &errBuf)
	if exit != 0 || !called {
		t.Fatalf("exit=%d called=%v stderr=%q", exit, called, errBuf.String())
	}
}

func TestRunInstallUpgradeEnvLoadError(t *testing.T) {
	oldEnv := envFile
	envFile = t.TempDir() + "/missing.env"
	t.Cleanup(func() { envFile = oldEnv })
	var out, errBuf bytes.Buffer
	exit := Run([]string{"install", "--upgrade"}, &out, &errBuf)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
	if !strings.Contains(errBuf.String(), "cannot read env file") {
		t.Fatalf("expected env-load failure, got %q", errBuf.String())
	}
}

func TestRunInstallCommandDispatches(t *testing.T) {
	envPath := writeDispatchEnv(t)
	oldEnv, oldBuild, oldRun := envFile, buildInstallDeps, runInstall
	envFile = envPath
	called := false
	buildInstallDeps = func(map[string]string) commands.InstallDeps { return commands.InstallDeps{} }
	runInstall = func(_ context.Context, in commands.InstallInputs, _ commands.InstallDeps, _, _ io.Writer) error {
		called = true
		if in.CFAPIToken != "token" || in.CFAccountID != "acct" || in.Domain != "vpn.example.com" || in.NodeID != "JPY-04" || in.User1Name != "alice" || in.Mode != "direct" || in.Hy2Host != "hy2.vpn.example.com" || in.Hy2Port != "21000" {
			t.Fatalf("bad install inputs: %#v", in)
		}
		return nil
	}
	t.Cleanup(func() { envFile, buildInstallDeps, runInstall = oldEnv, oldBuild, oldRun })
	var out, errBuf bytes.Buffer
	exit := Run([]string{"install"}, &out, &errBuf)
	if exit != 0 || !called {
		t.Fatalf("exit=%d called=%v stderr=%q", exit, called, errBuf.String())
	}
}

func writeDispatchEnv(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/cfvpn.env"
	content := "CF_API_TOKEN=token\nCF_ACCOUNT_ID=acct\nNODE_ID=JPY-04\nDOMAIN=vpn.example.com\nUSER1_NAME=alice\nMODE=direct\nHY2_HOST=hy2.vpn.example.com\nHY2_PORT=21000\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
