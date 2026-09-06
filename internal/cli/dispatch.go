package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/kulinh/cf-vpn/internal/cert"
	"github.com/kulinh/cf-vpn/internal/cloudflare"
	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/netinfo"
	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
)

var envFile = paths.EnvFile

var runInstall = commands.RunInstall
var runUpgrade = commands.RunUpgrade
var runUpgradeCheck = commands.RunUpgradeCheck
var buildInstallDeps = func(env map[string]string) commands.InstallDeps {
	deps := commands.InstallDeps{IP: netinfo.NewDefault(), Cert: cert.NewDefault(), UFW: commands.NewExecUFW(), BinaryRunner: systemd.ExecRunner{}, SystemdRunner: systemd.ExecRunner{}}
	deps.CF = cloudflare.DefaultClient(env["CF_API_TOKEN"], env["CF_ACCOUNT_ID"])
	return deps
}

// installFromEnv reads MODE, HY2_HOST, HY2_PORT (and optionally HY2_OBFS_PW, HY2_PASS_USER1)
// from env and populates InstallInputs. MODE is required; missing MODE returns "mode_required".
func installFromEnv(env map[string]string) (commands.InstallInputs, error) {
	mode := env["MODE"]
	if mode == "" {
		return commands.InstallInputs{}, fmt.Errorf("mode_required")
	}
	return commands.InstallInputs{
		CFAPIToken:     env["CF_API_TOKEN"],
		CFAccountID:    env["CF_ACCOUNT_ID"],
		Domain:         env["DOMAIN"],
		NodeID:         env["NODE_ID"],
		User1Name:      env["USER1_NAME"],
		Mode:           mode,
		Hy2Host:        env["HY2_HOST"],
		Hy2Port:        env["HY2_PORT"],
		Hy2ObfsPW:      env["HY2_OBFS_PW"],
		Hy2PassUser1:   env["HY2_PASS_USER1"],
		XrayDNSServers: env["XRAY_DNS_SERVERS"],
	}, nil
}

func parseUpgradeArgs(args []string, allowCheck bool) (commands.UpgradeInputs, bool, bool) {
	in := commands.UpgradeInputs{Mode: "direct", Now: time.Now}
	check := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check":
			if !allowCheck {
				return commands.UpgradeInputs{}, false, false
			}
			check = true
		case "--mode":
			if i+1 >= len(args) || (args[i+1] != "direct" && args[i+1] != "cloudflare" && args[i+1] != "auto") {
				return commands.UpgradeInputs{}, false, false
			}
			in.Mode = args[i+1]
			i++
		default:
			return commands.UpgradeInputs{}, false, false
		}
	}
	return in, check, true
}

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "usage: cfvpnctl <command>")
		return 0
	}

	// Bound every command so a hung external step (lego DNS propagation, curl,
	// systemctl) can't wedge a node forever when driven from cron.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	switch args[0] {
	case "help":
		fmt.Fprintln(stdout, "usage: cfvpnctl <command>")
		return 0
	case "install":
		upgrade := false
		upgradeArgs := make([]string, 0, len(args[1:]))
		for _, arg := range args[1:] {
			if arg == "--upgrade" {
				upgrade = true
				continue
			}
			upgradeArgs = append(upgradeArgs, arg)
		}
		upgradeIn, check, ok := parseUpgradeArgs(upgradeArgs, true)
		if !ok || check && !upgrade || !upgrade && len(upgradeArgs) > 0 {
			fmt.Fprintln(stderr, "usage: cfvpnctl install [--upgrade [--check] [--mode auto|direct|cloudflare]]")
			return 2
		}
		if upgrade {
			env, err := state.Load(envFile)
			if err != nil {
				fmt.Fprintf(stderr, "cannot read env file %s: %v\n", envFile, err)
				return 1
			}
			deps := buildInstallDeps(env)
			if check {
				if err := runUpgradeCheck(ctx, upgradeIn, deps, stdout, stderr); err != nil {
					fmt.Fprintln(stderr, err)
					return 1
				}
				return 0
			}
			if _, err := runUpgrade(ctx, upgradeIn, deps, stdout, stderr); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		}
		env, err := state.Load(envFile)
		if err != nil {
			fmt.Fprintf(stderr, "cannot read env file %s: %v\n", envFile, err)
			return 1
		}
		in, err := installFromEnv(env)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		deps := buildInstallDeps(env)
		if err := runInstall(ctx, in, deps, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "upgrade":
		upgradeIn, check, ok := parseUpgradeArgs(args[1:], false)
		if !ok || check {
			fmt.Fprintln(stderr, "usage: cfvpnctl upgrade [--mode auto|direct|cloudflare]")
			return 2
		}
		env, err := state.Load(envFile)
		if err != nil {
			fmt.Fprintf(stderr, "cannot read env file %s: %v\n", envFile, err)
			return 1
		}
		deps := buildInstallDeps(env)
		if _, err := runUpgrade(ctx, upgradeIn, deps, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "add-user":
		if len(args) < 2 || args[1] == "" {
			fmt.Fprintln(stderr, "usage: cfvpnctl add-user <name>")
			return 2
		}
		env, err := state.Load(paths.EnvFile)
		if err != nil {
			fmt.Fprintf(stderr, "cannot read env file %s: %v\n", paths.EnvFile, err)
			return 1
		}
		in := commands.UserInputs{Name: args[1], Domain: env["DOMAIN"]}
		if err := commands.RunAddUser(ctx, in, nil, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "remove-user":
		var (
			name string
			yes  bool
		)
		for _, arg := range args[1:] {
			if arg == "--yes" {
				yes = true
				continue
			}
			if name == "" {
				name = arg
				continue
			}
			fmt.Fprintln(stderr, "usage: cfvpnctl remove-user <name> --yes")
			return 2
		}
		if name == "" {
			fmt.Fprintln(stderr, "usage: cfvpnctl remove-user <name> --yes")
			return 2
		}
		if !yes {
			fmt.Fprintln(stderr, "refusing destructive operation without --yes")
			fmt.Fprintln(stderr, "usage: cfvpnctl remove-user <name> --yes")
			return 2
		}
		in := commands.UserInputs{Name: name}
		if err := commands.RunRemoveUser(ctx, in, nil, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "gen-sub":
		env, err := state.Load(paths.EnvFile)
		if err != nil {
			fmt.Fprintf(stderr, "cannot read env file %s: %v\n", paths.EnvFile, err)
			return 1
		}
		var name string
		if len(args) >= 2 {
			name = args[1]
		}
		in := commands.UserInputs{Name: name, Domain: env["DOMAIN"]}
		if err := commands.RunGenSub(ctx, in, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "rotate-domain":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cfvpnctl rotate-domain <new-domain> | --cleanup <uuid>")
			return 2
		}
		if args[1] == "--cleanup" {
			var (
				tunnelID string
				yes      bool
			)
			for _, arg := range args[2:] {
				if arg == "--yes" {
					yes = true
					continue
				}
				if tunnelID == "" {
					tunnelID = arg
					continue
				}
				fmt.Fprintln(stderr, "usage: cfvpnctl rotate-domain --cleanup <uuid> --yes")
				return 2
			}
			if tunnelID == "" {
				fmt.Fprintln(stderr, "usage: cfvpnctl rotate-domain --cleanup <uuid> --yes")
				return 2
			}
			if !yes {
				fmt.Fprintln(stderr, "refusing destructive operation without --yes")
				fmt.Fprintln(stderr, "usage: cfvpnctl rotate-domain --cleanup <uuid> --yes")
				return 2
			}
			env, err := state.Load(paths.EnvFile)
			if err != nil {
				fmt.Fprintf(stderr, "cannot read env file %s: %v\n", paths.EnvFile, err)
				return 1
			}
			deps := commands.RotateDeps{
				CF:     cloudflare.DefaultClient(env["CF_API_TOKEN"], env["CF_ACCOUNT_ID"]),
				Runner: systemd.ExecRunner{},
			}
			if err := commands.RunRotateCleanup(ctx, tunnelID, deps, stdout, stderr); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		}
		fmt.Fprintln(stderr, "tunnel-mode rotate-domain is deprecated; use panel rotate for direct mode")
		fmt.Fprintln(stderr, "usage: cfvpnctl rotate-domain --cleanup <uuid> --yes")
		return 2
	case "status":
		if err := commands.RunStatus(ctx, systemd.ExecRunner{}, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "cert-renew":
		env, err := state.Load(paths.EnvFile)
		if err != nil {
			fmt.Fprintf(stderr, "cannot read env file %s: %v\n", paths.EnvFile, err)
			return 1
		}
		if err := commands.RunCertRenew(ctx, env, commands.CertRenewDeps{Cert: cert.NewDefault(), Runner: systemd.ExecRunner{}}, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "healthcheck":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cfvpnctl healthcheck {run|install}")
			return 2
		}
		switch args[1] {
		case "run":
			env, err := state.Load(paths.EnvFile)
			if err != nil {
				fmt.Fprintf(stderr, "cannot read env file %s: %v\n", paths.EnvFile, err)
				return 1
			}
			if env["DOMAIN"] == "" {
				fmt.Fprintln(stderr, "usage: cfvpnctl healthcheck run (DOMAIN must be set)")
				return 2
			}
			if err := commands.RunHealthcheckRun(ctx, env, stdout); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		case "install":
			if err := commands.RunHealthcheckInstall(ctx, systemd.ExecRunner{}, stdout); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		default:
			fmt.Fprintln(stderr, "usage: cfvpnctl healthcheck {run|install}")
			return 2
		}
	case "tune-net":
		if len(args) > 1 {
			fmt.Fprintln(stderr, "usage: cfvpnctl tune-net")
			return 2
		}
		if err := commands.RunTuneNet(ctx, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "reconcile-units":
		if err := commands.RunReconcileUnits(ctx, systemd.ExecRunner{}, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}
