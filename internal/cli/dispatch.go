package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "usage: cfvpnctl <command>")
		return 0
	}

	switch args[0] {
	case "help":
		fmt.Fprintln(stdout, "usage: cfvpnctl <command>")
		return 0
	case "install":
		env, err := state.Load(paths.EnvFile)
		if err != nil {
			fmt.Fprintf(stderr, "cannot read env file %s: %v\n", paths.EnvFile, err)
			return 1
		}
		in := commands.InstallInputs{
			CFAPIToken:  env["CF_API_TOKEN"],
			CFAccountID: env["CF_ACCOUNT_ID"],
			Domain:      env["DOMAIN"],
			User1Name:   env["USER1_NAME"],
		}
		if err := commands.RunInstall(context.Background(), in, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}
