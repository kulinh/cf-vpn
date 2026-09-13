package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/kulinh/cf-vpn/internal/cert"
	"github.com/kulinh/cf-vpn/internal/cloudflare"
	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/netinfo"
	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/tailscale"
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

// installFromEnv reads MODE, HY2_HOST, HY2_PORT, ADMIN_TUNNEL_UUID (and optionally
// HY2_OBFS_PW, HY2_PASS_USER1) from env and populates InstallInputs. MODE is required; missing MODE returns "mode_required".
func installFromEnv(env map[string]string) (commands.InstallInputs, error) {
	mode := env["MODE"]
	if mode == "" {
		return commands.InstallInputs{}, fmt.Errorf("mode_required")
	}
	return commands.InstallInputs{
		CFAPIToken:      env["CF_API_TOKEN"],
		CFAccountID:     env["CF_ACCOUNT_ID"],
		Domain:          env["DOMAIN"],
		NodeID:          env["NODE_ID"],
		User1Name:       env["USER1_NAME"],
		Mode:            mode,
		Hy2Host:         env["HY2_HOST"],
		Hy2Port:         env["HY2_PORT"],
		Hy2ObfsPW:       env["HY2_OBFS_PW"],
		Hy2PassUser1:    env["HY2_PASS_USER1"],
		AdminTunnelUUID: env["ADMIN_TUNNEL_UUID"],
		XrayDNSServers:  env["XRAY_DNS_SERVERS"],
		RealityDest:     env["REALITY_DEST"],
		RealitySNI:      env["REALITY_SNI"],
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
		case "--binaries":
			in.Binaries = true
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
			fmt.Fprintln(stderr, "usage: cfvpnctl upgrade [--mode auto|direct|cloudflare] [--binaries]")
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
	case "rotate-reality":
		var dest, sni string
		usage := "usage: cfvpnctl rotate-reality --dest <host:443> [--sni <host>]"
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--dest":
				if i+1 >= len(args) {
					fmt.Fprintln(stderr, usage)
					return 2
				}
				dest = args[i+1]
				i++
			case "--sni":
				if i+1 >= len(args) {
					fmt.Fprintln(stderr, usage)
					return 2
				}
				sni = args[i+1]
				i++
			default:
				fmt.Fprintln(stderr, usage)
				return 2
			}
		}
		if dest == "" {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		if err := commands.RunRotateReality(ctx, commands.RotateRealityInputs{Dest: dest, SNI: sni}, systemd.ExecRunner{}, nil, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "xhttp":
		if len(args) != 2 || (args[1] != "enable" && args[1] != "disable") {
			fmt.Fprintln(stderr, "usage: cfvpnctl xhttp {enable|disable}")
			return 2
		}
		if err := commands.RunXHTTPSet(ctx, args[1] == "enable", systemd.ExecRunner{}, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "derp":
		return runDerp(ctx, args[1:], stdout, stderr)
	case "xhttp-direct":
		host, path, ok := parseEnableDisableHostPath(args, "xhttp-direct", stderr)
		if !ok {
			return 2
		}
		if err := commands.RunXHTTPDirectSet(ctx, host, path, systemd.ExecRunner{}, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "xhttp-h3":
		host, path, ok := parseEnableDisableHostPath(args, "xhttp-h3", stderr)
		if !ok {
			return 2
		}
		if err := commands.RunXHTTPH3Set(ctx, host, path, systemd.ExecRunner{}, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "hy2":
		if len(args) != 2 || (args[1] != "enable" && args[1] != "disable") {
			fmt.Fprintln(stderr, "usage: cfvpnctl hy2 {enable|disable}")
			return 2
		}
		env, err := state.Load(envFile)
		if err != nil {
			fmt.Fprintf(stderr, "cannot read env file %s: %v\n", envFile, err)
			return 1
		}
		if err := commands.RunHy2Set(ctx, args[1] == "enable", buildInstallDeps(env), stdout, stderr); err != nil {
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

// runDerp handles `cfvpnctl derp ...`: the tailnet's custom DERP regions and
// the china-mode flag (derpMap.OmitDefaultRegions), edited through the
// Tailscale API with the OAuth client in /etc/cfvpn/tailscale-oauth.env.
func runDerp(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := "usage: cfvpnctl derp china-mode on|off | derp show | derp region add --id N --code C --name NAME --host H [--derp-port 8443] [--stun-port 3478] | derp region remove --id N"
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	deps := commands.DerpDeps{}
	switch args[0] {
	case "china-mode":
		if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		if err := commands.RunDerpChinaMode(ctx, args[1] == "on", deps, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "show":
		if err := commands.RunDerpShow(ctx, deps, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "region":
		if len(args) < 2 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		r := tailscale.Region{DERPPort: 8443, STUNPort: 3478}
		for i := 2; i < len(args); i++ {
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, usage)
				return 2
			}
			v := args[i+1]
			switch args[i] {
			case "--id":
				n, err := strconv.Atoi(v)
				if err != nil {
					fmt.Fprintln(stderr, usage)
					return 2
				}
				r.ID = n
			case "--code":
				r.Code = v
			case "--name":
				r.Name = v
			case "--host":
				r.HostName = v
			case "--derp-port":
				n, err := strconv.Atoi(v)
				if err != nil {
					fmt.Fprintln(stderr, usage)
					return 2
				}
				r.DERPPort = n
			case "--stun-port":
				n, err := strconv.Atoi(v)
				if err != nil {
					fmt.Fprintln(stderr, usage)
					return 2
				}
				r.STUNPort = n
			default:
				fmt.Fprintln(stderr, usage)
				return 2
			}
			i++
		}
		switch args[1] {
		case "add":
			if err := commands.RunDerpRegionAdd(ctx, r, deps, stdout); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		case "remove":
			if r.ID == 0 {
				fmt.Fprintln(stderr, usage)
				return 2
			}
			if err := commands.RunDerpRegionRemove(ctx, r.ID, deps, stdout); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		}
		fmt.Fprintln(stderr, usage)
		return 2
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
}

// parseEnableDisableHostPath parses the shared "<cmd> enable --host H --path P
// | <cmd> disable" shape used by the xhttp-direct and xhttp-h3 routes. It
// returns empty host and path for "disable" — which is how both RunXHTTP*Set
// functions spell "turn this off" — and ok=false after printing the usage line
// for anything malformed.
func parseEnableDisableHostPath(args []string, cmd string, stderr io.Writer) (host, path string, ok bool) {
	usage := "usage: cfvpnctl " + cmd + " enable --host <host> --path </long-random-path> | disable"
	fail := func() (string, string, bool) {
		fmt.Fprintln(stderr, usage)
		return "", "", false
	}
	if len(args) < 2 {
		return fail()
	}
	switch args[1] {
	case "disable":
		if len(args) != 2 {
			return fail()
		}
		return "", "", true
	case "enable":
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--host":
				if i+1 >= len(args) {
					return fail()
				}
				host = args[i+1]
				i++
			case "--path":
				if i+1 >= len(args) {
					return fail()
				}
				path = args[i+1]
				i++
			default:
				return fail()
			}
		}
		if host == "" || path == "" {
			return fail()
		}
		return host, path, true
	default:
		return fail()
	}
}
